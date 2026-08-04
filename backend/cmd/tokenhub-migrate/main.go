package main

import (
	"fmt"
	"os"

	"tokenhub/backend/internal/migration/cli"
	_ "tokenhub/backend/internal/migration/source/litellm"
)

func main() {
	if err := cli.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(cli.ExitCode(err))
	}
}
