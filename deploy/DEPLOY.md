# Deploy runbook (one-time)

Three repos: the **tooling** repo (this one, users clone it), the **staging** repo
(donation PRs + vetting), and the **Hugging Face dataset** (published Parquet).

## 0. Tooling repo (done)

`github.com/GeeveGeorge/donate-your-code` is live. Two workflow templates need a
token with `workflow` scope to install (GitHub blocks pushing `.github/workflows/`
files otherwise):

```sh
gh auth refresh -h github.com -s workflow        # one-time
mkdir -p .github/workflows
cp deploy/tooling-repo/.github/workflows/*.yml .github/workflows/
git add .github/workflows && git commit -m "ci: add CI + signed release" && git push
```

(Or paste the two files in via the GitHub web UI, which doesn't need the scope.)

## 1. Staging repo

```sh
# from the tooling repo:
mkdir -p /tmp/staging && cp -r deploy/staging-repo/. /tmp/staging/
cp schema/record.schema.json /tmp/staging/schema/   # share the schema
cd /tmp/staging && git init -b main && git add -A && git commit -m "init staging gate"
gh repo create GeeveGeorge/donate-your-code-staging --public --source=. --push
```

Then on `GeeveGeorge/donate-your-code-staging`:

- **Branch protection** on `main`: require status checks, **require review from Code
  Owners**, disallow force-push and direct push. (Contribution PRs add only
  `staging/**`; CODEOWNERS guards everything else.)
- **Actions → General**: allow GitHub Actions; allow the workflow to create/approve
  PR merges if you want the gatekeeper to auto-merge (Settings → Actions → "Allow
  GitHub Actions to create and approve pull requests").
- **Environment `compaction`**: add secret `HF_TOKEN` (a Hugging Face *write* token
  scoped to the dataset) and variable `HF_DATASET=GeeveGeorge/donate-your-code`.
  Optionally require a reviewer on the environment to gate token use.
- Confirm the three workflows are present and enabled: `validate`, `gatekeeper`,
  `compact`.

## 2. Hugging Face dataset

```sh
huggingface-cli repo create donate-your-code --type dataset      # as GeeveGeorge
# upload the dataset card as the dataset README:
cp deploy/dataset-card.md README.md   # then push to the HF repo
```

Create a **write** token scoped to that dataset and store it as `HF_TOKEN` in the
staging repo's `compaction` environment (step 1).

## 3. Sanity check end-to-end

```sh
go build -o dyc ./cmd/dyc
./dyc scan
./dyc auth login                       # uses gh token
./dyc donate <project> --dry-run       # offline preview
./dyc donate <project>                 # opens a real PR → validate → gatekeeper merge → (hourly) compact → HF
```

## 4. Before a public launch

- Vendor the full `LICENSE-CODE` (Apache-2.0) and `LICENSE-DATA` (CC0) texts.
- SHA-pin/​hash-pin remaining deps (`requirements.txt` with `--require-hashes`),
  run `zizmor` over the workflows, enable Dependabot for `github-actions`.
- Cut a signed release (tag `v0.1.0`) so `AGENTS.md` can pin the `dyc` checksum +
  cosign identity. Add the published SHA-256 to a `CHECKSUMS` file referenced by
  `AGENTS.md`.
- Get the Anthropic-terms framing reviewed by counsel; keep the dataset framed as
  research/evaluation.
