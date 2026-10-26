package main

import (
	"fmt"
	"os"

	"github.com/adnair2024/beacon/internal/cli"
	"github.com/adnair2024/beacon/internal/envfile"
	ver "github.com/adnair2024/beacon/internal/version"
)

// Stamped by the release build: -ldflags "-X main.version=... -X main.commit=... -X main.date=...".
var (
	version = "dev"
	commit  = ""
	date    = ""
)

func main() {
	// A .env in the current directory (development from a checkout) may set
	// beacon's own variables; the real environment always wins.
	env := envfile.Load(".env")
	v := ver.Current(ver.Info{Version: version, Commit: commit, Date: date})
	if err := cli.NewRoot(v, env).Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "beacon:", err)
		os.Exit(1)
	}
}
