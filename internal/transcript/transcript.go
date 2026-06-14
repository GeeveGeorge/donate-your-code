// Package transcript parses Claude Code JSONL transcript lines and identifies
// genuine Fable 5 assistant turns. It treats all transcript content as untrusted
// data: nothing here interprets content as instructions.
package transcript

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"regexp"
	"strings"
)

// FableModel is the exact, empirically-confirmed model string for Claude Fable 5
// as it appears in real transcripts (no date suffix).
const FableModel = "claude-fable-5"

// SyntheticModel marks harness-generated assistant turns that are NOT model output.
const SyntheticModel = "<synthetic>"

var (
	// msgIDRe matches genuine Anthropic API message ids. Synthetic turns use a
	// UUID-form id and are rejected by this pattern.
	msgIDRe = regexp.MustCompile(`^msg_[A-Za-z0-9]+$`)
	// toolUseIDRe matches genuine tool_use ids.
	toolUseIDRe = regexp.MustCompile(`^toolu_[A-Za-z0-9]+$`)
)

// fableNeedle is the fast substring pre-filter. A line that does not contain it
// cannot be a genuine Fable 5 assistant turn, so we skip JSON parsing entirely.
// NOTE: the string also appears inside ordinary user prompt text, so a hit is a
// GATE (parse this line), never a SELECTOR (this line is Fable 5).
var fableNeedle = []byte(FableModel)

// Line is the subset of a transcript line we care about. Unknown fields are
// ignored (schema drift tolerance).
type Line struct {
	Type       string   `json:"type"`
	UUID       string   `json:"uuid"`
	ParentUUID *string  `json:"parentUuid"`
	Timestamp  string   `json:"timestamp"`
	SessionID  string   `json:"sessionId"`
	Version    string   `json:"version"`
	Message    *Message `json:"message"`
}

// Message is the per-line message payload for user/assistant lines.
type Message struct {
	ID         string          `json:"id"`
	Role       string          `json:"role"`
	Model      string          `json:"model"`
	Content    json.RawMessage `json:"content"`
	StopReason *string         `json:"stop_reason"`
	Usage      *Usage          `json:"usage"`
}

// Usage carries only the fields that are reliable in JSONL transcripts. The
// streaming input_tokens/output_tokens placeholders are deliberately not parsed.
type Usage struct {
	CacheCreationInputTokens int64  `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64  `json:"cache_read_input_tokens"`
	ServiceTier              string `json:"service_tier"`
}

// ContentBlock is one element of a message content array.
type ContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
	Source    json.RawMessage `json:"source"`
}

// IsGenuineFable reports whether this line is a genuine Fable 5 assistant turn:
// an assistant line whose model is exactly claude-fable-5 and whose message id is
// a real API id. This defeats both the prompt-text false positive and synthetic
// turns.
func (l *Line) IsGenuineFable() bool {
	return l.Type == "assistant" &&
		l.Message != nil &&
		l.Message.Model == FableModel &&
		msgIDRe.MatchString(l.Message.ID)
}

// IsSynthetic reports whether an assistant line is a harness-generated synthetic
// turn (model "<synthetic>" or a non-msg_ id).
func (l *Line) IsSynthetic() bool {
	if l.Type != "assistant" || l.Message == nil {
		return false
	}
	return l.Message.Model == SyntheticModel || !msgIDRe.MatchString(l.Message.ID)
}

// ValidToolUseID reports whether s is a genuine tool_use id.
func ValidToolUseID(s string) bool { return toolUseIDRe.MatchString(s) }

// Blocks decodes a message content value, which may be a plain string or an
// array of content blocks.
func (m *Message) Blocks() (blocks []ContentBlock, plain string, isString bool) {
	if m == nil || len(m.Content) == 0 {
		return nil, "", false
	}
	var s string
	if json.Unmarshal(m.Content, &s) == nil {
		return nil, s, true
	}
	var bs []ContentBlock
	if json.Unmarshal(m.Content, &bs) == nil {
		return bs, "", false
	}
	return nil, "", false
}

// SubBlocks decodes a tool_result block's content, which may be a string or an
// array of {type:text|image|...} objects.
func SubBlocks(raw json.RawMessage) (blocks []ContentBlock, plain string, isString bool) {
	if len(raw) == 0 {
		return nil, "", false
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return nil, s, true
	}
	var bs []ContentBlock
	if json.Unmarshal(raw, &bs) == nil {
		return bs, "", false
	}
	return nil, "", false
}

// ParseReader parses every line of a transcript into Line values. Malformed lines
// are skipped (and counted). It uses an unbounded line reader so pathological
// long lines never silently truncate.
func ParseReader(r io.Reader) (lines []*Line, skipped int, err error) {
	br := bufio.NewReaderSize(r, 1<<20)
	for {
		chunk, rerr := br.ReadString('\n')
		if t := strings.TrimRight(chunk, "\r\n"); t != "" {
			var l Line
			if json.Unmarshal([]byte(t), &l) == nil {
				lines = append(lines, &l)
			} else {
				skipped++
			}
		}
		if rerr != nil {
			if rerr == io.EOF {
				return lines, skipped, nil
			}
			return lines, skipped, rerr
		}
	}
}

// FirstCWD returns the first non-empty top-level `cwd` value in a transcript.
// This is the real absolute project path (the encoded folder name is lossy when
// path segments contain '-', e.g. EDA-DB). It is used only for LOCAL display and
// selection; cwd is never donated.
func FirstCWD(r io.Reader) string {
	br := bufio.NewReaderSize(r, 1<<20)
	for i := 0; i < 200; i++ {
		chunk, err := br.ReadString('\n')
		if t := strings.TrimSpace(chunk); t != "" {
			var l struct {
				CWD string `json:"cwd"`
			}
			if json.Unmarshal([]byte(t), &l) == nil && l.CWD != "" {
				return l.CWD
			}
		}
		if err != nil {
			break
		}
	}
	return ""
}

// CountFable counts genuine Fable 5 assistant turns in a transcript using the
// fast substring pre-filter: lines without the needle are never JSON-parsed.
func CountFable(r io.Reader) (count int, err error) {
	br := bufio.NewReaderSize(r, 1<<20)
	for {
		chunk, rerr := br.ReadString('\n')
		if b := bytes.TrimRight([]byte(chunk), "\r\n"); len(b) > 0 && bytes.Contains(b, fableNeedle) {
			var l Line
			if json.Unmarshal(b, &l) == nil && l.IsGenuineFable() {
				count++
			}
		}
		if rerr != nil {
			if rerr == io.EOF {
				return count, nil
			}
			return count, rerr
		}
	}
}
