# Donate Your Code (`dyc`)

Donate your own **Claude Fable 5** transcript turns to a free, public, CC0
dataset — safely. `dyc` is a small, audited Go binary that finds genuine Fable 5
turns in your local Claude Code transcripts, **scrubs secrets and PII**, and (in
later versions) contributes them to a vetted dataset via a least-privilege GitHub
Pull Request.

> **Provenance is self-attested and unverified.** It is impossible to *prove* a
> record came from Fable 5 (the model name is a plaintext field). This project
> enforces structural checks and computes corroboration/confidence signals, but
> the dataset may contain spoofed records. See [SECURITY.md](./SECURITY.md).

> **Legal note.** Anthropic's terms assign output ownership to you but restrict
> using outputs to *train a competing model*. This dataset is framed for
> **research / evaluation / transparency**, you are responsible for your own
> compliance, and the maintainer should obtain counsel before any public launch.
> Not legal advice.

## How it works

```
you ──dyc donate──▶ GitHub staging repo ──CI gate──▶ bot merge ──hourly──▶ Hugging Face Parquet dataset
   (your own token)   (authoritative vetting)        (one writer)          (CC0, streamable)
```

The `dyc` binary is the **trust boundary**: it reads only your `~/.claude`
transcripts behind a hard allowlist, makes no network calls except the GitHub API,
and fails closed. The **server-side CI gate** re-validates and re-scrubs everything
independently — a tampered client can only harm its own submission.

## Quick start — just tell your agent

In Claude Code (or any coding agent), say:

> **look at https://github.com/GeeveGeorge/donate-your-code and donate my Fable 5 code per its AGENTS.md**

The agent installs `dyc`, lists your projects, asks **which ones** you want to
share, shows you the scrubbed preview, and opens the PR. You only pick projects
and approve — no API key, nothing else to set up.

## Or run it yourself

**Zero install** — a single, transparent, stdlib-only Python script (no binary, no
pip, works wherever `python3` is):

```sh
curl -fsSL https://raw.githubusercontent.com/GeeveGeorge/donate-your-code/main/dyc.py -o dyc.py
python3 dyc.py scan                       # list projects + Fable 5 turn counts (no network)
python3 dyc.py preview <selector> --full  # show the EXACT post-scrub payload (no network)
python3 dyc.py auth login                 # store a least-privilege GitHub token (prefers gh)
python3 dyc.py donate <selA> <selB> ...   # scrub + validate + open ONE PR (try --dry-run first)
```

Prefer a compiled binary? `curl -fsSL …/install.sh | sh` (checksum-verified), or
`go install github.com/GeeveGeorge/donate-your-code/cmd/dyc@latest`. The Go and
Python clients produce byte-identical content-addressed records.

A *selector* is a project basename substring, a session id prefix, or `all`; pass
several at once. `scan`, `preview`, and `donate --dry-run` make **no network
calls**; `donate` talks only to the GitHub API. The agent only orchestrates `dyc`
— it never reads your transcripts itself (see [`AGENTS.md`](./AGENTS.md)).

## What gets donated

The full scrubbed conversation thread around each Fable 5 turn (your prompts, the
Fable 5 responses, tool calls, and tool results) — **after** redacting private
keys, vendor secrets, high-entropy tokens, emails, phone numbers, payment cards,
IPs, absolute paths, and your username. File-history snapshots and attachments are
dropped wholesale. Nothing is donated without explicit per-session opt-in and a
shown preview.

## Status

The **client is complete and working**: deterministic scan/preview/scrub/
canonicalize, agent-driven project selection, and `donate` (fork → DCO commit → PR)
over an egress-locked GitHub client, all with tests. The Go↔Python
canonicalization is conformance-tested (`deploy/staging-repo/tests`).

In progress: the server-side CI gate (`validate.yml` / `gatekeeper.yml` /
`compact.yml`), compaction to a Hugging Face Parquet dataset, and provenance
scoring. See `SECURITY.md` and `deploy/`.

## Layout

```
cmd/dyc/                CLI (scan, preview, version)
internal/discover/      config-root resolution + path-safe allowlist
internal/transcript/    JSONL parsing + genuine-Fable5 predicate
internal/thread/        DAG thread reconstruction + record building
internal/extern/        <persisted-output> external file resolution
internal/scrub/         deterministic fail-closed redaction + tripwire
internal/record/        canonical record + RFC 8785 record_id
schema/                 record.schema.json + canonicalization.md (shared with server)
AGENTS.md / CLAUDE.md   agent orchestration runbook
SECURITY.md             threat model
```

## Licenses

- Tool code: **Apache-2.0** ([LICENSE-CODE](./LICENSE-CODE)).
- Contributed data / schemas: **CC0-1.0** ([LICENSE-DATA](./LICENSE-DATA)).

Contributions are made under the DCO (`git commit -s`) certifying your right to
donate and your CC0 dedication; see [CONTRIBUTING.md](./CONTRIBUTING.md).
