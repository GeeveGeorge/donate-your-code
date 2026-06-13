package github

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const apiBase = "https://api.github.com"

// Client is a minimal GitHub REST client over the egress-locked transport.
type Client struct {
	token string
	http  *http.Client
}

// New returns a Client. blockAll wires the --no-net audit switch.
func New(token string, blockAll bool) *Client {
	return &Client{token: token, http: NewHTTPClient(blockAll)}
}

// User is the subset of /user we use.
type User struct {
	Login string `json:"login"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

func (c *Client) do(method, path string, body any, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, apiBase+path, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "dyc")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
		if ra := resp.Header.Get("Retry-After"); ra != "" {
			return fmt.Errorf("github rate-limited (%d); retry after %ss", resp.StatusCode, ra)
		}
		if resp.Header.Get("X-RateLimit-Remaining") == "0" {
			return fmt.Errorf("github rate limit exhausted; resets at %s (epoch)", resp.Header.Get("X-RateLimit-Reset"))
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("github %s %s -> %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(data)))
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

// GetUser returns the authenticated user.
func (c *Client) GetUser() (*User, error) {
	var u User
	if err := c.do("GET", "/user", nil, &u); err != nil {
		return nil, err
	}
	return &u, nil
}

// RepoInfo is the subset of a repo we use.
type RepoInfo struct {
	FullName      string `json:"full_name"`
	DefaultBranch string `json:"default_branch"`
}

// GetRepo fetches repo metadata.
func (c *Client) GetRepo(owner, repo string) (*RepoInfo, error) {
	var r RepoInfo
	if err := c.do("GET", "/repos/"+owner+"/"+repo, nil, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// EnsureFork creates (if needed) and waits for the authenticated user's fork of
// owner/repo, returning the fork's full name.
func (c *Client) EnsureFork(owner, repo, login string) (string, error) {
	forkFull := login + "/" + repo
	if _, err := c.GetRepo(login, repo); err == nil {
		return forkFull, nil
	}
	// Trigger the fork (async).
	_ = c.do("POST", "/repos/"+owner+"/"+repo+"/forks", map[string]any{}, nil)
	for i := 0; i < 12; i++ {
		time.Sleep(time.Duration(2+i) * time.Second)
		if _, err := c.GetRepo(login, repo); err == nil {
			return forkFull, nil
		}
	}
	return "", fmt.Errorf("fork %s did not become ready in time", forkFull)
}

type refObj struct {
	Object struct {
		SHA string `json:"sha"`
	} `json:"object"`
}

// GetBranchSHA returns the commit sha at the tip of a branch.
func (c *Client) GetBranchSHA(owner, repo, branch string) (string, error) {
	var r refObj
	if err := c.do("GET", "/repos/"+owner+"/"+repo+"/git/ref/heads/"+branch, nil, &r); err != nil {
		return "", err
	}
	return r.Object.SHA, nil
}

// CreateBranch creates refs/heads/branch pointing at fromSHA.
func (c *Client) CreateBranch(owner, repo, branch, fromSHA string) error {
	return c.do("POST", "/repos/"+owner+"/"+repo+"/git/refs", map[string]any{
		"ref": "refs/heads/" + branch,
		"sha": fromSHA,
	}, nil)
}

// CreateBlob uploads file content and returns its blob sha.
func (c *Client) CreateBlob(owner, repo string, content []byte) (string, error) {
	var out struct {
		SHA string `json:"sha"`
	}
	err := c.do("POST", "/repos/"+owner+"/"+repo+"/git/blobs", map[string]any{
		"content":  base64.StdEncoding.EncodeToString(content),
		"encoding": "base64",
	}, &out)
	return out.SHA, err
}

// TreeEntry is one file in a tree.
type TreeEntry struct {
	Path string `json:"path"`
	Mode string `json:"mode"`
	Type string `json:"type"`
	SHA  string `json:"sha"`
}

// CreateTree builds a tree based on baseTree.
func (c *Client) CreateTree(owner, repo, baseTree string, entries []TreeEntry) (string, error) {
	var out struct {
		SHA string `json:"sha"`
	}
	err := c.do("POST", "/repos/"+owner+"/"+repo+"/git/trees", map[string]any{
		"base_tree": baseTree,
		"tree":      entries,
	}, &out)
	return out.SHA, err
}

// CreateCommit creates a commit object.
func (c *Client) CreateCommit(owner, repo, message, tree, parent string) (string, error) {
	var out struct {
		SHA string `json:"sha"`
	}
	err := c.do("POST", "/repos/"+owner+"/"+repo+"/git/commits", map[string]any{
		"message": message,
		"tree":    tree,
		"parents": []string{parent},
	}, &out)
	return out.SHA, err
}

// UpdateBranch moves a branch ref to sha.
func (c *Client) UpdateBranch(owner, repo, branch, sha string, force bool) error {
	return c.do("PATCH", "/repos/"+owner+"/"+repo+"/git/refs/heads/"+branch, map[string]any{
		"sha":   sha,
		"force": force,
	}, nil)
}

// BaseTreeOf returns the tree sha of a commit.
func (c *Client) BaseTreeOf(owner, repo, commitSHA string) (string, error) {
	var out struct {
		Tree struct {
			SHA string `json:"sha"`
		} `json:"tree"`
	}
	err := c.do("GET", "/repos/"+owner+"/"+repo+"/git/commits/"+commitSHA, nil, &out)
	return out.Tree.SHA, err
}

// CreatePR opens a pull request against baseOwner/baseRepo and returns its URL.
func (c *Client) CreatePR(baseOwner, baseRepo, head, base, title, body string) (string, error) {
	var out struct {
		HTMLURL string `json:"html_url"`
		Number  int    `json:"number"`
	}
	err := c.do("POST", "/repos/"+baseOwner+"/"+baseRepo+"/pulls", map[string]any{
		"title":                 title,
		"head":                  head,
		"base":                  base,
		"body":                  body,
		"maintainer_can_modify": true,
	}, &out)
	return out.HTMLURL, err
}
