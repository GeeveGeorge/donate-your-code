// Package thread reconstructs the conversation thread around Fable 5 turns and
// builds scrubbed, content-addressed donation records. It walks the parentUuid
// DAG, anchors on the last genuine Fable 5 turn, and projects an allowlist of
// fields into a record. It fails closed: any unresolved external file or any
// tripwire hit drops the whole record.
package thread

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"

	"github.com/GeeveGeorge/donate-your-code/internal/discover"
	"github.com/GeeveGeorge/donate-your-code/internal/record"
	"github.com/GeeveGeorge/donate-your-code/internal/scrub"
	"github.com/GeeveGeorge/donate-your-code/internal/transcript"
)

// Builder turns parsed transcripts into donation records.
type Builder struct {
	scrubber      *scrub.Scrubber
	salt          []byte
	clientVersion string
}

// NewBuilder constructs a Builder. salt is the per-donor secret used to derive
// parent_session_hash; it is never donated.
func NewBuilder(scrubber *scrub.Scrubber, salt []byte, clientVersion string) *Builder {
	return &Builder{scrubber: scrubber, salt: salt, clientVersion: clientVersion}
}

// Result reports the outcome of building one record from one transcript file.
type Result struct {
	Record *record.Record
	Status string // "ok" | "no-fable" | "dropped" | "parse-error"
	Reason string
}

// BuildSession builds records for a session bundle: one for the main transcript
// and one for each subagent transcript that contains a genuine Fable 5 turn.
func (b *Builder) BuildSession(s discover.Session) []Result {
	var results []Result

	if f, err := discover.SafeOpen(s.MainFile, s.Root); err == nil {
		lines, _, perr := transcript.ParseReader(f)
		f.Close()
		if perr != nil {
			results = append(results, Result{Status: "parse-error", Reason: perr.Error()})
		} else {
			results = append(results, b.buildOne(lines, false, "", s))
		}
	}

	parentHash := b.sessionHash(s.SessionID)
	for _, sub := range s.SubagentFiles {
		f, err := discover.SafeOpen(sub, s.Root)
		if err != nil {
			continue
		}
		lines, _, perr := transcript.ParseReader(f)
		f.Close()
		if perr != nil {
			results = append(results, Result{Status: "parse-error", Reason: perr.Error()})
			continue
		}
		results = append(results, b.buildOne(lines, true, parentHash, s))
	}
	return results
}

func (b *Builder) sessionHash(sessionID string) string {
	h := sha256.New()
	h.Write(b.salt)
	h.Write([]byte(sessionID))
	return hex.EncodeToString(h.Sum(nil))
}

// buildOne reconstructs the main thread (root → last genuine Fable 5 turn),
// scrubs it, and returns a record or a non-ok status.
func (b *Builder) buildOne(lines []*transcript.Line, isSubagent bool, parentHash string, s discover.Session) Result {
	chain := mainThread(lines)
	if len(chain) == 0 {
		return Result{Status: "no-fable"}
	}

	conv := &converter{
		scrubber:       b.scrubber,
		toolResultsDir: s.ToolResultsDir,
		refByToolUse:   map[string]int{},
	}

	var msgs []record.Message
	modelsSeen := map[string]bool{}
	claudeVersion := ""
	for _, l := range chain {
		if l.IsSynthetic() {
			continue
		}
		m, ok := conv.convertLine(l)
		if !ok {
			if conv.dropReason != "" {
				return Result{Status: "dropped", Reason: conv.dropReason}
			}
			continue
		}
		if m.Role == "assistant" && m.Model != "" {
			modelsSeen[m.Model] = true
		}
		if l.Version != "" {
			claudeVersion = l.Version
		}
		msgs = append(msgs, m)
	}
	if len(msgs) == 0 {
		return Result{Status: "no-fable"}
	}

	models := make([]string, 0, len(modelsSeen))
	for k := range modelsSeen {
		models = append(models, k)
	}
	sort.Strings(models)

	rec := &record.Record{
		SchemaVersion:     record.SchemaVersion,
		Model:             record.FableModel,
		Provenance:        record.ProvenanceSelfAttested,
		DCO:               false,
		License:           record.LicenseCC0,
		ClientVersion:     b.clientVersion,
		ScrubberVersion:   scrub.Version(),
		ClaudeCodeVersion: claudeVersion,
		IsSubagent:        isSubagent,
		ModelsPresent:     models,
		Messages:          msgs,
		RedactionSummary:  toSummary(conv.counts),
	}
	if isSubagent {
		rec.ParentSessionHash = parentHash
	}
	if _, err := rec.ComputeID(); err != nil {
		return Result{Status: "dropped", Reason: "canonicalize: " + err.Error()}
	}

	// Fail-closed tripwire over the fully serialized record.
	serialized, err := json.Marshal(rec)
	if err != nil {
		return Result{Status: "dropped", Reason: "marshal: " + err.Error()}
	}
	if hits := b.scrubber.Tripwire(string(serialized)); len(hits) > 0 {
		return Result{Status: "dropped", Reason: "tripwire: " + joinStrings(hits)}
	}

	return Result{Record: rec, Status: "ok"}
}

// mainThread anchors on the genuine Fable 5 turn with the latest timestamp and
// walks parentUuid edges to the root, guarding against cycles and dangling
// parents. The returned slice is ordered root → anchor.
func mainThread(lines []*transcript.Line) []*transcript.Line {
	byUUID := make(map[string]*transcript.Line, len(lines))
	for _, l := range lines {
		if l.UUID != "" {
			byUUID[l.UUID] = l
		}
	}
	var anchor *transcript.Line
	for _, l := range lines {
		if l.IsGenuineFable() && (anchor == nil || l.Timestamp > anchor.Timestamp) {
			anchor = l
		}
	}
	if anchor == nil {
		return nil
	}
	var chain []*transcript.Line
	seen := map[string]bool{}
	cur := anchor
	for cur != nil {
		if seen[cur.UUID] {
			break
		}
		seen[cur.UUID] = true
		chain = append(chain, cur)
		if cur.ParentUUID == nil {
			break
		}
		parent, ok := byUUID[*cur.ParentUUID]
		if !ok {
			break
		}
		cur = parent
	}
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}
	return chain
}

func toSummary(c scrub.Counts) record.RedactionSummary {
	return record.RedactionSummary{
		Keys:        c.Keys,
		Secrets:     c.Secrets,
		HighEntropy: c.HighEntropy,
		Emails:      c.Emails,
		Phones:      c.Phones,
		Cards:       c.Cards,
		IPs:         c.IPs,
		MACs:        c.MACs,
		Paths:       c.Paths,
		Usernames:   c.Usernames,
		Images:      c.Images,
	}
}

func joinStrings(ss []string) string {
	out := ""
	for i, s := range ss {
		if i > 0 {
			out += ","
		}
		out += s
	}
	return out
}
