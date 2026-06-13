---
license: cc0-1.0
language:
  - en
pretty_name: Donate Your Code — Claude Fable 5 transcripts (self-attested)
tags:
  - code
  - agent-transcripts
  - claude
  - self-attested
task_categories:
  - text-generation
---

# Donate Your Code — Fable 5 transcript dataset

Community-contributed Claude **Fable 5** coding-agent conversations, scrubbed and
donated by their authors with [`dyc`](https://github.com/GeeveGeorge/donate-your-code).
Each record is one conversation thread containing at least one genuine Fable 5
assistant turn, with the model's prompts, responses, tool calls, and tool results
— after best-effort removal of secrets and PII.

## ⚠️ Read before you use this

- **Provenance is self-attested and UNVERIFIED.** The `model` field in a Claude
  Code transcript is plaintext under the user's control. There is no signer, no
  watermark, and no way to re-prompt — so it is **impossible to cryptographically
  prove** any record is genuine Fable 5 output. The pipeline enforces the model
  string, structural integrity, and content-addressing, and (over time) computes
  cross-contributor corroboration and confidence signals — but **expect a poisoned
  minority** and filter accordingly (`confidence_score`, `corroboration_count`).
- **Scrubbing is best-effort, not a guarantee.** Records are scrubbed client-side
  and re-scrubbed server-side (gitleaks + structural backstop; Presidio planned),
  but residual PII/secrets are possible. Report issues; see removal below.
- **Anthropic terms.** Anthropic's terms assign output ownership to the user but
  restrict using outputs to *train a competing model*. This dataset is published
  for **research, evaluation, and transparency**; contributors and downstream users
  are responsible for their own compliance. This is not legal advice.
- **License: CC0-1.0.** Contributors dedicate records to the public domain via DCO
  sign-off. Purely AI-generated text likely has thin/no copyright; CC0 is honest
  about what is transferable.

## Schema (one row per record)

| field | meaning |
|---|---|
| `record_id` | sha256 of the canonical content (content-addressed, dedup key) |
| `model` | always `claude-fable-5` |
| `provenance` | `self-attested-unverified` |
| `messages_json` | the scrubbed thread: user / assistant / tool messages and blocks |
| `models_present` | all assistant models seen in the thread (context may include others) |
| `is_subagent` | whether this thread came from a subagent transcript |
| `claude_code_version` | the Claude Code version that wrote the transcript |
| `contributor` | the donor's GitHub username (pseudonym they consented to expose) |

Confidence/corroboration columns are added as the corroboration and reputation
signals come online.

## Removal / right-to-erasure

Open a removal request keyed by `record_id` (or your contributor pseudonym) in the
[staging repo](https://github.com/GeeveGeorge/donate-your-code-staging). Approved
removals tombstone the record and block re-ingestion. Note that copies already
downloaded from a public CC0 dataset cannot be recalled.

## Load it

```python
from datasets import load_dataset
ds = load_dataset("g30rv17ys/donate-your-code", streaming=True)
```
