package cli

import (
	"context"
	"fmt"

	"tokenhub/backend/internal/migration/source"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(inspectCmd)
	inspectCmd.Flags().String("from", "", "Path to source gateway config")
	_ = inspectCmd.MarkFlagRequired("from")
}

var inspectCmd = &cobra.Command{
	Use:   "inspect [source]",
	Short: "Inspect a source gateway configuration",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		sourceName := args[0]
		extractor, err := source.Get(sourceName)
		if err != nil {
			return errExit(ExitSourceUnreadable, err.Error())
		}

		fromPath, _ := cmd.Flags().GetString("from")
		info, err := extractor.Probe(context.Background(), source.ExtractOptions{InputPath: fromPath})
		if err != nil {
			return errExit(ExitSourceUnreadable, err.Error())
		}

		fmt.Printf("Adapter: %s\n", info.Adapter)
		fmt.Printf("Version: %s\n", info.Version)
		fmt.Printf("Ready:   %v\n", info.Ready)
		if info.Note != "" {
			fmt.Printf("Note:    %s\n", info.Note)
		}
		return nil
	},
}
