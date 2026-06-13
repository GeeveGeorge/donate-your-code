package scrub

import (
	"strings"
	"testing"
)

const rsaKey = "-----BEGIN RSA PRIVATE KEY-----\nMIIabc123def456\n-----END RSA PRIVATE KEY-----"

// TestRedTeamZeroLeak is the core safety test: known secrets/PII must never
// survive scrubbing, and the placeholder must appear instead.
func TestRedTeamZeroLeak(t *testing.T) {
	s := New([]string{"alice"})
	cases := []struct {
		name    string
		in      string
		leakage []string // substrings that must NOT remain
		want    string   // a placeholder that MUST appear
	}{
		{"aws", "key=AKIAIOSFODNN7EXAMPLE end", []string{"AKIAIOSFODNN7EXAMPLE"}, "«SECRET:aws»"},
		{"github", "tok ghp_0123456789abcdefghijklmnopqrstuvwxyz done", []string{"ghp_0123456789"}, "«SECRET:github»"},
		{"anthropic", "sk-ant-api03-AAAAAAAAAAAAAAAAAAAAAAAA x", []string{"sk-ant-api03"}, "«SECRET:anthropic»"},
		{"email", "mail me at bob.smith@example.com please", []string{"bob.smith@example.com"}, "«EMAIL»"},
		{"home unix", "see /Users/alice/secrets/db.txt now", []string{"/Users/alice", "alice"}, "«HOME»"},
		{"home other user", "ls /home/charlie/.ssh/id_rsa", []string{"/home/charlie", "charlie"}, "«HOME»"},
		{"private key", "config " + rsaKey, []string{"BEGIN RSA PRIVATE KEY", "MIIabc123def456"}, "«PRIVATE_KEY»"},
		{"card", "pay 4111 1111 1111 1111 today", []string{"4111 1111 1111 1111"}, "«CARD»"},
		{"ipv4", "host 192.168.10.24 up", []string{"192.168.10.24"}, "«IP»"},
		{"username", "hello alice how are you", []string{"alice"}, "«USER»"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, _ := s.Scrub(c.in, true)
			for _, leak := range c.leakage {
				if strings.Contains(out, leak) {
					t.Fatalf("LEAK: %q still present in %q", leak, out)
				}
			}
			if !strings.Contains(out, c.want) {
				t.Fatalf("expected placeholder %q in %q", c.want, out)
			}
		})
	}
}

func TestCardLuhnAvoidsFalsePositive(t *testing.T) {
	s := New(nil)
	// A 16-digit run that fails Luhn must be left alone (it's not a card).
	out, _ := s.Scrub("id 1234567890123456 here", false)
	if strings.Contains(out, "«CARD»") {
		t.Fatalf("non-Luhn digit run wrongly redacted: %q", out)
	}
}

func TestHighEntropyRedaction(t *testing.T) {
	s := New(nil)
	// base64-ish secret blob with digits/symbols and high entropy.
	out, _ := s.Scrub("token=Zk7Q1xP9bL3wR8sT2vY6nM0aJ4cD5eF7 end", true)
	if !strings.Contains(out, "«HIGH_ENTROPY»") {
		t.Fatalf("expected high-entropy redaction in %q", out)
	}
	// An ordinary long English word (no digits/symbols) must be left alone.
	out2, _ := s.Scrub("internationalization is a long word", true)
	if strings.Contains(out2, "«HIGH_ENTROPY»") {
		t.Fatalf("English word wrongly redacted: %q", out2)
	}
}

func TestTripwireFires(t *testing.T) {
	s := New([]string{"alice"})
	if hits := s.Tripwire("totally clean text with no secrets"); len(hits) != 0 {
		t.Fatalf("tripwire false positive: %v", hits)
	}
	for _, leak := range []string{
		"residual /Users/bob/x",
		"-----BEGIN RSA PRIVATE KEY-----",
		"AKIAIOSFODNN7EXAMPLE",
		"contact me at a@b.co",
		"hello alice",
	} {
		if hits := s.Tripwire(leak); len(hits) == 0 {
			t.Fatalf("tripwire missed leak: %q", leak)
		}
	}
}

func TestScrubDeterministic(t *testing.T) {
	s := New([]string{"alice"})
	in := "alice at /Users/alice/x mailed bob@x.com key AKIAIOSFODNN7EXAMPLE"
	a, _ := s.Scrub(in, true)
	b, _ := s.Scrub(in, true)
	if a != b {
		t.Fatalf("scrub not deterministic:\n%q\n%q", a, b)
	}
}

func TestScrubTree(t *testing.T) {
	s := New(nil)
	tree := map[string]any{
		"command": "cat /home/dave/.env",
		"nested":  []any{"AKIAIOSFODNN7EXAMPLE", map[string]any{"k": "x@y.com"}},
	}
	out, c := s.ScrubTree(tree, true)
	m := out.(map[string]any)
	if strings.Contains(m["command"].(string), "/home/dave") {
		t.Fatalf("path leaked in tree: %v", m["command"])
	}
	if c.Secrets == 0 || c.Emails == 0 {
		t.Fatalf("expected secret+email counts, got %+v", c)
	}
}
