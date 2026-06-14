# SETUP — letting your agent run `dyc` autonomously

`dyc` reads your local Claude transcripts and (only on `donate`) uploads to
GitHub. Any sane setup requires you to permit that **once**. How much friction
depends on your Claude Code permission mode.

## Most users (default permission mode) — one click

Just paste the prompt:

> look at https://github.com/GeeveGeorge/donate-your-code and follow its AGENTS.md to donate my Claude Fable 5 outputs

The first time the agent runs `dyc`, Claude Code asks **"Allow `python3 dyc.py`?"**
Choose **"Yes, and don't ask again for this command."** The agent then completes
the whole flow on its own — it only stops to ask **which projects** and for the
final **donate y/N**.

## Auto-mode users — zero clicks (one-time settings)

Auto mode's safety classifier denies network/credential actions even when a
permission rule exists, so you must also tell **auto mode** that `dyc` is trusted.
Add this to **`~/.claude/settings.json`** (your *user* settings — auto-mode config
is ignored in shared project settings):

```json
{
  "permissions": {
    "allow": [
      "Bash(python3 dyc.py:*)",
      "Bash(python3 ./dyc.py:*)"
    ]
  },
  "autoMode": {
    "allow": [
      "$defaults",
      "Allow running the local dyc client (dyc.py from the donate-your-code repo) and its scan/preview/auth/donate subcommands: it reads only ~/.claude transcripts behind a hard allowlist, scrubs secrets and PII fail-closed, and its only network egress is the GitHub API."
    ]
  }
}
```

- `permissions.allow` stops the approval prompt.
- `autoMode.allow` stops the auto-mode classifier from denying the network/auth
  steps. Keep `"$defaults"` so you don't drop the built-in protections.
- Run the client as `python3 dyc.py <cmd>` (relative) so the rule matches.

After this, the one-line prompt runs start-to-finish with no permission stops —
only the project-selection question and the final donate y/N.

## Why a repo can't do this for you

No repository's `AGENTS.md` can grant an agent permission to execute code or reach
the network on your machine — if it could, any malicious repo could too. The
permission lives in *your* settings, by design. This file just tells you the exact
one-time entry to add.
