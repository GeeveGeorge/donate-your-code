// Package state manages dyc's local state directory: the per-donor salt, the
// GitHub token, and the append-only log of already-donated record ids (for
// idempotent, resumable donation). State lives in $XDG_CONFIG_HOME/dyc (or
// ~/.config/dyc) and NEVER inside ~/.claude.
package state

import (
	"bufio"
	"crypto/rand"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Dir returns the state directory path (not necessarily existing yet).
func Dir() (string, error) {
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		return filepath.Join(v, "dyc"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "dyc"), nil
}

func ensureDir() (string, error) {
	d, err := Dir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(d, 0o700); err != nil {
		return "", err
	}
	return d, nil
}

// Salt loads the per-donor 32-byte salt, creating it on first use. It is used to
// derive parent_session_hash and is never donated.
func Salt() ([]byte, error) {
	d, err := ensureDir()
	if err != nil {
		return nil, err
	}
	p := filepath.Join(d, "salt")
	if b, err := os.ReadFile(p); err == nil && len(b) >= 16 {
		return b, nil
	}
	salt := make([]byte, 32)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	if err := os.WriteFile(p, salt, 0o600); err != nil {
		return nil, err
	}
	return salt, nil
}

// SaveToken writes the GitHub token to the state dir with 0600 permissions.
func SaveToken(tok string) error {
	d, err := ensureDir()
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(d, "token"), []byte(strings.TrimSpace(tok)), 0o600)
}

// DeleteToken removes any stored token.
func DeleteToken() error {
	d, err := Dir()
	if err != nil {
		return err
	}
	err = os.Remove(filepath.Join(d, "token"))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// ResolveToken returns a GitHub token and the source it came from, checking (in
// order) the stored token, then DYC_GITHUB_TOKEN, GITHUB_TOKEN, GH_TOKEN.
func ResolveToken() (token, source string) {
	if d, err := Dir(); err == nil {
		if b, err := os.ReadFile(filepath.Join(d, "token")); err == nil {
			if t := strings.TrimSpace(string(b)); t != "" {
				return t, "stored"
			}
		}
	}
	for _, env := range []string{"DYC_GITHUB_TOKEN", "GITHUB_TOKEN", "GH_TOKEN"} {
		if t := strings.TrimSpace(os.Getenv(env)); t != "" {
			return t, "env:" + env
		}
	}
	return "", ""
}

// DonateEntry records one submitted record.
type DonateEntry struct {
	RecordID string `json:"record_id"`
	PRURL    string `json:"pr_url"`
	Status   string `json:"status"` // submitted | merged
	At       string `json:"at"`
}

// LoadDonated reads the append-only donation log into a map keyed by record id.
func LoadDonated() (map[string]DonateEntry, error) {
	out := map[string]DonateEntry{}
	d, err := Dir()
	if err != nil {
		return out, err
	}
	f, err := os.Open(filepath.Join(d, "donated.jsonl"))
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return out, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<16), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e DonateEntry
		if json.Unmarshal([]byte(line), &e) == nil && e.RecordID != "" {
			out[e.RecordID] = e
		}
	}
	return out, sc.Err()
}

// AppendDonated appends one entry to the donation log.
func AppendDonated(e DonateEntry) error {
	d, err := ensureDir()
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(d, "donated.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	_, err = f.Write(append(b, '\n'))
	return err
}
