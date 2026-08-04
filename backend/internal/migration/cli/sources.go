package cli

import (
	"fmt"
	"sort"

	"tokenhub/backend/internal/migration/source"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(sourcesCmd)
}

var sourcesCmd = &cobra.Command{
	Use:   "sources",
	Short: "List registered source adapters",
	RunE: func(cmd *cobra.Command, args []string) error {
		names := source.Names()
		sort.Strings(names)
		for _, name := range names {
			extractor, err := source.Get(name)
			if err != nil {
				fmt.Printf("%s (error: %v)\n", name, err)
				continue
			}
			versions := extractor.SupportedVersions()
			versionStr := "all"
			if len(versions) == 1 {
				versionStr = versions[0]
			}
			fmt.Printf("%s\t%s\n", name, versionStr)
		}
		return nil
	},
}
