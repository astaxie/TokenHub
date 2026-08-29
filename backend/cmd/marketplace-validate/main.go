package main

import (
	"fmt"
	"io"
	"os"

	"tokenhub/backend/internal/plugin"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "marketplace-validate:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: marketplace-validate <index.json> [...]")
	}
	for _, path := range args {
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		index, err := plugin.DecodeMarketplaceIndex(data)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		fmt.Fprintf(stdout, "%s: valid marketplace index (%d plugins)\n", path, len(index.Plugins))
	}
	return nil
}
