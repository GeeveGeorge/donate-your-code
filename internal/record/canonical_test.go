package record

import (
	"encoding/json"
	"testing"
)

// wantU0001 is the byte sequence  ""  (a JSON string containing the JCS
// escape for U+0001), built from explicit bytes to avoid editor escape mangling.
var wantU0001 = string([]byte{0x22, 0x5c, 0x75, 0x30, 0x30, 0x30, 0x31, 0x22})

// These are cross-language golden vectors. The Python server MUST produce byte-
// identical canonical output for the same inputs, or content-addressing breaks.
func TestCanonicalizeGolden(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want string
	}{
		{"sorted keys", map[string]any{"b": 1, "a": 2}, `{"a":2,"b":1}`},
		{"nested array", map[string]any{"x": []any{true, false, nil, int64(1)}}, `{"x":[true,false,null,1]}`},
		{"string escapes", "a\"\\\n\t", `"a\"\\\n\t"`},
		{"control char", string(rune(1)), wantU0001},
		{"utf8 literal", "é→λ", `"é→λ"`},
		{"empty obj/arr", map[string]any{"o": map[string]any{}, "a": []any{}}, `{"a":[],"o":{}}`},
		{"json.Number", map[string]any{"n": json.Number("1.50")}, `{"n":1.50}`},
		{"key order deep", map[string]any{"z": int64(1), "a": map[string]any{"y": int64(2), "b": int64(3)}}, `{"a":{"b":3,"y":2},"z":1}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Canonicalize(c.in)
			if err != nil {
				t.Fatalf("Canonicalize error: %v", err)
			}
			if string(got) != c.want {
				t.Fatalf("got %q want %q", got, c.want)
			}
		})
	}
}

func TestCanonicalizeRejectsFloat(t *testing.T) {
	if _, err := Canonicalize(map[string]any{"x": 1.5}); err == nil {
		t.Fatal("expected float64 to be rejected (use json.Number)")
	}
}

func TestHashDeterministicAndOrderIndependent(t *testing.T) {
	a := map[string]any{"b": int64(1), "a": int64(2), "c": []any{"x", "y"}}
	b := map[string]any{"c": []any{"x", "y"}, "a": int64(2), "b": int64(1)}
	ha, _, err := Hash(a)
	if err != nil {
		t.Fatal(err)
	}
	hb, _, err := Hash(b)
	if err != nil {
		t.Fatal(err)
	}
	if ha != hb {
		t.Fatalf("hash not order-independent: %s != %s", ha, hb)
	}
	if len(ha) != 64 {
		t.Fatalf("expected 64-hex sha256, got %d chars", len(ha))
	}
}

func TestRecordIDStable(t *testing.T) {
	mk := func() *Record {
		return &Record{
			SchemaVersion: SchemaVersion,
			Model:         FableModel,
			Messages: []Message{
				{Role: "user", Blocks: []Block{{Type: "text", Text: "hi"}}},
				{Role: "assistant", Model: FableModel, StopReason: "end_turn",
					Usage:  &Usage{CacheReadInputTokens: 10, ServiceTier: "standard"},
					Blocks: []Block{{Type: "text", Text: "hello"}}},
			},
		}
	}
	r1, r2 := mk(), mk()
	id1, err := r1.ComputeID()
	if err != nil {
		t.Fatal(err)
	}
	id2, err := r2.ComputeID()
	if err != nil {
		t.Fatal(err)
	}
	if id1 != id2 {
		t.Fatalf("record id not stable: %s != %s", id1, id2)
	}
	// Metadata fields must NOT change the id (content-addressing / corroboration).
	r3 := mk()
	r3.Contributor = "alice"
	r3.IsSubagent = true
	r3.ParentSessionHash = "deadbeef"
	r3.ClaudeCodeVersion = "2.1.159"
	id3, err := r3.ComputeID()
	if err != nil {
		t.Fatal(err)
	}
	if id3 != id1 {
		t.Fatalf("metadata leaked into record id: %s != %s", id3, id1)
	}
}
