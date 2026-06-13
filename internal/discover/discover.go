// Package discover resolves Claude Code config roots and enumerates session
// transcript files behind a hard allowlist. It is the first half of the trust
// boundary (P1/P6): it opens ONLY files under projects/**/*.jsonl and subagent
// transcripts, refuses symlinks and path escapes, and never reads arbitrary
// paths.
package discover

import (
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strings"
)

// Errors returned by SafeOpen.
var (
	ErrNotAbsolute = errors.New("path is not absolute")
	ErrOutsideRoot = errors.New("path escapes the allowed root")
	ErrSymlink     = errors.New("path leaf is a symlink")
	ErrNotRegular  = errors.New("path is not a regular file")
)

// ConfigRoots returns the resolved, existing, de-duplicated set of Claude Code
// `projects` directories to scan, in priority order. Each returned path is the
// real (symlink-resolved) absolute path and is used as an allowlist root.
func ConfigRoots() []string {
	var candidates []string
	if v := os.Getenv("CLAUDE_CONFIG_DIR"); v != "" {
		for _, part := range filepath.SplitList(v) {
			candidates = append(candidates, filepath.Join(part, "projects"))
		}
	}
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		candidates = append(candidates, filepath.Join(v, "claude", "projects"))
	} else if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, ".config", "claude", "projects"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, ".claude", "projects"))
	}
	// Windows %USERPROFILE%\.claude\projects is covered by UserHomeDir on Windows.

	seen := map[string]bool{}
	var roots []string
	for _, c := range candidates {
		fi, err := os.Stat(c)
		if err != nil || !fi.IsDir() {
			continue
		}
		real, err := filepath.EvalSymlinks(c)
		if err != nil {
			continue
		}
		if !seen[real] {
			seen[real] = true
			roots = append(roots, real)
		}
	}
	return roots
}

// Username returns the set of OS usernames worth redacting: the current OS user
// plus any name parsed from an encoded project directory of the form
// "-Users-<name>-..." or "-home-<name>-...".
func Username(encodedProjectDirs ...string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(n string) {
		if len(n) >= 3 && !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	if u, err := user.Current(); err == nil {
		add(u.Username)
		// On some systems Username is DOMAIN\user; keep the last segment too.
		if i := strings.LastIndexAny(u.Username, `\/`); i >= 0 {
			add(u.Username[i+1:])
		}
	}
	for _, enc := range encodedProjectDirs {
		parts := strings.Split(strings.TrimPrefix(enc, "-"), "-")
		for i := 0; i+1 < len(parts); i++ {
			if (parts[i] == "Users" || parts[i] == "home") && parts[i+1] != "" {
				add(parts[i+1])
			}
		}
	}
	return out
}

// Session is one session bundle: a top-level transcript plus any subagent
// transcripts that belong to it.
type Session struct {
	Root            string   // the allowlist root this session lives under
	ProjectDir      string   // absolute path of the encoded project directory
	EncodedName     string   // the encoded project dir name, e.g. -Users-geeve-app
	ProjectBasename string   // display-only basename, e.g. app
	SessionID       string   // transcript filename stem (a UUID)
	MainFile        string   // absolute path of <sessionID>.jsonl
	SubagentFiles   []string // absolute paths of <sessionID>/subagents/*.jsonl
	ToolResultsDir  string   // absolute path of <sessionID>/tool-results (may not exist)
}

// DiscoverSessions enumerates every session bundle under the given roots.
func DiscoverSessions(roots []string) ([]Session, error) {
	var sessions []Session
	for _, root := range roots {
		projectDirs, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, pd := range projectDirs {
			if !pd.IsDir() {
				continue
			}
			projDir := filepath.Join(root, pd.Name())
			entries, err := os.ReadDir(projDir)
			if err != nil {
				continue
			}
			for _, e := range entries {
				if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
					continue
				}
				sid := strings.TrimSuffix(e.Name(), ".jsonl")
				s := Session{
					Root:            root,
					ProjectDir:      projDir,
					EncodedName:     pd.Name(),
					ProjectBasename: basenameFromEncoded(pd.Name()),
					SessionID:       sid,
					MainFile:        filepath.Join(projDir, e.Name()),
					ToolResultsDir:  filepath.Join(projDir, sid, "tool-results"),
				}
				subDir := filepath.Join(projDir, sid, "subagents")
				if subs, err := os.ReadDir(subDir); err == nil {
					for _, sf := range subs {
						if !sf.IsDir() && strings.HasSuffix(sf.Name(), ".jsonl") {
							s.SubagentFiles = append(s.SubagentFiles, filepath.Join(subDir, sf.Name()))
						}
					}
					sort.Strings(s.SubagentFiles)
				}
				sessions = append(sessions, s)
			}
		}
	}
	sort.Slice(sessions, func(i, j int) bool {
		if sessions[i].EncodedName != sessions[j].EncodedName {
			return sessions[i].EncodedName < sessions[j].EncodedName
		}
		return sessions[i].SessionID < sessions[j].SessionID
	})
	return sessions, nil
}

func basenameFromEncoded(enc string) string {
	enc = strings.TrimPrefix(enc, "-")
	if i := strings.LastIndex(enc, "-"); i >= 0 {
		return enc[i+1:]
	}
	if enc == "" {
		return "(root)"
	}
	return enc
}

// SafeOpen opens a file for reading only if its real (symlink-resolved) path is
// contained within allowedRoot and it is a regular, non-symlink file. This is the
// containment guard that prevents path traversal and symlink/hardlink redirection
// out of the allowlist.
func SafeOpen(path, allowedRoot string) (*os.File, error) {
	clean := filepath.Clean(path)
	if !filepath.IsAbs(clean) {
		return nil, ErrNotAbsolute
	}
	rootReal, err := filepath.EvalSymlinks(allowedRoot)
	if err != nil {
		return nil, err
	}
	// Reject if the leaf itself is a symlink.
	if lfi, err := os.Lstat(clean); err == nil && lfi.Mode()&os.ModeSymlink != 0 {
		return nil, ErrSymlink
	}
	real, err := filepath.EvalSymlinks(clean)
	if err != nil {
		return nil, err
	}
	if !within(rootReal, real) {
		return nil, ErrOutsideRoot
	}
	fi, err := os.Lstat(real)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, ErrNotRegular
	}
	return os.Open(real)
}

// within reports whether p is root or strictly contained within root.
func within(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
