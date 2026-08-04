package cli

import (
	"errors"

	"github.com/spf13/cobra"
)

const (
	ExitOK               = 0
	ExitPartial          = 2
	ExitVerifyMismatch   = 3
	ExitSourceUnreadable = 4
	ExitSinkRejected     = 5
	ExitSchemaMismatch   = 6
)

type exitError struct {
	code int
	msg  string
}

func (e exitError) Error() string {
	if e.msg != "" {
		return e.msg
	}
	return "migration command failed"
}

func errExit(code int, msg string) error {
	return exitError{code: code, msg: msg}
}

func ExitCode(err error) int {
	var typed exitError
	if errors.As(err, &typed) {
		return typed.code
	}
	return 1
}

func Execute() error {
	return rootCmd.Execute()
}

var rootCmd = &cobra.Command{
	Use:           "tokenhub-migrate",
	Short:         "Migrate competing AI gateways into TokenHub",
	Long:          "tokenhub-migrate provides a repeatable, idempotent workflow for migrating supported gateways into TokenHub.",
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	rootCmd.PersistentFlags().String("secret-source", "env", "Secret resolution source: env|file")
	rootCmd.PersistentFlags().String("secret-file", "", "Path to key=value secrets file (required with --secret-source=file)")
	rootCmd.PersistentFlags().String("id-strategy", "prefixed", "ID generation strategy: stable|prefixed|source")
	rootCmd.PersistentFlags().String("report", "", "Write structured report to JSON file")
	rootCmd.PersistentFlags().String("log-level", "info", "Log level: debug|info|warn|error")
}
