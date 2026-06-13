package main

import (
	"fmt"
	"os"

	"github.com/GeeveGeorge/donate-your-code/internal/discover"
	"github.com/GeeveGeorge/donate-your-code/internal/scrub"
	"github.com/GeeveGeorge/donate-your-code/internal/state"
)

func cmdStatus(args []string) int {
	for _, a := range args {
		fmt.Fprintf(os.Stderr, "status: unknown flag %q\n", a)
		return 2
	}
	roots := discover.ConfigRoots()
	owner, repo := stagingTarget()
	dir, _ := state.Dir()
	_, tokenSrc := state.ResolveToken()
	if tokenSrc == "" {
		tokenSrc = "none (run `dyc auth login`)"
	}
	donated, _ := state.LoadDonated()

	fmt.Printf("dyc %s   scrubber %s   schema %s\n", version, scrub.Version(), recordSchemaVersion)
	fmt.Printf("state dir:       %s\n", dir)
	fmt.Printf("token:           %s\n", tokenSrc)
	fmt.Printf("staging target:  %s/%s\n", owner, repo)
	fmt.Printf("records donated: %d\n", len(donated))
	fmt.Println("claude config roots:")
	if len(roots) == 0 {
		fmt.Println("  (none found)")
	}
	for _, r := range roots {
		fmt.Printf("  %s\n", r)
	}
	return 0
}
