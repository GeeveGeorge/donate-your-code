package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/GeeveGeorge/donate-your-code/internal/discover"
	"github.com/GeeveGeorge/donate-your-code/internal/github"
	"github.com/GeeveGeorge/donate-your-code/internal/record"
	"github.com/GeeveGeorge/donate-your-code/internal/scrub"
	"github.com/GeeveGeorge/donate-your-code/internal/state"
	"github.com/GeeveGeorge/donate-your-code/internal/thread"
)

// Staging repo coordinates. Compiled-in so a typo/MITM can't redirect donations;
// overridable for self-hosting via env.
func stagingTarget() (owner, repo string) {
	owner = envOr("DYC_STAGING_OWNER", "GeeveGeorge")
	repo = envOr("DYC_STAGING_REPO", "donate-your-code-staging")
	return
}

func envOr(k, d string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return d
}

func cmdDonate(args []string) int {
	var selectors []string
	dryRun := false
	assumeYes := false
	noNet := false
	maxRecords := 0
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--dry-run":
			dryRun = true
		case a == "--yes" || a == "-y":
			assumeYes = true
		case a == "--no-net":
			noNet = true
		case a == "--all":
			selectors = append(selectors, "all")
		case a == "--max-records":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "donate: --max-records needs a value")
				return 2
			}
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil {
				fmt.Fprintln(os.Stderr, "donate: --max-records value must be an integer")
				return 2
			}
			maxRecords = n
		case strings.HasPrefix(a, "--"):
			fmt.Fprintf(os.Stderr, "donate: unknown flag %q\n", a)
			return 2
		default:
			selectors = append(selectors, a)
		}
	}
	if len(selectors) == 0 {
		fmt.Fprintln(os.Stderr, "donate: at least one selector is required (project basename, session id prefix, or --all)")
		return 2
	}

	// Build candidate records (offline).
	roots := discover.ConfigRoots()
	sessions, _ := discover.DiscoverSessions(roots)
	var encoded []string
	for _, s := range sessions {
		encoded = append(encoded, s.EncodedName)
	}
	scrubber := scrub.New(discover.Username(encoded...))
	salt, err := state.Salt()
	if err != nil {
		fmt.Fprintf(os.Stderr, "donate: state error: %v\n", err)
		return 1
	}
	builder := thread.NewBuilder(scrubber, salt, "dyc/"+version)
	donated, _ := state.LoadDonated()

	type item struct {
		session discover.Session
		rec     *record.Record
	}
	var items []item
	var dropped, skipped int
	for _, s := range sessions {
		if !matchAny(selectors, s) {
			continue
		}
		for _, res := range builder.BuildSession(s) {
			switch res.Status {
			case "ok":
				if _, done := donated[res.Record.RecordID]; done {
					skipped++
					continue
				}
				items = append(items, item{s, res.Record})
			case "dropped":
				dropped++
			}
		}
	}
	if maxRecords > 0 && len(items) > maxRecords {
		items = items[:maxRecords]
	}

	if len(items) == 0 {
		fmt.Printf("Nothing to donate. (%d dropped fail-closed, %d already donated.)\n", dropped, skipped)
		return 0
	}

	owner, repo := stagingTarget()
	fmt.Printf("Ready to donate %d record(s) to %s/%s", len(items), owner, repo)
	if dropped+skipped > 0 {
		fmt.Printf("  (%d dropped fail-closed, %d already donated)", dropped, skipped)
	}
	fmt.Println()
	for _, it := range items {
		fmt.Printf("  %s  %s/%s  (%d messages)\n", it.rec.RecordID[:12], it.session.ProjectBasename, short(it.session.SessionID), len(it.rec.Messages))
	}
	fmt.Println("\nLicense: CC0-1.0   Provenance: self-attested   DCO sign-off will be added to the commit.")
	fmt.Println("Tip: run `dyc preview <selector> --full` to inspect the exact scrubbed payload first.")

	if dryRun {
		fmt.Println("\n[dry-run] No network calls were made. The above records would be submitted as a single PR.")
		return 0
	}

	if !assumeYes && !confirm(fmt.Sprintf("Submit %d record(s) as a public CC0 PR?", len(items))) {
		fmt.Println("Aborted. Nothing was sent.")
		return 0
	}

	// --- network: submit ---
	token, src := state.ResolveToken()
	if token == "" {
		fmt.Fprintln(os.Stderr, "donate: no GitHub token. Run `dyc auth login` or set DYC_GITHUB_TOKEN.")
		return 1
	}
	gh := github.New(token, noNet)
	user, err := gh.GetUser()
	if err != nil {
		fmt.Fprintf(os.Stderr, "donate: GitHub auth failed (%s): %v\n", src, err)
		return 1
	}

	base, err := gh.GetRepo(owner, repo)
	if err != nil {
		fmt.Fprintf(os.Stderr, "donate: cannot read staging repo %s/%s: %v\n", owner, repo, err)
		return 1
	}
	if _, err := gh.EnsureFork(owner, repo, user.Login); err != nil {
		fmt.Fprintf(os.Stderr, "donate: %v\n", err)
		return 1
	}
	branch := "dyc/" + randHex(6)
	baseSHA, err := gh.GetBranchSHA(user.Login, repo, base.DefaultBranch)
	if err != nil {
		fmt.Fprintf(os.Stderr, "donate: read fork tip: %v\n", err)
		return 1
	}
	if err := gh.CreateBranch(user.Login, repo, branch, baseSHA); err != nil {
		fmt.Fprintf(os.Stderr, "donate: create branch: %v\n", err)
		return 1
	}
	baseTree, err := gh.BaseTreeOf(user.Login, repo, baseSHA)
	if err != nil {
		fmt.Fprintf(os.Stderr, "donate: read base tree: %v\n", err)
		return 1
	}

	var entries []github.TreeEntry
	for _, it := range items {
		it.rec.Contributor = user.Login
		it.rec.DCO = true
		content, err := it.rec.Marshal()
		if err != nil {
			fmt.Fprintf(os.Stderr, "donate: marshal %s: %v\n", it.rec.RecordID[:12], err)
			return 1
		}
		blobSHA, err := gh.CreateBlob(user.Login, repo, content)
		if err != nil {
			fmt.Fprintf(os.Stderr, "donate: upload blob: %v\n", err)
			return 1
		}
		entries = append(entries, github.TreeEntry{
			Path: record.ShardPath(it.rec.RecordID), Mode: "100644", Type: "blob", SHA: blobSHA,
		})
	}
	treeSHA, err := gh.CreateTree(user.Login, repo, baseTree, entries)
	if err != nil {
		fmt.Fprintf(os.Stderr, "donate: create tree: %v\n", err)
		return 1
	}
	msg := commitMessage(user, len(items))
	commitSHA, err := gh.CreateCommit(user.Login, repo, msg, treeSHA, baseSHA)
	if err != nil {
		fmt.Fprintf(os.Stderr, "donate: create commit: %v\n", err)
		return 1
	}
	if err := gh.UpdateBranch(user.Login, repo, branch, commitSHA, false); err != nil {
		fmt.Fprintf(os.Stderr, "donate: update branch: %v\n", err)
		return 1
	}
	prURL, err := gh.CreatePR(owner, repo, user.Login+":"+branch, base.DefaultBranch,
		fmt.Sprintf("Donate %d Fable 5 record(s)", len(items)), prBody(len(items)))
	if err != nil {
		fmt.Fprintf(os.Stderr, "donate: open PR: %v\n", err)
		return 1
	}

	now := time.Now().UTC().Format(time.RFC3339)
	for _, it := range items {
		_ = state.AppendDonated(state.DonateEntry{RecordID: it.rec.RecordID, PRURL: prURL, Status: "submitted", At: now})
	}
	fmt.Printf("\n✅ Submitted PR: %s\n", prURL)
	fmt.Println("The CI gate will validate and (if it passes) the bot will merge it. Thank you for contributing!")
	return 0
}

func commitMessage(u *github.User, n int) string {
	name := u.Name
	if name == "" {
		name = u.Login
	}
	email := u.Email
	if email == "" {
		email = u.Login + "@users.noreply.github.com"
	}
	return fmt.Sprintf("Donate %d Fable 5 record(s)\n\nSigned-off-by: %s <%s>\n", n, name, email)
}

func prBody(n int) string {
	return fmt.Sprintf(`Automated donation of %d Claude Fable 5 record(s) via `+"`dyc`"+`.

- License: **CC0-1.0**
- Provenance: **self-attested, unverified**
- Files touched: only `+"`staging/**`"+` content-addressed records
- DCO: signed off in the commit

This PR will be validated by the CI gate and merged by the bot if it passes.
`, n)
}

func confirm(prompt string) bool {
	fmt.Printf("%s [y/N] ", prompt)
	var resp string
	_, _ = fmt.Scanln(&resp)
	resp = strings.ToLower(strings.TrimSpace(resp))
	return resp == "y" || resp == "yes"
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
