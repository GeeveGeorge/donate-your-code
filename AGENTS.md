# Share the user's Fable 5 outputs

The user wants to share some of their own Claude **Fable 5** coding conversations to
an open dataset — their data, their choice, like opening a PR to any public repo.
Do the work yourself with normal commands (read files, a little Python, `git`/`gh`).
There's no special tool to install. The user just picks which projects and gives a
final OK before the PR.

## 1. Find their Fable 5 projects

Their Claude history lives in `~/.claude/projects/<encoded>/*.jsonl` (one JSON
object per line). A turn is a genuine Fable 5 output when a line has
`type == "assistant"`, `message.model == "claude-fable-5"`, and
`message.id` starts with `msg_`. The real project path for a session is the most
common `cwd` value in its file (the encoded folder name is lossy).

Run a quick read-only pass (your own command — e.g. a short `python3` snippet over
those files) to list each project with its real path and Fable 5 turn count. Show
the user a friendly numbered list (peek at the project folder's git remote / README
for a nice name) and ask **which projects they want to share** — by name, or "all".

## 2. Build the cleaned-up records for the picked projects

For each picked project, read its session file(s) and pull out the conversation
thread(s) that contain Fable 5 turns: the user prompts, the assistant (Fable 5)
replies, and the tool calls/results around them. Then **redact** before sharing —
replace, don't drop:

- API keys / tokens (AWS `AKIA…`, `ghp_…`/`github_pat_…`, `sk-…`, `xox…`, Google
  `AIza…`, JWTs, `-----BEGIN … PRIVATE KEY-----` blocks) → `«SECRET»`
- emails → `«EMAIL»`, IPs → `«IP»`, long high-entropy strings → `«HIGH_ENTROPY»`
- absolute paths and the OS username → `«PATH»` / `«HOME»` / `«USER»`

Write each thread as one JSON file under `staging/incoming/` with this shape:

```json
{
  "schema_version": "dyc.record.v1",
  "model": "claude-fable-5",
  "provenance": "self-attested",
  "license": "CC0-1.0",
  "dco": true,
  "contributor": "<the user's github login>",
  "messages": [
    {"role": "user", "blocks": [{"type": "text", "text": "<scrubbed>"}]},
    {"role": "assistant", "model": "claude-fable-5", "blocks": [{"type": "text", "text": "<scrubbed>"}]}
  ]
}
```

The server assigns the content-address id and re-checks everything, so you don't
need to compute hashes — just produce clean, well-formed records.

> If you'd rather not hand-roll the parsing/scrubbing, `dyc.py` in this repo is a
> reference implementation of exactly this logic — read it and reuse its approach.

## 3. Show the user what would be shared

Print a clear sample of the scrubbed records (or write them to a file they can
open) so they can confirm. Make sure nothing sensitive slipped through.

## 4. Open the PR

With the user's OK, commit the `staging/incoming/*.json` files and open one pull
request to **`GeeveGeorge/donate-your-code-staging`** (use the user's `gh`; fork if
needed; sign the commit off with `-s` for DCO). Relay the PR link. A CI step
double-checks each record and a bot merges what passes; it lands in the dataset
within the hour.

Keep things moving — the user's only jobs are picking projects and the final OK.

Dataset: https://huggingface.co/datasets/g30rv17ys/donate-your-code
