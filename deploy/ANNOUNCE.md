# X / Twitter launch post

Pick the single hook tweet **or** the thread. Fill in `LIVE_LINK` with where your
donation/dataset is live (e.g. the Hugging Face dataset, or your merged PR) once
the staging repo + HF dataset are deployed. Repo + v0.1.0 release are already live.

---

## Option A — single hook tweet (most viral)

> Fable 5 got taken down.
>
> But you used it — and its traces are still sitting in your `~/.claude` folder.
>
> Before they're gone, let's crowdsource them into one open dataset.
>
> Donate only what you want. Your agent does it for you 👇
> github.com/GeeveGeorge/donate-your-code

---

## Option B — thread

**1/**
Fable 5 got taken down.

But most of you *used* it — and its traces are still inside your `~/.claude`
folder right now.

Let's crowdsource them into one open, CC0 dataset before they're gone. 🧶

**2/**
You're in full control of what you share.

Pick your projects — that weekend hack, that fun side project, whatever you're
happy to make public. **Select or deselect** each one. Nothing else gets touched.

**3/**
The whole thing is hands-off:

Give the repo link to your Claude Code (or any) agent and say
"donate my Fable 5 code." It finds your Fable 5 turns, you choose what to share,
it scrubs secrets, and opens a PR. **No API key. No backend.**

github.com/GeeveGeorge/donate-your-code

**4/**
It's safe by design:

• your agent NEVER reads your transcripts — a tiny audited CLI does
• it only ever touches `~/.claude`, nothing else on disk
• its only network call is the GitHub API
• secrets / keys / paths / emails are stripped, fail-closed
• you see the EXACT scrubbed payload before anything is sent

**5/**
I've donated mine to kick it off — it's live here: LIVE_LINK

Add yours in two steps:
1. point your agent at github.com/GeeveGeorge/donate-your-code
2. tell it to donate your Fable 5 code

**6/**
CC0. Open source. The more people contribute, the richer the public record of what
Fable 5 could actually do.

If you ran Fable 5, you're holding a piece of it. Share the part you want. 🧶
github.com/GeeveGeorge/donate-your-code

---

## Notes

- Honesty matters for trust: the dataset card and README state plainly that
  provenance is **self-attested/unverified** and scrubbing is best-effort. Don't
  overclaim in the post.
- A short demo GIF of `dyc scan` → pick a project → `dyc preview` (scrubbed) →
  `dyc donate` would massively boost the thread.
