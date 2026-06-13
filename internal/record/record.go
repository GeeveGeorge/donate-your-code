// Package record defines the canonical donation record and the content-addressed
// record_id. The id is a SHA-256 over a precisely-specified subset of the record
// (the "preimage"), canonicalized with RFC 8785. The preimage shape is the
// security-critical interface shared byte-for-byte with the Python server; see
// schema/canonicalization.md.
package record

const (
	SchemaVersion          = "dyc.record.v1"
	ProvenanceSelfAttested = "self-attested"
	LicenseCC0             = "CC0-1.0"
	FableModel             = "claude-fable-5"
)

// Block is one content block in a message.
type Block struct {
	Type      string  `json:"type"`
	Text      string  `json:"text,omitempty"`       // text, thinking, fallback
	Ref       *int    `json:"ref,omitempty"`        // tool_use, tool_result linkage
	Name      string  `json:"name,omitempty"`       // tool_use
	InputJSON string  `json:"input_json,omitempty"` // tool_use, canonical JSON string
	IsError   *bool   `json:"is_error,omitempty"`   // tool_result
	Truncated *bool   `json:"truncated,omitempty"`  // tool_result (external file missing)
	Content   []Block `json:"content,omitempty"`    // tool_result sub-blocks
	Image     string  `json:"image,omitempty"`      // image placeholder
}

// Usage carries only reliable token-accounting fields.
type Usage struct {
	CacheCreationInputTokens int64  `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64  `json:"cache_read_input_tokens"`
	ServiceTier              string `json:"service_tier,omitempty"`
}

// Message is one turn in the donated thread.
type Message struct {
	Role       string  `json:"role"` // user | assistant | tool
	Model      string  `json:"model,omitempty"`
	StopReason string  `json:"stop_reason,omitempty"`
	Usage      *Usage  `json:"usage,omitempty"`
	Blocks     []Block `json:"blocks"`
}

// RedactionSummary is shown in the preview; it is metadata, not part of the id.
type RedactionSummary struct {
	Keys        int `json:"keys"`
	Secrets     int `json:"secrets"`
	HighEntropy int `json:"high_entropy"`
	Emails      int `json:"emails"`
	Phones      int `json:"phones"`
	Cards       int `json:"cards"`
	IPs         int `json:"ips"`
	MACs        int `json:"macs"`
	Paths       int `json:"paths"`
	Usernames   int `json:"usernames"`
	Images      int `json:"images"`
}

// Record is the full donated unit written to disk and submitted by PR.
type Record struct {
	SchemaVersion     string           `json:"schema_version"`
	RecordID          string           `json:"record_id"`
	Model             string           `json:"model"`
	Provenance        string           `json:"provenance"`
	Contributor       string           `json:"contributor,omitempty"`
	DCO               bool             `json:"dco"`
	License           string           `json:"license"`
	ClientVersion     string           `json:"client_version"`
	ScrubberVersion   string           `json:"scrubber_version"`
	ClaudeCodeVersion string           `json:"claude_code_version,omitempty"`
	IsSubagent        bool             `json:"is_subagent"`
	ParentSessionHash string           `json:"parent_session_hash,omitempty"`
	ModelsPresent     []string         `json:"models_present"`
	Messages          []Message        `json:"messages"`
	RedactionSummary  RedactionSummary `json:"redaction_summary"`
}

// preimage builds the fixed-shape value tree that the record_id hashes. The id is
// CONTENT-ONLY so the same conversation from two donors yields the same id (exact
// dedup + cross-contributor corroboration). Excluded as non-content / metadata:
// contributor, client/scrubber/claude-code versions, license, provenance, dco,
// redaction summary, models_present, is_subagent, and parent_session_hash.
func (r *Record) preimage() map[string]any {
	msgs := make([]any, 0, len(r.Messages))
	for _, m := range r.Messages {
		u := m.Usage
		if u == nil {
			u = &Usage{}
		}
		msgs = append(msgs, map[string]any{
			"role":        m.Role,
			"model":       m.Model,
			"stop_reason": m.StopReason,
			"usage": map[string]any{
				"cache_creation_input_tokens": u.CacheCreationInputTokens,
				"cache_read_input_tokens":     u.CacheReadInputTokens,
				"service_tier":                u.ServiceTier,
			},
			"blocks": blocksPreimage(m.Blocks),
		})
	}
	return map[string]any{
		"schema_version": r.SchemaVersion,
		"model":          r.Model,
		"messages":       msgs,
	}
}

func blocksPreimage(blocks []Block) []any {
	out := make([]any, 0, len(blocks))
	for _, b := range blocks {
		out = append(out, blockPreimage(b))
	}
	return out
}

func blockPreimage(b Block) map[string]any {
	switch b.Type {
	case "tool_use":
		return map[string]any{
			"type":       "tool_use",
			"ref":        refVal(b.Ref),
			"name":       b.Name,
			"input_json": b.InputJSON,
		}
	case "tool_result":
		return map[string]any{
			"type":      "tool_result",
			"ref":       refVal(b.Ref),
			"is_error":  boolVal(b.IsError),
			"truncated": boolVal(b.Truncated),
			"content":   blocksPreimage(b.Content),
		}
	case "image":
		return map[string]any{
			"type":  "image",
			"image": b.Image,
		}
	default: // text, thinking, fallback
		return map[string]any{
			"type": b.Type,
			"text": b.Text,
		}
	}
}

func refVal(p *int) int {
	if p == nil {
		return -1
	}
	return *p
}

func boolVal(p *bool) bool { return p != nil && *p }

// ComputeID canonicalizes the preimage, sets RecordID, and returns it.
func (r *Record) ComputeID() (string, error) {
	id, _, err := Hash(r.preimage())
	if err != nil {
		return "", err
	}
	r.RecordID = id
	return id, nil
}
