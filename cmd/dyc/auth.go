package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/GeeveGeorge/donate-your-code/internal/github"
	"github.com/GeeveGeorge/donate-your-code/internal/state"
)

func cmdAuth(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: dyc auth login|status|logout")
		return 2
	}
	switch args[0] {
	case "login":
		return authLogin(args[1:])
	case "status":
		return authStatus()
	case "logout":
		return authLogout()
	default:
		fmt.Fprintf(os.Stderr, "auth: unknown subcommand %q\n", args[0])
		return 2
	}
}

func authLogin(args []string) int {
	fromStdin := false
	fromGh := false
	for _, a := range args {
		switch a {
		case "--token-stdin":
			fromStdin = true
		case "--from-gh":
			fromGh = true
		default:
			fmt.Fprintf(os.Stderr, "auth login: unknown flag %q\n", a)
			return 2
		}
	}

	var token string
	switch {
	case fromStdin:
		token = readStdinToken()
	case fromGh:
		token = ghCLIToken()
	default:
		// Prefer the gh CLI (no token echoed); fall back to stdin.
		if token = ghCLIToken(); token == "" {
			fmt.Fprintln(os.Stderr, "Paste a fine-grained GitHub token (fork + contents:write + pull-requests:write), then press Enter.")
			fmt.Fprintln(os.Stderr, "Note: input may be visible. Prefer `dyc auth login --from-gh` or DYC_GITHUB_TOKEN to avoid echoing.")
			token = readStdinToken()
		}
	}
	if token == "" {
		fmt.Fprintln(os.Stderr, "auth login: no token provided")
		return 1
	}

	u, err := github.New(token, false).GetUser()
	if err != nil {
		fmt.Fprintf(os.Stderr, "auth login: token rejected by GitHub: %v\n", err)
		return 1
	}
	if err := state.SaveToken(token); err != nil {
		fmt.Fprintf(os.Stderr, "auth login: could not store token: %v\n", err)
		return 1
	}
	fmt.Printf("Logged in as %s. Token stored (0600) in the dyc state dir.\n", u.Login)
	return 0
}

func authStatus() int {
	token, src := state.ResolveToken()
	if token == "" {
		fmt.Println("Not logged in. Run `dyc auth login` or set DYC_GITHUB_TOKEN.")
		return 0
	}
	u, err := github.New(token, false).GetUser()
	if err != nil {
		fmt.Printf("Token present (source: %s) but GitHub rejected it: %v\n", src, err)
		return 1
	}
	fmt.Printf("Logged in as %s (token source: %s).\n", u.Login, src)
	return 0
}

func authLogout() int {
	if err := state.DeleteToken(); err != nil {
		fmt.Fprintf(os.Stderr, "auth logout: %v\n", err)
		return 1
	}
	fmt.Println("Stored token removed. (Environment tokens, if any, are still in effect.)")
	return 0
}

func readStdinToken() string {
	sc := bufio.NewScanner(os.Stdin)
	if sc.Scan() {
		return strings.TrimSpace(sc.Text())
	}
	return ""
}

func ghCLIToken() string {
	path, err := exec.LookPath("gh")
	if err != nil {
		return ""
	}
	out, err := exec.Command(path, "auth", "token").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
