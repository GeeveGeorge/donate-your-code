# Security model & threat model

Donate Your Code moves potentially-sensitive transcript data off users' machines.
Security is the primary design goal. This document is the threat model; the
`internal/` packages and the CI gate are its enforcement.

## Invariants

- **I1 Single egress.** The client's only network destination is the GitHub API.
  `scan` and `preview` make no network calls (`--no-net` proves it).
- **I2 Hard read allowlist.** The client reads ONLY `projects/**/*.jsonl`,
  `**/subagents/*.jsonl`, and the `tool-results/*.txt` files explicitly referenced
  by a `<persisted-output>` wrapper — nothing else, no arbitrary paths.
- **I3 No path escape / I4 no symlink escape.** Every opened path is cleaned, made
  absolute, and its **real (symlink-resolved) path must be contained** within an
  approved root; the leaf must be a regular, non-symlink file. External
  tool-results must resolve inside the same session's `tool-results/` directory.
- **I5 Fail closed.** Any scrub error, unresolved external file, oversized read, or
  tripwire hit drops the whole record. Default is donate-nothing.
- **I6 Explicit consent.** Per-session opt-in plus a shown post-scrub payload.
- **I7 Content is data, not instructions.** Transcript text is never executed or
  interpreted as commands by the client or the orchestrating agent.
- **I8 Deterministic.** Same inputs → identical canonical records and `record_id`s.

## Threats and mitigations

| Attacker / failure | Mitigation | Enforced |
|---|---|---|
| User accidentally donates secrets/PII (esp. in tool results) | allowlist projection (drop snapshots/attachments/metadata wholesale) + deterministic scrub (secrets, keys, PII, paths, username) + fail-closed tripwire + mandatory preview; CI re-scrubs authoritatively (gitleaks/trufflehog/Presidio) | client + CI |
| Hand-crafted fake "Fable 5" record | structural validation + `model=="claude-fable-5"` + `^msg_` id + reject `<synthetic>`; cross-contributor corroboration + reputation; v3 stylometry. Residual: a poisoned minority is tolerated and disclosed. | CI + consumers |
| Inject code/backdoor via a contribution PR | PRs may touch ONLY `staging/**`; `pull_request` validation runs with no secrets; trusted `workflow_run` re-validates + merges; bot has no `workflows` permission; CODEOWNERS + branch protection. **Structurally impossible to merge code.** | CI |
| Supply-chain attack on `dyc` | reproducible builds, cosign signatures, SBOM, hash-pinned deps; a malicious client still can't bypass the server gate | release + CI |
| Prompt-injection of the orchestrating agent | agent never reads transcripts; only relays `dyc` control-plane output; payload goes to the user's pager, not the agent | AGENTS.md + client channel separation |
| Exfiltration of non-`.claude` files | I2/I3/I4 hard allowlist + containment | client |

## Residual risks (stated honestly)

1. Scrub recall is high but not perfect — residual PII/secrets are possible until
   audits/erasure catch them.
2. Provenance is probabilistic, not cryptographic — no watermark, no signer, no
   re-prompting. Some fraction of records may be spoofed/mislabeled.
3. The Anthropic ToS competing-model tension is a legal, not technical, risk.

## Reporting

Report vulnerabilities privately via the repository's security advisory feature.
Do not open public issues for undisclosed vulnerabilities.
