package transcript

import (
	"strings"
	"testing"
)

func line(t *testing.T, m map[string]any) string {
	t.Helper()
	return mustJSON(t, m)
}

func TestGenuineFablePredicate(t *testing.T) {
	fable := mustParse(t, map[string]any{
		"type":    "assistant",
		"uuid":    "u1",
		"message": map[string]any{"id": "msg_01ABC", "role": "assistant", "model": FableModel},
	})
	if !fable.IsGenuineFable() {
		t.Fatal("expected genuine fable turn")
	}

	synthetic := mustParse(t, map[string]any{
		"type":    "assistant",
		"uuid":    "u2",
		"message": map[string]any{"id": "3e694de3-5ce5-442f-a251-90f2df9992cd", "role": "assistant", "model": "<synthetic>"},
	})
	if synthetic.IsGenuineFable() {
		t.Fatal("synthetic turn must not be genuine fable")
	}
	if !synthetic.IsSynthetic() {
		t.Fatal("expected synthetic detection")
	}

	// Assistant turn from a different model.
	opus := mustParse(t, map[string]any{
		"type":    "assistant",
		"uuid":    "u3",
		"message": map[string]any{"id": "msg_01XYZ", "role": "assistant", "model": "claude-opus-4-8"},
	})
	if opus.IsGenuineFable() {
		t.Fatal("opus turn must not be genuine fable")
	}

	// User line whose text MENTIONS the model string (the false-positive case).
	userMention := mustParse(t, map[string]any{
		"type":    "user",
		"uuid":    "u4",
		"message": map[string]any{"role": "user", "content": "please use claude-fable-5 outputs only"},
	})
	if userMention.IsGenuineFable() {
		t.Fatal("user prompt mentioning the model must not be genuine fable")
	}

	// Assistant turn with a UUID-form id (fake msg id) must be rejected.
	fakeID := mustParse(t, map[string]any{
		"type":    "assistant",
		"uuid":    "u5",
		"message": map[string]any{"id": "not-a-msg-id", "role": "assistant", "model": FableModel},
	})
	if fakeID.IsGenuineFable() {
		t.Fatal("non-msg_ id must be rejected")
	}
}

func TestCountFablePrefilter(t *testing.T) {
	lines := []string{
		line(t, map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": "talk about claude-fable-5"}}),
		line(t, map[string]any{"type": "assistant", "message": map[string]any{"id": "msg_1", "role": "assistant", "model": FableModel}}),
		line(t, map[string]any{"type": "assistant", "message": map[string]any{"id": "msg_2", "role": "assistant", "model": "claude-opus-4-8"}}),
		line(t, map[string]any{"type": "assistant", "message": map[string]any{"id": "msg_3", "role": "assistant", "model": FableModel}}),
		"{ this is a malformed line",
	}
	n, err := CountFable(strings.NewReader(strings.Join(lines, "\n")))
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("expected 2 genuine fable turns, got %d", n)
	}
}

func TestParseReaderSkipsMalformed(t *testing.T) {
	in := strings.Join([]string{
		line(t, map[string]any{"type": "user", "uuid": "a", "message": map[string]any{"role": "user", "content": "hi"}}),
		"not json",
		line(t, map[string]any{"type": "assistant", "uuid": "b", "message": map[string]any{"id": "msg_1", "role": "assistant", "model": FableModel}}),
	}, "\n")
	lines, skipped, err := ParseReader(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 || skipped != 1 {
		t.Fatalf("got %d lines, %d skipped; want 2,1", len(lines), skipped)
	}
}

func TestBlocksStringVsArray(t *testing.T) {
	l := mustParse(t, map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"id": "msg_1", "role": "assistant", "model": FableModel,
			"content": []any{
				map[string]any{"type": "text", "text": "hi"},
				map[string]any{"type": "tool_use", "id": "toolu_1", "name": "Bash", "input": map[string]any{"command": "ls"}},
			},
		},
	})
	blocks, _, isString := l.Message.Blocks()
	if isString || len(blocks) != 2 {
		t.Fatalf("expected 2 array blocks, got isString=%v len=%d", isString, len(blocks))
	}
	if !ValidToolUseID(blocks[1].ID) {
		t.Fatalf("expected valid tool_use id, got %q", blocks[1].ID)
	}
}
