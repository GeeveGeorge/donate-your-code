// Package scrub is the deterministic, fail-closed redaction pipeline. It is the
// client's first line of defense (the server re-runs a heavier, authoritative
// pass). It contains no ML and no network: same input always yields the same
// output, which is required for content-addressed record ids.
package scrub

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
)

// Counts tallies redactions for the preview/redaction summary.
type Counts struct {
	Keys        int
	Secrets     int
	HighEntropy int
	Emails      int
	Phones      int
	Cards       int
	IPs         int
	MACs        int
	Paths       int
	Usernames   int
	Images      int
}

// Add merges another Counts into c.
func (c *Counts) Add(o Counts) {
	c.Keys += o.Keys
	c.Secrets += o.Secrets
	c.HighEntropy += o.HighEntropy
	c.Emails += o.Emails
	c.Phones += o.Phones
	c.Cards += o.Cards
	c.IPs += o.IPs
	c.MACs += o.MACs
	c.Paths += o.Paths
	c.Usernames += o.Usernames
	c.Images += o.Images
}

type rule struct {
	name string
	re   *regexp.Regexp
	repl string
}

var (
	privateKeyRe = regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*?-----END [A-Z0-9 ]*PRIVATE KEY-----`)
	pgpBlockRe   = regexp.MustCompile(`(?s)-----BEGIN PGP [A-Z ]*-----.*?-----END PGP [A-Z ]*-----`)

	// High-confidence vendor secret rules (gitleaks-style). Order is stable so
	// the ruleset hash (Version) is deterministic.
	secretRules = []rule{
		{"aws-access-key", regexp.MustCompile(`AKIA[0-9A-Z]{16}`), "«SECRET:aws»"},
		{"github-token", regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{36,}`), "«SECRET:github»"},
		{"github-pat", regexp.MustCompile(`github_pat_[A-Za-z0-9_]{22,}`), "«SECRET:github»"},
		{"slack-token", regexp.MustCompile(`xox[baprs]-[A-Za-z0-9-]{10,}`), "«SECRET:slack»"},
		{"slack-webhook", regexp.MustCompile(`https://hooks\.slack\.com/services/[A-Za-z0-9/]+`), "«SECRET:slack»"},
		{"stripe-key", regexp.MustCompile(`(?:sk|rk)_live_[A-Za-z0-9]{16,}`), "«SECRET:stripe»"},
		{"google-api-key", regexp.MustCompile(`AIza[0-9A-Za-z\-_]{35}`), "«SECRET:google»"},
		{"sendgrid-key", regexp.MustCompile(`SG\.[A-Za-z0-9_\-]{22}\.[A-Za-z0-9_\-]{43}`), "«SECRET:sendgrid»"},
		{"anthropic-key", regexp.MustCompile(`sk-ant-[A-Za-z0-9_\-]{20,}`), "«SECRET:anthropic»"},
		{"openai-key", regexp.MustCompile(`sk-[A-Za-z0-9]{20,}`), "«SECRET:openai»"},
		{"jwt", regexp.MustCompile(`eyJ[A-Za-z0-9_\-]+\.eyJ[A-Za-z0-9_\-]+\.[A-Za-z0-9_\-]+`), "«SECRET:jwt»"},
		{"npm-token", regexp.MustCompile(`npm_[A-Za-z0-9]{36}`), "«SECRET:npm»"},
		{"pypi-token", regexp.MustCompile(`pypi-AgEIcHlwaS5vcmc[A-Za-z0-9_\-]{50,}`), "«SECRET:pypi»"},
	}

	emailRe     = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)
	macRe       = regexp.MustCompile(`\b(?:[0-9A-Fa-f]{2}:){5}[0-9A-Fa-f]{2}\b`)
	ipv4Re      = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`)
	ipv6Re      = regexp.MustCompile(`\b(?:[0-9A-Fa-f]{0,4}:){2,7}[0-9A-Fa-f]{0,4}\b`)
	phoneIntlRe = regexp.MustCompile(`\+\d[\d \-().]{6,}\d`)
	phoneUSRe   = regexp.MustCompile(`\b\d{3}[-.]\d{3}[-.]\d{4}\b`)
	cardRe      = regexp.MustCompile(`\b(?:\d[ -]?){13,19}\b`)
	homeUnixRe  = regexp.MustCompile(`(/Users/|/home/)([^/\s:"']+)`)
	homeWinRe   = regexp.MustCompile(`([A-Za-z]:\\Users\\)([^\\\s:"']+)`)
	otherPathRe = regexp.MustCompile(`/(?:usr|etc|var|opt|tmp|private|bin|sbin|Applications|Library|System|Volumes|mnt|srv|root)(?:/[\w.\-+@]+)+`)
	tokenRe     = regexp.MustCompile(`[A-Za-z0-9+/=_\-]{20,}`)
)

// Scrubber redacts secrets and PII from text. It is constructed with the set of
// OS usernames to redact globally.
type Scrubber struct {
	usernames []string
}

// New returns a Scrubber that also redacts the given usernames wherever they
// appear (only names of length >= 3 are used, to avoid over-matching).
func New(usernames []string) *Scrubber {
	var us []string
	seen := map[string]bool{}
	for _, u := range usernames {
		if len(u) >= 3 && !seen[u] {
			seen[u] = true
			us = append(us, u)
		}
	}
	sort.Strings(us)
	return &Scrubber{usernames: us}
}

// Scrub redacts text. highRisk lowers the high-entropy threshold for content that
// originates from tool results / thinking / external files.
func (s *Scrubber) Scrub(text string, highRisk bool) (string, Counts) {
	var c Counts
	discovered := map[string]bool{}

	text = countReplaceAll(privateKeyRe, text, "«PRIVATE_KEY»", &c.Keys)
	text = countReplaceAll(pgpBlockRe, text, "«PRIVATE_KEY»", &c.Keys)

	for _, r := range secretRules {
		text = countReplaceAll(r.re, text, r.repl, &c.Secrets)
	}

	// Paths BEFORE high-entropy: the high-entropy token class includes '/', so a
	// path would otherwise be consumed as a single high-entropy token. Capture
	// the username from home paths, then redact prefixes.
	text = homeUnixRe.ReplaceAllStringFunc(text, func(m string) string {
		sm := homeUnixRe.FindStringSubmatch(m)
		if len(sm[2]) >= 3 {
			discovered[sm[2]] = true
		}
		c.Paths++
		return "«HOME»/"
	})
	text = homeWinRe.ReplaceAllStringFunc(text, func(m string) string {
		sm := homeWinRe.FindStringSubmatch(m)
		if len(sm[2]) >= 3 {
			discovered[sm[2]] = true
		}
		c.Paths++
		return `«HOME»\`
	})
	text = countReplaceAll(otherPathRe, text, "«PATH»", &c.Paths)

	// High-entropy tokens (after vendor + path rules so placeholders aren't re-scanned).
	thr := 3.6
	if highRisk {
		thr = 3.2
	}
	text = tokenRe.ReplaceAllStringFunc(text, func(tok string) string {
		if qualifiesHighEntropy(tok, thr) {
			c.HighEntropy++
			return "«HIGH_ENTROPY»"
		}
		return tok
	})

	text = countReplaceAll(emailRe, text, "«EMAIL»", &c.Emails)

	// Credit cards: only redact runs that pass the Luhn check.
	text = cardRe.ReplaceAllStringFunc(text, func(m string) string {
		digits := stripNonDigits(m)
		if len(digits) >= 13 && len(digits) <= 19 && luhn(digits) {
			c.Cards++
			return "«CARD»"
		}
		return m
	})

	text = macRe.ReplaceAllStringFunc(text, func(string) string { c.MACs++; return "«MAC»" })
	text = ipv4Re.ReplaceAllStringFunc(text, func(m string) string {
		if validIPv4(m) {
			c.IPs++
			return "«IP»"
		}
		return m
	})
	text = ipv6Re.ReplaceAllStringFunc(text, func(m string) string {
		if strings.Count(m, ":") >= 2 && containsHex(m) {
			c.IPs++
			return "«IP»"
		}
		return m
	})

	text = countReplaceAll(phoneIntlRe, text, "«PHONE»", &c.Phones)
	text = countReplaceAll(phoneUSRe, text, "«PHONE»", &c.Phones)

	// Global username redaction (provided ∪ discovered).
	names := append([]string{}, s.usernames...)
	for n := range discovered {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range dedupe(names) {
		re := regexp.MustCompile(`\b` + regexp.QuoteMeta(n) + `\b`)
		text = re.ReplaceAllStringFunc(text, func(string) string { c.Usernames++; return "«USER»" })
	}

	return text, c
}

// ScrubTree recursively redacts string leaves of a parsed JSON value (used for
// tool_use inputs). Numbers, bools, and nulls pass through unchanged.
func (s *Scrubber) ScrubTree(v any, highRisk bool) (any, Counts) {
	var c Counts
	switch t := v.(type) {
	case string:
		out, cc := s.Scrub(t, highRisk)
		c.Add(cc)
		return out, c
	case []any:
		for i := range t {
			nv, cc := s.ScrubTree(t[i], highRisk)
			t[i] = nv
			c.Add(cc)
		}
		return t, c
	case map[string]any:
		for k := range t {
			nv, cc := s.ScrubTree(t[k], highRisk)
			t[k] = nv
			c.Add(cc)
		}
		return t, c
	default:
		return v, c
	}
}

// Tripwire scans a fully-serialized record for unmistakable leaks that must never
// escape. A non-empty result means the record must be dropped (fail closed).
func (s *Scrubber) Tripwire(serialized string) []string {
	var hits []string
	markers := []string{
		"-----BEGIN", "PRIVATE KEY", "AKIA", "ghp_", "gho_", "ghs_", "ghu_",
		"github_pat_", "xox-", "sk-ant-",
	}
	for _, m := range markers {
		if strings.Contains(serialized, m) {
			hits = append(hits, m)
		}
	}
	if homeUnixRe.MatchString(serialized) || homeWinRe.MatchString(serialized) {
		hits = append(hits, "home-path")
	}
	if emailRe.MatchString(serialized) {
		hits = append(hits, "email")
	}
	for _, n := range s.usernames {
		if regexp.MustCompile(`\b` + regexp.QuoteMeta(n) + `\b`).MatchString(serialized) {
			hits = append(hits, "username")
			break
		}
	}
	return hits
}

// HashImage returns the placeholder used in place of raw image bytes.
func HashImage(raw []byte) string {
	sum := sha256.Sum256(raw)
	return fmt.Sprintf("«IMAGE:%s:%d»", hex.EncodeToString(sum[:]), len(raw))
}

// Version returns a stable hash of the ruleset so each record can record which
// scrubber produced it (enabling server-side re-flagging by improved scrubbers).
func Version() string {
	var b strings.Builder
	b.WriteString("dyc-scrub-v0\n")
	b.WriteString(privateKeyRe.String() + "\n")
	b.WriteString(pgpBlockRe.String() + "\n")
	for _, r := range secretRules {
		b.WriteString(r.name + "=" + r.re.String() + "\n")
	}
	for _, re := range []*regexp.Regexp{emailRe, macRe, ipv4Re, ipv6Re, phoneIntlRe, phoneUSRe, cardRe, homeUnixRe, homeWinRe, otherPathRe, tokenRe} {
		b.WriteString(re.String() + "\n")
	}
	sum := sha256.Sum256([]byte(b.String()))
	return "scrub-" + hex.EncodeToString(sum[:8])
}

func countReplaceAll(re *regexp.Regexp, text, repl string, counter *int) string {
	return re.ReplaceAllStringFunc(text, func(string) string { *counter++; return repl })
}

func qualifiesHighEntropy(tok string, thr float64) bool {
	if len(tok) < 20 {
		return false
	}
	hasDigit, hasSym := false, false
	for i := 0; i < len(tok); i++ {
		switch ch := tok[i]; {
		case ch >= '0' && ch <= '9':
			hasDigit = true
		case ch == '+' || ch == '/' || ch == '=' || ch == '_' || ch == '-':
			hasSym = true
		}
	}
	if !hasDigit && !hasSym {
		return false
	}
	return shannon(tok) >= thr
}

func shannon(s string) float64 {
	if s == "" {
		return 0
	}
	var freq [256]float64
	for i := 0; i < len(s); i++ {
		freq[s[i]]++
	}
	n := float64(len(s))
	h := 0.0
	for _, f := range freq {
		if f > 0 {
			p := f / n
			h -= p * math.Log2(p)
		}
	}
	return h
}

func luhn(digits string) bool {
	sum := 0
	alt := false
	for i := len(digits) - 1; i >= 0; i-- {
		d := int(digits[i] - '0')
		if alt {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
		alt = !alt
	}
	return sum%10 == 0
}

func stripNonDigits(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] >= '0' && s[i] <= '9' {
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

func validIPv4(s string) bool {
	parts := strings.Split(s, ".")
	if len(parts) != 4 {
		return false
	}
	for _, p := range parts {
		if len(p) == 0 || len(p) > 3 {
			return false
		}
		n := 0
		for i := 0; i < len(p); i++ {
			if p[i] < '0' || p[i] > '9' {
				return false
			}
			n = n*10 + int(p[i]-'0')
		}
		if n > 255 {
			return false
		}
	}
	return true
}

func containsHex(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F') {
			return true
		}
	}
	return false
}

func dedupe(sorted []string) []string {
	var out []string
	var last string
	for i, s := range sorted {
		if i == 0 || s != last {
			out = append(out, s)
			last = s
		}
	}
	return out
}
