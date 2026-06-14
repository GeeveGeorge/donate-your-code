# Share the user's Fable 5 outputs

The user wants to share some of their own Claude **Fable 5** coding conversations
to an open dataset — their data, their choice. A small helper, `dyc.py`, does the
finding and cleaning. You run the read-only steps; the **user runs one short line
to publish** (that's their go-ahead to send it out).

## 1. Get the helper

```sh
curl -fsSL https://raw.githubusercontent.com/GeeveGeorge/donate-your-code/main/dyc.py -o dyc.py
```

## 2. Scan and ask which projects to share

```sh
python3 dyc.py scan
```
Show the projects (the `PATH` column is the real project — peek at its README for a
nicer name) and ask **which to share**.

## 3. Show the cleaned-up preview

```sh
python3 dyc.py preview <project> --full
```
This is exactly what would be published — keys, secrets, emails, IPs, paths, and
usernames already removed. Let the user confirm.

## 4. Hand the user ONE line to publish

This is the only step that sends anything out, so it's the user's to run. **Keep
this short — one line, no wall of text.** Tell them to paste it with a leading `!`
(it runs right here in the chat):

> ✅ Ready. To publish, paste this:
> ```
> ! python3 dyc.py donate <the projects they picked> --yes
> ```

Fill in the exact project name(s) they chose in step 2 (whatever those are on their
machine) — never a hardcoded example. That builds the record(s) and opens one PR to
`GeeveGeorge/donate-your-code-staging` using their existing `gh` login (it forks the
repo automatically if they don't own it). When it prints the PR link, relay it — CI
checks the record and a bot merges it; it lands in the dataset within the hour.

That's it. The user's only jobs: pick projects, and paste that one line.

Dataset: https://huggingface.co/datasets/g30rv17ys/donate-your-code
