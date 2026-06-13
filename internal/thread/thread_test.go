package thread

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GeeveGeorge/donate-your-code/internal/discover"
	"github.com/GeeveGeorge/donate-your-code/internal/scrub"
)

// writeTranscript writes JSONL lines to a file and returns a discover.Session
// rooted at dir, with a tool-results dir for external resolution.
func writeSession(t *testing.T, lines []map[string]any) discover.Session {
	t.Helper()
	root := t.TempDir()
	projDir := filepath.Join(root, "-Users-tester-app")
	sid := "11111111-2222-3333-4444-555555555555"
	if err := os.MkdirAll(filepath.Join(projDir, sid, "tool-results"), 0o755); err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(projDir, sid+".jsonl")
	var b strings.Builder
	for _, l := range lines {
		j, err := json.Marshal(l)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(j)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(main, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return discover.Session{
		Root:            root,
		ProjectDir:      projDir,
		EncodedName:     "-Users-tester-app",
		ProjectBasename: "app",
		SessionID:       sid,
		MainFile:        main,
		ToolResultsDir:  filepath.Join(projDir, sid, "tool-results"),
	}
}

func newBuilder() *Builder {
	return NewBuilder(scrub.New([]string{"tester"}), []byte("salt-salt-salt!!"), "dyc/test")
}

func assistant(uuid, parent, id, model string, content []any, stop string) map[string]any {
	m := map[string]any{"id": id, "role": "assistant", "model": model, "content": content}
	if stop != "" {
		m["stop_reason"] = stop
	}
	l := map[string]any{"type": "assistant", "uuid": uuid, "message": m, "timestamp": uuid}
	if parent != "" {
		l["parentUuid"] = parent
	}
	return l
}

func user(uuid, parent string, content any) map[string]any {
	l := map[string]any{"type": "user", "uuid": uuid, "message": map[string]any{"role": "user", "content": content}, "timestamp": uuid}
	if parent != "" {
		l["parentUuid"] = parent
	}
	return l
}

func TestBuildFableThreadWithToolLinkage(t *testing.T) {
	lines := []map[string]any{
		user("a1", "", "what files are here?"),
		assistant("a2", "a1", "msg_01AAA", "claude-fable-5", []any{
			map[string]any{"type": "text", "text": "Let me look."},
			map[string]any{"type": "tool_use", "id": "toolu_1", "name": "Bash", "input": map[string]any{"command": "ls /Users/tester/app"}},
		}, "tool_use"),
		user("a3", "a2", []any{
			map[string]any{"type": "tool_result", "tool_use_id": "toolu_1", "content": "main.go README.md"},
		}),
		assistant("a4", "a3", "msg_01BBB", "claude-fable-5", []any{
			map[string]any{"type": "text", "text": "There are two files."},
		}, "end_turn"),
	}
	s := writeSession(t, lines)
	results := newBuilder().BuildSession(s)
	if len(results) != 1 || results[0].Status != "ok" {
		t.Fatalf("expected one ok result, got %+v", results)
	}
	rec := results[0].Record
	if rec.RecordID == "" || len(rec.RecordID) != 64 {
		t.Fatalf("bad record id %q", rec.RecordID)
	}
	if len(rec.Messages) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(rec.Messages))
	}
	// tool_use ref must match tool_result ref.
	var useRef, resRef *int
	for _, m := range rec.Messages {
		for _, b := range m.Blocks {
			if b.Type == "tool_use" {
				useRef = b.Ref
			}
			if b.Type == "tool_result" {
				resRef = b.Ref
			}
		}
	}
	if useRef == nil || resRef == nil || *useRef != *resRef {
		t.Fatalf("tool ref linkage broken: use=%v res=%v", useRef, resRef)
	}
	// The path inside the tool_use input must be scrubbed.
	blob, _ := json.Marshal(rec)
	if strings.Contains(string(blob), "/Users/tester") || strings.Contains(string(blob), "tester") {
		t.Fatalf("path/username leaked into record: %s", blob)
	}
}

func TestNoFableProducesNoRecord(t *testing.T) {
	lines := []map[string]any{
		user("a1", "", "hi"),
		assistant("a2", "a1", "msg_01CCC", "claude-opus-4-8", []any{
			map[string]any{"type": "text", "text": "hello"},
		}, "end_turn"),
	}
	s := writeSession(t, lines)
	results := newBuilder().BuildSession(s)
	if len(results) != 1 || results[0].Status != "no-fable" {
		t.Fatalf("expected no-fable, got %+v", results)
	}
}

func TestExternalPersistedResolved(t *testing.T) {
	s := writeSession(t, nil)
	// Create the external tool-result file.
	ext := filepath.Join(s.ToolResultsDir, "big.txt")
	if err := os.WriteFile(ext, []byte("secret token AKIAIOSFODNN7EXAMPLE in output"), 0o644); err != nil {
		t.Fatal(err)
	}
	persisted := "<persisted-output>\nOutput too large (1KB). Full output saved to: " + ext + "\n\nPreview (first 2KB):\n...\n</persisted-output>"
	lines := []map[string]any{
		user("a1", "", "run it"),
		assistant("a2", "a1", "msg_01DDD", "claude-fable-5", []any{
			map[string]any{"type": "tool_use", "id": "toolu_9", "name": "Bash", "input": map[string]any{"command": "run"}},
		}, "tool_use"),
		user("a3", "a2", []any{
			map[string]any{"type": "tool_result", "tool_use_id": "toolu_9", "content": persisted},
		}),
		assistant("a4", "a3", "msg_01EEE", "claude-fable-5", []any{
			map[string]any{"type": "text", "text": "done"},
		}, "end_turn"),
	}
	// rewrite main with these lines
	rewriteMain(t, s, lines)

	results := newBuilder().BuildSession(s)
	if len(results) != 1 || results[0].Status != "ok" {
		t.Fatalf("expected ok, got %+v", results)
	}
	blob, _ := json.Marshal(results[0].Record)
	if strings.Contains(string(blob), "AKIAIOSFODNN7EXAMPLE") {
		t.Fatalf("external secret leaked: %s", blob)
	}
	if !strings.Contains(string(blob), "«SECRET:aws»") {
		t.Fatalf("expected external content to be scrubbed and inlined: %s", blob)
	}
}

func TestExternalMissingFailsClosed(t *testing.T) {
	s := writeSession(t, nil)
	missing := filepath.Join(s.ToolResultsDir, "does-not-exist.txt")
	persisted := "<persisted-output>\nFull output saved to: " + missing + "\n</persisted-output>"
	lines := []map[string]any{
		user("a1", "", "run it"),
		assistant("a2", "a1", "msg_01FFF", "claude-fable-5", []any{
			map[string]any{"type": "tool_use", "id": "toolu_8", "name": "Bash", "input": map[string]any{"command": "run"}},
		}, "tool_use"),
		user("a3", "a2", []any{
			map[string]any{"type": "tool_result", "tool_use_id": "toolu_8", "content": persisted},
		}),
		assistant("a4", "a3", "msg_01FF2", "claude-fable-5", []any{
			map[string]any{"type": "text", "text": "done"},
		}, "end_turn"),
	}
	rewriteMain(t, s, lines)
	results := newBuilder().BuildSession(s)
	if len(results) != 1 || results[0].Status != "dropped" {
		t.Fatalf("expected dropped (fail-closed), got %+v", results)
	}
}

func TestExternalOutsideToolResultsRefused(t *testing.T) {
	s := writeSession(t, nil)
	// Reference a file OUTSIDE the session tool-results dir (path traversal).
	outside := filepath.Join(s.Root, "..", "etc-passwd.txt")
	persisted := "<persisted-output>\nFull output saved to: " + outside + "\n</persisted-output>"
	lines := []map[string]any{
		user("a1", "", "run it"),
		assistant("a2", "a1", "msg_01GGG", "claude-fable-5", []any{
			map[string]any{"type": "tool_use", "id": "toolu_7", "name": "Bash", "input": map[string]any{"command": "run"}},
		}, "tool_use"),
		user("a3", "a2", []any{
			map[string]any{"type": "tool_result", "tool_use_id": "toolu_7", "content": persisted},
		}),
		assistant("a4", "a3", "msg_01GG2", "claude-fable-5", []any{
			map[string]any{"type": "text", "text": "done"},
		}, "end_turn"),
	}
	rewriteMain(t, s, lines)
	results := newBuilder().BuildSession(s)
	if len(results) != 1 || results[0].Status != "dropped" {
		t.Fatalf("expected dropped for out-of-bounds external ref, got %+v", results)
	}
}

func rewriteMain(t *testing.T, s discover.Session, lines []map[string]any) {
	t.Helper()
	var b strings.Builder
	for _, l := range lines {
		j, _ := json.Marshal(l)
		b.Write(j)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(s.MainFile, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}
