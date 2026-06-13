package thread

import (
	"bytes"
	"encoding/json"
	"io"

	"github.com/GeeveGeorge/donate-your-code/internal/discover"
	"github.com/GeeveGeorge/donate-your-code/internal/extern"
	"github.com/GeeveGeorge/donate-your-code/internal/record"
	"github.com/GeeveGeorge/donate-your-code/internal/scrub"
	"github.com/GeeveGeorge/donate-your-code/internal/transcript"
)

// maxExternalBytes caps how much of an external tool-result file we will inline.
const maxExternalBytes = 8 << 20

// converter holds per-thread state while projecting transcript lines into record
// messages. It accumulates redaction counts and, on a fatal condition, sets
// dropReason so the caller fails the whole record closed.
type converter struct {
	scrubber       *scrub.Scrubber
	toolResultsDir string
	refByToolUse   map[string]int
	nextRef        int
	counts         scrub.Counts
	dropReason     string
}

func (c *converter) convertLine(l *transcript.Line) (record.Message, bool) {
	if l.Message == nil {
		return record.Message{}, false
	}
	switch l.Type {
	case "assistant":
		return c.convertAssistant(l)
	case "user":
		return c.convertUser(l)
	default:
		return record.Message{}, false
	}
}

func (c *converter) convertAssistant(l *transcript.Line) (record.Message, bool) {
	blocks, plain, isString := l.Message.Blocks()
	var rblocks []record.Block
	if isString {
		if plain != "" {
			rblocks = append(rblocks, c.textBlock("text", plain, false))
		}
	} else {
		for _, blk := range blocks {
			if rb, ok := c.convertAssistantBlock(blk); ok {
				rblocks = append(rblocks, rb)
			}
		}
	}
	if len(rblocks) == 0 {
		return record.Message{}, false
	}
	m := record.Message{Role: "assistant", Model: l.Message.Model, Blocks: rblocks}
	if l.Message.StopReason != nil {
		m.StopReason = *l.Message.StopReason
	}
	if u := l.Message.Usage; u != nil {
		m.Usage = &record.Usage{
			CacheCreationInputTokens: u.CacheCreationInputTokens,
			CacheReadInputTokens:     u.CacheReadInputTokens,
			ServiceTier:              u.ServiceTier,
		}
	}
	return m, true
}

func (c *converter) convertAssistantBlock(blk transcript.ContentBlock) (record.Block, bool) {
	switch blk.Type {
	case "text":
		return c.textBlock("text", blk.Text, false), true
	case "thinking":
		txt := blk.Thinking
		if txt == "" {
			txt = blk.Text
		}
		return c.textBlock("thinking", txt, true), true
	case "tool_use":
		ref := c.assignRef(blk.ID)
		return record.Block{
			Type:      "tool_use",
			Ref:       &ref,
			Name:      blk.Name,
			InputJSON: c.scrubInput(blk.Input),
		}, true
	default:
		if blk.Text != "" {
			return c.textBlock("fallback", blk.Text, true), true
		}
		return record.Block{}, false
	}
}

func (c *converter) convertUser(l *transcript.Line) (record.Message, bool) {
	blocks, plain, isString := l.Message.Blocks()
	if isString {
		if plain == "" {
			return record.Message{}, false
		}
		return record.Message{Role: "user", Blocks: []record.Block{c.textBlock("text", plain, false)}}, true
	}
	for _, blk := range blocks {
		if blk.Type == "tool_result" {
			return c.convertToolLine(blocks)
		}
	}
	var rblocks []record.Block
	for _, blk := range blocks {
		if blk.Type == "text" && blk.Text != "" {
			rblocks = append(rblocks, c.textBlock("text", blk.Text, false))
		}
	}
	if len(rblocks) == 0 {
		return record.Message{}, false
	}
	return record.Message{Role: "user", Blocks: rblocks}, true
}

func (c *converter) convertToolLine(blocks []transcript.ContentBlock) (record.Message, bool) {
	var rblocks []record.Block
	for _, blk := range blocks {
		if blk.Type != "tool_result" {
			continue
		}
		ref, ok := c.refByToolUse[blk.ToolUseID]
		if !ok {
			continue // orphan tool_result: no matching tool_use in this thread
		}
		sub, dropped := c.convertToolResultContent(blk.Content)
		if dropped {
			return record.Message{}, false
		}
		rref := ref
		isErr := blk.IsError
		rblocks = append(rblocks, record.Block{Type: "tool_result", Ref: &rref, IsError: &isErr, Content: sub})
	}
	if len(rblocks) == 0 {
		return record.Message{}, false
	}
	return record.Message{Role: "tool", Blocks: rblocks}, true
}

func (c *converter) convertToolResultContent(raw json.RawMessage) (out []record.Block, dropped bool) {
	blocks, plain, isString := transcript.SubBlocks(raw)
	if isString {
		if path, ok := extern.ParsePersisted(plain); ok {
			content, ok2 := c.readExternal(path)
			if !ok2 {
				c.dropReason = "external tool-result unresolved"
				return nil, true
			}
			return []record.Block{c.textBlock("text", content, true)}, false
		}
		return []record.Block{c.textBlock("text", plain, true)}, false
	}
	for _, sb := range blocks {
		switch sb.Type {
		case "text":
			out = append(out, c.textBlock("text", sb.Text, true))
		case "image":
			c.counts.Images++
			out = append(out, record.Block{Type: "image", Image: scrub.HashImage(sb.Source)})
		default:
			if sb.Text != "" {
				out = append(out, c.textBlock("fallback", sb.Text, true))
			}
		}
	}
	return out, false
}

// readExternal opens a persisted-output file ONLY if its real path is contained
// within this session's tool-results directory. Missing, oversized, or
// out-of-bounds files fail closed.
func (c *converter) readExternal(path string) (string, bool) {
	f, err := discover.SafeOpen(path, c.toolResultsDir)
	if err != nil {
		return "", false
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxExternalBytes+1))
	if err != nil || len(data) > maxExternalBytes {
		return "", false
	}
	return string(data), true
}

func (c *converter) textBlock(typ, text string, highRisk bool) record.Block {
	t, cc := c.scrubber.Scrub(text, highRisk)
	c.counts.Add(cc)
	return record.Block{Type: typ, Text: t}
}

func (c *converter) assignRef(toolUseID string) int {
	if r, ok := c.refByToolUse[toolUseID]; ok {
		return r
	}
	r := c.nextRef
	c.nextRef++
	if toolUseID != "" {
		c.refByToolUse[toolUseID] = r
	}
	return r
}

func (c *converter) scrubInput(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return c.scrubInputFallback(raw)
	}
	sv, cc := c.scrubber.ScrubTree(v, true)
	c.counts.Add(cc)
	canon, err := record.Canonicalize(sv)
	if err != nil {
		return c.scrubInputFallback(raw)
	}
	return string(canon)
}

func (c *converter) scrubInputFallback(raw json.RawMessage) string {
	t, cc := c.scrubber.Scrub(string(raw), true)
	c.counts.Add(cc)
	canon, _ := record.Canonicalize(t)
	return string(canon)
}
