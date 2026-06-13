// Package extern resolves the <persisted-output> wrapper that Claude Code writes
// when a tool result is too large to inline. The referenced file is the highest-
// risk read surface, so resolution is strict: the path must be absolute and the
// resolved real path must stay inside the session's own tool-results directory
// (enforced by the caller via discover.SafeOpen).
package extern

import (
	"regexp"
	"strings"
)

var savedPathRe = regexp.MustCompile(`Full output saved to:\s*(\S+\.txt)`)

// ParsePersisted extracts the external file path referenced by a persisted-output
// wrapper. ok is false when s is not a persisted-output wrapper or has no path.
func ParsePersisted(s string) (path string, ok bool) {
	if !strings.Contains(s, "<persisted-output>") {
		return "", false
	}
	m := savedPathRe.FindStringSubmatch(s)
	if m == nil {
		return "", false
	}
	return m[1], true
}
