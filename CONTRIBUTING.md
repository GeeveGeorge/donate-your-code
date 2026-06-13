# Contributing

## Consent & the DCO

Donations are opt-in per session. Before anything leaves your machine, `dyc`
shows you the exact post-scrub payload. By contributing you sign off with the
[Developer Certificate of Origin](https://developercertificate.org/) (`git commit
-s`), certifying that:

1. you have the right to donate the content, and
2. you dedicate it to the public domain under **CC0-1.0**.

> **CC0/DCO note.** The DCO text refers to an "open source license". For this
> project, signing off is understood to certify your **CC0 public-domain
> dedication** of the contributed content. (CC0 is a waiver, not strictly a
> license; this clarification makes the intent explicit.)

## Privacy & erasure

Transcripts can contain third-party PII. `dyc` scrubs client-side and the CI gate
re-scrubs authoritatively, but scrubbing is best-effort. To request removal, open
a removal request keyed by `record_id` (or your pseudonymous contributor id);
removal tombstones the record and blocks re-ingestion. Note that CC0/public copies
already downloaded cannot be recalled.

## Code contributions

- Run `gofmt -w .`, `go vet ./...`, and `go test ./...` before opening a PR.
- The `schema/canonicalization.md` spec and `schema/record.schema.json` are
  CODEOWNERS-gated: changing them is a schema change and must bump
  `schema_version` and update golden vectors on both the Go and Python sides.
- Keep the client dependency-light: new dependencies must be justified in
  `AUDIT.md`.

## Data contributions

Use `dyc donate` (v1+). Contribution PRs to the staging repo may touch ONLY
`staging/**`; any PR touching workflows, scripts, schema, or other paths is
auto-rejected. The server gate is authoritative — re-running validation and
scrubbing from scratch and never trusting the client.
