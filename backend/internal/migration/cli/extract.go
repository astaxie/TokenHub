package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"tokenhub/backend/internal/migration/bundle"
	"tokenhub/backend/internal/migration/source"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(extractCmd)
	extractCmd.Flags().String("from", "", "Path to source gateway config")
	extractCmd.Flags().String("out", "", "Output bundle JSON file (default: stdout)")
	_ = extractCmd.MarkFlagRequired("from")
}

var extractCmd = &cobra.Command{
	Use:   "extract [source]",
	Short: "Extract a canonical migration bundle from a source gateway",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		sourceName := args[0]
		extractor, err := source.Get(sourceName)
		if err != nil {
			return errExit(ExitSourceUnreadable, fmt.Sprintf("unknown source %q: %v", sourceName, err))
		}

		fromPath, _ := cmd.Flags().GetString("from")
		outPath, _ := cmd.Flags().GetString("out")
		idStrategyName, _ := cmd.Flags().GetString("id-strategy")
		idStrategy := bundle.IDStrategy(strings.ToLower(strings.TrimSpace(idStrategyName)))
		if !idStrategy.Valid() {
			return errExit(ExitSourceUnreadable, fmt.Sprintf("invalid --id-strategy %q: expected stable, prefixed, or source", idStrategyName))
		}

		migrationBundle, err := extractor.Extract(context.Background(), source.ExtractOptions{
			InputPath:  fromPath,
			IDStrategy: idStrategy,
		})
		if err != nil {
			return errExit(ExitSourceUnreadable, err.Error())
		}

		payload, err := bundle.Marshal(migrationBundle)
		if err != nil {
			return errExit(ExitSchemaMismatch, err.Error())
		}

		if outPath != "" {
			if err := os.WriteFile(outPath, payload, 0o644); err != nil {
				return errExit(ExitSinkRejected, err.Error())
			}
			fmt.Printf("Bundle written to %s\n", outPath)
		} else {
			_, _ = os.Stdout.Write(payload)
			_, _ = os.Stdout.Write([]byte("\n"))
		}

		if len(migrationBundle.Warnings) > 0 {
			fmt.Fprintf(os.Stderr, "Warnings (%d):\n", len(migrationBundle.Warnings))
			for _, warning := range migrationBundle.Warnings {
				fmt.Fprintf(os.Stderr, "  [%s] %s: %s\n", warning.Severity, warning.Code, warning.Message)
			}
		}
		return nil
	},
}
