// Command dyc is the Donate Your Code client: a deterministic, audited tool that
// finds genuine Claude Fable 5 turns in local Claude Code transcripts, scrubs
// them, and (in later versions) contributes them to a public dataset via a
// least-privilege GitHub PR. It is the trust boundary — an orchestrating agent
// only runs these subcommands; it never parses transcripts itself.
//
// v0 implements the read-only subcommands: scan and preview. These make no
// network calls at all.
package main

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/GeeveGeorge/donate-your-code/internal/discover"
	"github.com/GeeveGeorge/donate-your-code/internal/scrub"
	"github.com/GeeveGeorge/donate-your-code/internal/thread"
	"github.com/GeeveGeorge/donate-your-code/internal/transcript"
)

// version is overridden at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd := os.Args[1]
	args := os.Args[2:]
	switch cmd {
	case "scan":
		os.Exit(cmdScan(args))
	case "preview":
		os.Exit(cmdPreview(args))
	case "donate":
		os.Exit(cmdDonate(args))
	case "auth":
		os.Exit(cmdAuth(args))
	case "status":
		os.Exit(cmdStatus(args))
	case "version", "--version", "-v":
		fmt.Printf("dyc %s\n", version)
		fmt.Printf("scrubber %s\n", scrub.Version())
		fmt.Printf("record-schema %s\n", recordSchemaVersion)
	case "help", "--help", "-h":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "dyc: unknown command %q\n", cmd)
		usage()
		os.Exit(2)
	}
}

const recordSchemaVersion = "dyc.record.v1"

func usage() {
	fmt.Fprint(os.Stderr, `dyc — Donate Your Code (client)

Usage:
  dyc scan [--all] [--json]                 Enumerate sessions and count genuine Fable 5 turns (read-only, no network)
  dyc preview <selector> [--full] [--json]  Show the exact post-scrub payload for matching sessions (read-only, no network)
  dyc donate <selector> [--dry-run] [--yes] [--max-records N]
                                            Scrub, validate, and submit matching records as a GitHub PR
  dyc auth login|status|logout              Manage the GitHub token (prefers the gh CLI; never echoes the token)
  dyc status                                Show config roots, token source, donation count, and staging target
  dyc version                               Print version, scrubber ruleset hash, and schema version

Selector: a project basename substring, a session id prefix, or "all".

Security: scan/preview/dry-run make NO network calls. donate's only network
destination is the GitHub API. Transcripts are read only behind a hard allowlist.
`)
}

// ---- scan ----

func cmdScan(args []string) int {
	showAll := false
	asJSON := false
	for _, a := range args {
		switch a {
		case "--all":
			showAll = true
		case "--json":
			asJSON = true
		default:
			fmt.Fprintf(os.Stderr, "scan: unknown flag %q\n", a)
			return 2
		}
	}

	roots := discover.ConfigRoots()
	if len(roots) == 0 {
		fmt.Fprintln(os.Stderr, "scan: no Claude config roots found (looked for ~/.claude/projects, XDG, CLAUDE_CONFIG_DIR)")
		return 1
	}
	sessions, _ := discover.DiscoverSessions(roots)

	type row struct {
		Project   string `json:"project"`
		Session   string `json:"session"`
		FableMain int    `json:"fable_main"`
		FableSub  int    `json:"fable_subagents"`
		Bytes     int64  `json:"bytes"`
	}
	var rows []row
	var total int
	for _, s := range sessions {
		main := countFile(s.MainFile, s.Root)
		sub := 0
		for _, sf := range s.SubagentFiles {
			sub += countFile(sf, s.Root)
		}
		if main+sub == 0 && !showAll {
			continue
		}
		total += main + sub
		rows = append(rows, row{
			Project:   s.ProjectBasename,
			Session:   short(s.SessionID),
			FableMain: main,
			FableSub:  sub,
			Bytes:     fileSize(s.MainFile),
		})
	}

	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(map[string]any{"roots": roots, "sessions": rows, "total_fable_turns": total})
		return 0
	}

	fmt.Printf("Claude config roots:\n")
	for _, r := range roots {
		fmt.Printf("  %s\n", r)
	}
	fmt.Println()
	if len(rows) == 0 {
		fmt.Println("No sessions with genuine Fable 5 turns found. (Use --all to list every session.)")
		return 0
	}
	fmt.Printf("%-24s  %-10s  %8s  %8s  %10s\n", "PROJECT", "SESSION", "FABLE", "SUBAGENT", "SIZE")
	for _, r := range rows {
		fmt.Printf("%-24s  %-10s  %8d  %8d  %10s\n", trunc(r.Project, 24), r.Session, r.FableMain, r.FableSub, humanBytes(r.Bytes))
	}
	fmt.Printf("\n%d session(s) contain genuine Fable 5 turns; %d Fable 5 turn(s) total.\n", len(rows), total)
	return 0
}

func countFile(path, root string) int {
	f, err := discover.SafeOpen(path, root)
	if err != nil {
		return 0
	}
	defer f.Close()
	n, _ := transcript.CountFable(f)
	return n
}

func fileSize(path string) int64 {
	if fi, err := os.Stat(path); err == nil {
		return fi.Size()
	}
	return 0
}

// ---- preview ----

func cmdPreview(args []string) int {
	full := false
	asJSON := false
	var selectors []string
	for _, a := range args {
		switch {
		case a == "--full":
			full = true
		case a == "--json":
			asJSON = true
		case strings.HasPrefix(a, "--"):
			fmt.Fprintf(os.Stderr, "preview: unknown flag %q\n", a)
			return 2
		default:
			selectors = append(selectors, a)
		}
	}
	if len(selectors) == 0 {
		fmt.Fprintln(os.Stderr, "preview: at least one selector is required (project basename, session id prefix, or \"all\")")
		return 2
	}

	roots := discover.ConfigRoots()
	sessions, _ := discover.DiscoverSessions(roots)

	var encoded []string
	for _, s := range sessions {
		encoded = append(encoded, s.EncodedName)
	}
	scrubber := scrub.New(discover.Username(encoded...))
	salt := make([]byte, 32)
	_, _ = rand.Read(salt)
	builder := thread.NewBuilder(scrubber, salt, "dyc/"+version)

	matched := 0
	produced := 0
	dropped := 0
	for _, s := range sessions {
		if !matchAny(selectors, s) {
			continue
		}
		matched++
		for _, res := range builder.BuildSession(s) {
			switch res.Status {
			case "ok":
				produced++
				printRecord(s, res, full, asJSON)
			case "dropped":
				dropped++
				if !asJSON {
					fmt.Printf("[dropped] %s/%s — %s\n", s.ProjectBasename, short(s.SessionID), res.Reason)
				}
			case "parse-error":
				if !asJSON {
					fmt.Printf("[parse-error] %s/%s — %s\n", s.ProjectBasename, short(s.SessionID), res.Reason)
				}
			}
		}
	}

	if matched == 0 {
		fmt.Fprintf(os.Stderr, "preview: no sessions matched %v\n", selectors)
		return 1
	}
	if !asJSON {
		fmt.Printf("\nMatched %d session(s): %d record(s) ready, %d dropped (fail-closed).\n", matched, produced, dropped)
		if !full {
			fmt.Println("Re-run with --full to print the complete scrubbed payload.")
		}
	}
	return 0
}

func printRecord(s discover.Session, res thread.Result, full, asJSON bool) {
	r := res.Record
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(r)
		return
	}
	rs := r.RedactionSummary
	fmt.Printf("● %s/%s  record_id=%s\n", s.ProjectBasename, short(s.SessionID), r.RecordID)
	fmt.Printf("    messages=%d  models=%s  subagent=%v\n", len(r.Messages), strings.Join(r.ModelsPresent, ","), r.IsSubagent)
	fmt.Printf("    redactions: keys=%d secrets=%d high_entropy=%d emails=%d paths=%d usernames=%d ips=%d cards=%d phones=%d images=%d\n",
		rs.Keys, rs.Secrets, rs.HighEntropy, rs.Emails, rs.Paths, rs.Usernames, rs.IPs, rs.Cards, rs.Phones, rs.Images)
	if full {
		b, _ := json.MarshalIndent(r, "    ", "  ")
		fmt.Printf("    %s\n", b)
	}
}

func matchAny(selectors []string, s discover.Session) bool {
	for _, sel := range selectors {
		if matchSelector(sel, s) {
			return true
		}
	}
	return false
}

func matchSelector(sel string, s discover.Session) bool {
	if sel == "all" {
		return true
	}
	if strings.HasPrefix(s.SessionID, sel) {
		return true
	}
	return strings.Contains(strings.ToLower(s.ProjectBasename), strings.ToLower(sel))
}

// ---- helpers ----

func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%cB", float64(n)/float64(div), "KMGTPE"[exp])
}
