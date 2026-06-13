# Audit guide

This binary is meant to be easy to audit. Here is everything it can read and where
it can send data.

## Files it reads (hard allowlist — `internal/discover`)

Only, under each resolved Claude config root (`$CLAUDE_CONFIG_DIR/projects`,
`$XDG_CONFIG_HOME/claude/projects`, `~/.claude/projects`):

- `projects/<encoded>/<session>.jsonl` — top-level transcripts
- `projects/<encoded>/<session>/subagents/*.jsonl` — subagent transcripts
- `projects/<encoded>/<session>/tool-results/*.txt` — ONLY when referenced by a
  `<persisted-output>` wrapper, and ONLY if the real path resolves inside that
  session's `tool-results/` directory.

It also reads/writes its own state under `$XDG_CONFIG_HOME/dyc` (never inside
`~/.claude`). It refuses symlinked leaves and any path whose real location escapes
the allowed root (`internal/discover/discover.go`, `SafeOpen`).

## Network egress

- `scan`, `preview`, `version`: **none.**
- `donate` (v1+): the GitHub API only. The HTTP transport will be locked to
  `api.github.com` / `uploads.github.com`.

## Dependencies

The client uses the Go **standard library only** in v0 (no third-party modules).
Verify with `go list -deps ./cmd/dyc | grep -v '^github.com/GeeveGeorge/donate-your-code'` —
all results should be `internal`/stdlib packages. Any future dependency must be
listed and justified here.

## Reproducing & verifying a release

```sh
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=<v>" -o dyc ./cmd/dyc
sha256sum dyc                      # compare to the published CHECKSUMS
cosign verify-blob ...             # verify the Sigstore signature
./dyc version                      # prints version + scrubber ruleset hash
```

## What leaves your machine

Run `dyc preview <selector> --full` to see the exact bytes that `donate` would
submit. The output is deterministic: the same transcripts always produce the same
record and the same `record_id`.
