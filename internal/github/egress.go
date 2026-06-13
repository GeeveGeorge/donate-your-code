// Package github is a minimal, egress-locked GitHub REST client used only by
// `dyc donate`. It depends on the standard library only. Its HTTP transport
// rejects every destination except the GitHub API hosts, so the client cannot be
// repurposed to exfiltrate data anywhere else (invariant I1).
package github

import (
	"context"
	"net"
	"net/http"
	"time"
)

// AllowedHosts is the complete egress allowlist.
var AllowedHosts = map[string]bool{
	"api.github.com":     true,
	"uploads.github.com": true,
}

// EgressError is returned when a dial targets a non-allowlisted host.
type EgressError struct{ Host string }

func (e EgressError) Error() string { return "egress blocked (not a GitHub API host): " + e.Host }

// NewHTTPClient returns an http.Client whose transport hard-rejects any host
// outside AllowedHosts. When blockAll is true (the --no-net audit switch), every
// dial is rejected so an auditor can prove no network activity.
func NewHTTPClient(blockAll bool) *http.Client {
	base := &net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}
	tr := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, _, err := net.SplitHostPort(addr)
			if err != nil {
				host = addr
			}
			if blockAll {
				return nil, EgressError{Host: host}
			}
			if !AllowedHosts[host] {
				return nil, EgressError{Host: host}
			}
			return base.DialContext(ctx, network, addr)
		},
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		MaxIdleConns:          8,
	}
	return &http.Client{Transport: tr, Timeout: 90 * time.Second}
}
