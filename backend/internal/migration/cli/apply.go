package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"tokenhub/backend/internal/migration/bundle"
	migrationtokenhub "tokenhub/backend/internal/migration/sink/tokenhub"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(applyCmd)
	rootCmd.AddCommand(planCmd)
	rootCmd.AddCommand(verifyCmd)
	rootCmd.AddCommand(rollbackCmd)

	applyCmd.Flags().String("bundle", "", "Path to bundle JSON file")
	applyCmd.Flags().String("to", "", "TokenHub admin API base URL")
	applyCmd.Flags().String("token", "", "Admin API token")
	applyCmd.Flags().Bool("dry-run", false, "Perform a dry-run instead of writing")
	applyCmd.Flags().String("checkpoint-out", "", "Write the rollback checkpoint JSON to this path (default: <bundle>.checkpoint.json)")
	applyCmd.Flags().String("new-keys-out", "", "Write newly generated API key secrets JSON to this path (default: <bundle>.new-keys.json)")
	_ = applyCmd.MarkFlagRequired("bundle")

	planCmd.Flags().String("bundle", "", "Path to bundle JSON file")
	planCmd.Flags().String("to", "", "TokenHub admin API base URL")
	planCmd.Flags().String("token", "", "Admin API token")
	_ = planCmd.MarkFlagRequired("bundle")

	verifyCmd.Flags().String("bundle", "", "Path to bundle JSON file")
	verifyCmd.Flags().String("to", "", "TokenHub admin API base URL")
	verifyCmd.Flags().String("token", "", "Admin API token")
	_ = verifyCmd.MarkFlagRequired("bundle")

	rollbackCmd.Flags().String("checkpoint", "", "Path to checkpoint JSON file")
	rollbackCmd.Flags().String("to", "", "TokenHub admin API base URL")
	rollbackCmd.Flags().String("token", "", "Admin API token")
	_ = rollbackCmd.MarkFlagRequired("checkpoint")
}

func loadBundle(path string) (*bundle.CanonicalMigrationBundle, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return bundle.Unmarshal(payload)
}

func buildSecretResolver(sourceName string, filePath string) (bundle.SecretResolver, error) {
	switch strings.ToLower(strings.TrimSpace(sourceName)) {
	case "", "env":
		return bundle.EnvResolver{}, nil
	case "file":
		if strings.TrimSpace(filePath) == "" {
			return nil, fmt.Errorf("--secret-file is required when --secret-source=file")
		}
		return bundle.NewFileResolver(filePath)
	default:
		return nil, fmt.Errorf("invalid --secret-source %q: expected env or file", sourceName)
	}
}

func secretsResolver(cmd *cobra.Command) (bundle.SecretResolver, error) {
	sourceName, _ := cmd.Flags().GetString("secret-source")
	filePath, _ := cmd.Flags().GetString("secret-file")
	return buildSecretResolver(sourceName, filePath)
}

func resolveTarget(cmd *cobra.Command) (string, string) {
	baseURL, _ := cmd.Flags().GetString("to")
	token, _ := cmd.Flags().GetString("token")
	baseURL = strings.TrimSpace(baseURL)
	token = strings.TrimSpace(token)
	if baseURL == "" {
		baseURL = strings.TrimSpace(os.Getenv("TOKENHUB_API"))
	}
	if token == "" {
		token = strings.TrimSpace(os.Getenv("TOKENHUB_ADMIN_TOKEN"))
	}
	return baseURL, token
}

// requireRemoteTarget guards mutating commands: applying or rolling back
// against the implicit in-memory store would report success while writing
// nothing durable.
func requireRemoteTarget(action string, baseURL string) error {
	if strings.TrimSpace(baseURL) != "" {
		return nil
	}
	return errExit(ExitSinkRejected, fmt.Sprintf("%s requires a TokenHub target: pass --to or set TOKENHUB_API (refusing to %s against a transient in-memory store)", action, action))
}

// writeSecretFile persists payload with owner-only permissions. The
// destination is recreated so a pre-existing wide-permission file or symlink
// can never expose the secret, even briefly.
func writeSecretFile(path string, payload []byte) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(payload); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// writeApplyArtifacts persists the rollback checkpoint and any one-time API
// key secrets returned by apply so they are not silently discarded. The
// remote apply has already changed state by the time this runs, so on write
// failure the payloads are dumped to stdout as a last resort: the checkpoint
// and key plaintext cannot be retrieved again.
func writeApplyArtifacts(cmd *cobra.Command, bundlePath string, result *migrationtokenhub.ApplyResult) error {
	checkpointPath, _ := cmd.Flags().GetString("checkpoint-out")
	if strings.TrimSpace(checkpointPath) == "" {
		checkpointPath = bundlePath + ".checkpoint.json"
	}
	checkpointPayload, err := json.MarshalIndent(result.Checkpoint, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal checkpoint: %w", err)
	}
	var failures []string
	if err := writeSecretFile(checkpointPath, checkpointPayload); err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: remote apply changed state, but writing the checkpoint failed (%v); dumping it below so rollback stays possible:\n", err)
		fmt.Printf("--- checkpoint ---\n%s\n", checkpointPayload)
		failures = append(failures, fmt.Sprintf("write checkpoint: %v", err))
	} else {
		fmt.Printf("  Checkpoint: %s\n", checkpointPath)
	}
	if len(result.NewKeys) > 0 {
		newKeysPath, _ := cmd.Flags().GetString("new-keys-out")
		if strings.TrimSpace(newKeysPath) == "" {
			newKeysPath = bundlePath + ".new-keys.json"
		}
		newKeysPayload, err := json.MarshalIndent(map[string]any{"new_keys": result.NewKeys}, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal new keys: %w", err)
		}
		if err := writeSecretFile(newKeysPath, newKeysPayload); err != nil {
			fmt.Fprintf(os.Stderr, "WARNING: remote apply changed state, but writing the new API keys failed (%v); dumping the one-time plaintext below — it cannot be retrieved again:\n", err)
			fmt.Printf("--- new API keys ---\n%s\n", newKeysPayload)
			failures = append(failures, fmt.Sprintf("write new keys: %v", err))
		} else {
			fmt.Printf("  New API keys (%d, plaintext visible once): %s — distribute securely, then delete the file\n", len(result.NewKeys), newKeysPath)
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("remote apply changed state, but persisting artifacts failed (%s); do not re-run apply — capture the dumped output above", strings.Join(failures, "; "))
	}
	return nil
}

// newHTTPSink builds the production Admin API sink. The client is constructed
// with a nil httpClient so it receives the package default total timeout, and
// a malformed --to value is rejected here as a controlled error instead of
// surfacing mid-flight.
func newHTTPSink(baseURL string, token string, resolver bundle.SecretResolver) (*migrationtokenhub.HTTPSink, error) {
	client, err := migrationtokenhub.NewAdminAPIClient(baseURL, token, nil)
	if err != nil {
		return nil, err
	}
	return migrationtokenhub.NewHTTPSink(client, resolver), nil
}

func handleApplyResult(cmd *cobra.Command, bundlePath string, result *migrationtokenhub.ApplyResult, applyErr error) error {
	var artifactErr error
	if result != nil {
		label := "Apply complete"
		if applyErr != nil {
			label = "Apply partially complete"
		}
		fmt.Printf("%s:\n  Created: %d\n  Updated: %d\n  Skipped: %d\n", label, result.Report.Created, result.Report.Updated, result.Report.Skipped)
		artifactErr = writeApplyArtifacts(cmd, bundlePath, result)
	}
	if applyErr != nil {
		if artifactErr != nil {
			return fmt.Errorf("%w; %v", applyErr, artifactErr)
		}
		return applyErr
	}
	return artifactErr
}

var planCmd = &cobra.Command{
	Use:   "plan",
	Short: "Dry-run: show what apply would do",
	RunE: func(cmd *cobra.Command, args []string) error {
		bundlePath, _ := cmd.Flags().GetString("bundle")
		migrationBundle, err := loadBundle(bundlePath)
		if err != nil {
			return errExit(ExitSchemaMismatch, err.Error())
		}
		baseURL, token := resolveTarget(cmd)
		if err := requireRemoteTarget("plan", baseURL); err != nil {
			return err
		}
		resolver, err := secretsResolver(cmd)
		if err != nil {
			return errExit(ExitSourceUnreadable, err.Error())
		}
		sink, err := newHTTPSink(baseURL, token, resolver)
		if err != nil {
			return errExit(ExitSinkRejected, err.Error())
		}
		report, err := sink.Plan(context.Background(), migrationBundle)
		if err != nil {
			return errExit(ExitSinkRejected, err.Error())
		}
		fmt.Printf("Plan:\n  Created: %d\n  Updated: %d\n  Skipped: %d\n", report.Created, report.Updated, report.Skipped)
		return nil
	},
}

var applyCmd = &cobra.Command{
	Use:   "apply",
	Short: "Apply a bundle to TokenHub",
	RunE: func(cmd *cobra.Command, args []string) error {
		bundlePath, _ := cmd.Flags().GetString("bundle")
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		migrationBundle, err := loadBundle(bundlePath)
		if err != nil {
			return errExit(ExitSchemaMismatch, err.Error())
		}
		baseURL, token := resolveTarget(cmd)
		if err := requireRemoteTarget("apply", baseURL); err != nil {
			return err
		}
		resolver, err := secretsResolver(cmd)
		if err != nil {
			return errExit(ExitSourceUnreadable, err.Error())
		}
		sink, err := newHTTPSink(baseURL, token, resolver)
		if err != nil {
			return errExit(ExitSinkRejected, err.Error())
		}
		if dryRun {
			report, err := sink.Plan(context.Background(), migrationBundle)
			if err != nil {
				return errExit(ExitSinkRejected, err.Error())
			}
			fmt.Printf("Dry-run plan:\n  Created: %d\n  Updated: %d\n  Skipped: %d\n", report.Created, report.Updated, report.Skipped)
			return nil
		}
		result, applyErr := sink.Apply(context.Background(), migrationBundle)
		if err := handleApplyResult(cmd, bundlePath, result, applyErr); err != nil {
			return errExit(ExitSinkRejected, err.Error())
		}
		return nil
	},
}

var verifyCmd = &cobra.Command{
	Use:   "verify",
	Short: "Verify bundle consistency",
	RunE: func(cmd *cobra.Command, args []string) error {
		bundlePath, _ := cmd.Flags().GetString("bundle")
		migrationBundle, err := loadBundle(bundlePath)
		if err != nil {
			return errExit(ExitSchemaMismatch, err.Error())
		}
		baseURL, token := resolveTarget(cmd)
		if err := requireRemoteTarget("verify", baseURL); err != nil {
			return err
		}
		resolver, err := secretsResolver(cmd)
		if err != nil {
			return errExit(ExitSourceUnreadable, err.Error())
		}
		sink, err := newHTTPSink(baseURL, token, resolver)
		if err != nil {
			return errExit(ExitSinkRejected, err.Error())
		}
		result, err := sink.Verify(context.Background(), migrationBundle)
		if err != nil {
			return errExit(ExitSinkRejected, err.Error())
		}
		if result.OK {
			fmt.Println("Verify: PASS")
			return nil
		}
		fmt.Fprintf(os.Stderr, "Verify: FAIL (%d issues)\n", len(result.Issues))
		for _, issue := range result.Issues {
			fmt.Fprintf(os.Stderr, "  [%s] %s: %s\n", issue.Resource, issue.Ref, issue.Message)
		}
		return errExit(ExitVerifyMismatch, "verification mismatch")
	},
}

var rollbackCmd = &cobra.Command{
	Use:   "rollback",
	Short: "Rollback from a checkpoint file",
	RunE: func(cmd *cobra.Command, args []string) error {
		checkpointPath, _ := cmd.Flags().GetString("checkpoint")
		payload, err := os.ReadFile(checkpointPath)
		if err != nil {
			return errExit(ExitSinkRejected, err.Error())
		}
		var checkpoint migrationtokenhub.Checkpoint
		if err := bundle.UnmarshalCheckpoint(payload, &checkpoint); err != nil {
			return errExit(ExitSchemaMismatch, err.Error())
		}
		baseURL, token := resolveTarget(cmd)
		if err := requireRemoteTarget("rollback", baseURL); err != nil {
			return err
		}
		sink, err := newHTTPSink(baseURL, token, bundle.EnvResolver{})
		if err != nil {
			return errExit(ExitSinkRejected, err.Error())
		}
		result, err := sink.Rollback(context.Background(), checkpoint)
		if err != nil {
			return errExit(ExitSinkRejected, err.Error())
		}
		fmt.Printf("Rollback: %d changes reverted\n", len(result.Changes))
		return nil
	},
}
