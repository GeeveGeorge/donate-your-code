# X / Twitter announcement (draft)

A thread you can post once the staging repo + HF dataset are live and a signed
release is cut. Edit the voice to taste.

---

**1/**
Donate Your Code 🧶

A free, open way to donate your own Claude **Fable 5** coding transcripts to a
public, CC0 dataset — without leaking secrets.

Point your Claude Code agent at the repo, pick which projects to share, done.

github.com/GeeveGeorge/donate-your-code

**2/**
How it works:

`dyc` is a tiny, audited CLI. It finds genuine Fable 5 turns in your local
~/.claude transcripts, scrubs secrets + PII (fail-closed), and opens ONE GitHub PR
with your own token.

No backend. No API key. No data leaves your machine unless you confirm.

**3/**
Security is the whole point:

• hard allowlist — it only ever reads ~/.claude transcripts, nothing else
• single egress — the only network call is the GitHub API
• fail-closed scrub + a tripwire: if a secret survives, the record is dropped
• you preview the EXACT scrubbed payload before anything is sent

**4/**
The agent never reads your transcripts. It just runs `dyc` and helps you choose.
That kills prompt-injection from a poisoned transcript — the bytes never enter the
model's context. It's all in AGENTS.md.

**5/**
Server side, "no junk gets in" is structural, not a promise:

PRs can only add content-addressed records under staging/**. A GitHub Actions gate
re-validates everything from scratch, a bot merges what passes, and an hourly job
compacts to a Hugging Face Parquet dataset. Code changes via PR are impossible.

**6/**
Honest about the hard part: you can't *prove* a transcript is Fable 5 (the model
field is plaintext). So every record is labeled self-attested-unverified, and we
lean on cross-contributor corroboration + structural checks. Filter accordingly.

**7/**
CC0. Apache-2.0 tooling. Go↔Python content-addressing is conformance-tested so the
client and the server agree byte-for-byte.

Try it, break it, send PRs:
github.com/GeeveGeorge/donate-your-code

🧶
