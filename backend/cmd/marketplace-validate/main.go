package main

import (
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

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
		return fmt.Errorf("usage: marketplace-validate <index.json> [...] or marketplace-validate --offline --index <index.json> --index-signature <signature.json> --key <key_id>=<base64-public-key> --artifact <archive> --artifact-signature <signature.json>")
	}
	if args[0] == "--offline" {
		return runOffline(args[1:], stdout)
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

type offlineOptions struct {
	indexPath               string
	indexSignaturePath      string
	revocationPath          string
	revocationSignaturePath string
	artifactPaths           []string
	artifactSignaturePaths  []string
	trustedKeys             []plugin.MarketplaceTrustedKey
	now                     time.Time
}

func runOffline(args []string, stdout io.Writer) error {
	options, err := parseOfflineOptions(args)
	if err != nil {
		return err
	}
	input, err := loadOfflineInput(options)
	if err != nil {
		return err
	}
	result, err := plugin.VerifyMarketplaceOffline(input)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "%s: verified signed marketplace index (%d plugins, %d artifacts)\n", options.indexPath, result.Plugins, result.ArtifactsVerified)
	return nil
}

func parseOfflineOptions(args []string) (offlineOptions, error) {
	var options offlineOptions
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch arg {
		case "--index":
			value, err := nextOfflineValue(args, &index, arg)
			if err != nil {
				return offlineOptions{}, err
			}
			options.indexPath = value
		case "--index-signature":
			value, err := nextOfflineValue(args, &index, arg)
			if err != nil {
				return offlineOptions{}, err
			}
			options.indexSignaturePath = value
		case "--revocations":
			value, err := nextOfflineValue(args, &index, arg)
			if err != nil {
				return offlineOptions{}, err
			}
			options.revocationPath = value
		case "--revocations-signature":
			value, err := nextOfflineValue(args, &index, arg)
			if err != nil {
				return offlineOptions{}, err
			}
			options.revocationSignaturePath = value
		case "--artifact":
			value, err := nextOfflineValue(args, &index, arg)
			if err != nil {
				return offlineOptions{}, err
			}
			options.artifactPaths = append(options.artifactPaths, value)
		case "--artifact-signature":
			value, err := nextOfflineValue(args, &index, arg)
			if err != nil {
				return offlineOptions{}, err
			}
			options.artifactSignaturePaths = append(options.artifactSignaturePaths, value)
		case "--key":
			value, err := nextOfflineValue(args, &index, arg)
			if err != nil {
				return offlineOptions{}, err
			}
			key, err := parseTrustedKey(value)
			if err != nil {
				return offlineOptions{}, err
			}
			options.trustedKeys = append(options.trustedKeys, key)
		case "--now":
			value, err := nextOfflineValue(args, &index, arg)
			if err != nil {
				return offlineOptions{}, err
			}
			parsed, err := time.Parse(time.RFC3339, value)
			if err != nil {
				return offlineOptions{}, fmt.Errorf("--now must be RFC3339 UTC time")
			}
			if parsed.Location() != time.UTC {
				return offlineOptions{}, fmt.Errorf("--now must use UTC")
			}
			options.now = parsed
		default:
			return offlineOptions{}, fmt.Errorf("unknown offline option %q", arg)
		}
	}
	if strings.TrimSpace(options.indexPath) == "" {
		return offlineOptions{}, fmt.Errorf("--index is required for offline verification")
	}
	if strings.TrimSpace(options.indexSignaturePath) == "" {
		return offlineOptions{}, fmt.Errorf("--index-signature is required for offline verification")
	}
	if len(options.trustedKeys) == 0 {
		return offlineOptions{}, fmt.Errorf("at least one --key is required for offline verification")
	}
	if len(options.artifactPaths) == 0 {
		return offlineOptions{}, fmt.Errorf("at least one --artifact is required for offline verification")
	}
	if len(options.artifactPaths) != len(options.artifactSignaturePaths) {
		return offlineOptions{}, fmt.Errorf("--artifact and --artifact-signature must be provided in pairs")
	}
	if (options.revocationPath == "") != (options.revocationSignaturePath == "") {
		return offlineOptions{}, fmt.Errorf("--revocations and --revocations-signature must be provided together")
	}
	if options.now.IsZero() {
		return offlineOptions{}, fmt.Errorf("--now is required for offline verification")
	}
	return options, nil
}

func loadOfflineInput(options offlineOptions) (plugin.MarketplaceOfflineVerificationInput, error) {
	indexBytes, err := os.ReadFile(options.indexPath)
	if err != nil {
		return plugin.MarketplaceOfflineVerificationInput{}, fmt.Errorf("%s: %w", options.indexPath, err)
	}
	indexSignatureBytes, err := os.ReadFile(options.indexSignaturePath)
	if err != nil {
		return plugin.MarketplaceOfflineVerificationInput{}, fmt.Errorf("%s: %w", options.indexSignaturePath, err)
	}
	input := plugin.MarketplaceOfflineVerificationInput{
		IndexBytes:          indexBytes,
		IndexSignatureBytes: indexSignatureBytes,
		TrustedKeys:         options.trustedKeys,
		Now:                 options.now,
	}
	for index, artifactPath := range options.artifactPaths {
		artifactBytes, err := os.ReadFile(artifactPath)
		if err != nil {
			return plugin.MarketplaceOfflineVerificationInput{}, fmt.Errorf("%s: %w", artifactPath, err)
		}
		signatureBytes, err := os.ReadFile(options.artifactSignaturePaths[index])
		if err != nil {
			return plugin.MarketplaceOfflineVerificationInput{}, fmt.Errorf("%s: %w", options.artifactSignaturePaths[index], err)
		}
		input.Artifacts = append(input.Artifacts, plugin.MarketplaceOfflineArtifact{
			Data:      artifactBytes,
			Signature: signatureBytes,
		})
	}
	if options.revocationPath != "" {
		input.RevocationBytes, err = os.ReadFile(options.revocationPath)
		if err != nil {
			return plugin.MarketplaceOfflineVerificationInput{}, fmt.Errorf("%s: %w", options.revocationPath, err)
		}
		input.RevocationSignatureBytes, err = os.ReadFile(options.revocationSignaturePath)
		if err != nil {
			return plugin.MarketplaceOfflineVerificationInput{}, fmt.Errorf("%s: %w", options.revocationSignaturePath, err)
		}
	}
	return input, nil
}

func nextOfflineValue(args []string, index *int, flag string) (string, error) {
	*index = *index + 1
	if *index >= len(args) || strings.HasPrefix(args[*index], "--") {
		return "", fmt.Errorf("%s requires a value", flag)
	}
	return args[*index], nil
}

func parseTrustedKey(value string) (plugin.MarketplaceTrustedKey, error) {
	keyID, encoded, ok := strings.Cut(value, "=")
	keyID = strings.TrimSpace(keyID)
	encoded = strings.TrimSpace(encoded)
	if !ok || keyID == "" || encoded == "" {
		return plugin.MarketplaceTrustedKey{}, fmt.Errorf("--key must use key_id=base64-public-key")
	}
	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return plugin.MarketplaceTrustedKey{}, fmt.Errorf("--key %s is not valid base64", keyID)
	}
	return plugin.MarketplaceTrustedKey{KeyID: keyID, PublicKey: key}, nil
}
