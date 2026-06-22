package main

import (
	"fmt"
	"os"

	"github.com/Sipaha/outwall/internal/cli"

	// Bundle server-profile plugins (self-register via init()). The core never imports these; the
	// binary entrypoint opts each one in. Add a line here for each new platform plugin.
	_ "github.com/Sipaha/outwall/internal/serverprofile/citeck"
)

func main() {
	if err := cli.NewRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
