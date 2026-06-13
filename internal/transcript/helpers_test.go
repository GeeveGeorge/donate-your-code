package transcript

import (
	"encoding/json"
	"testing"
)

func mustJSON(t *testing.T, m map[string]any) string {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

func mustParse(t *testing.T, m map[string]any) *Line {
	t.Helper()
	var l Line
	if err := json.Unmarshal([]byte(mustJSON(t, m)), &l); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return &l
}
