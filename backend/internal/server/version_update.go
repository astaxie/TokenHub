package server

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"tokenhub/backend/internal/dbschema"
)

const (
	nativeUpdateTimeout       = 15 * time.Minute
	maxNativeArchiveBytes     = int64(600 * 1024 * 1024)
	maxNativeChecksumBytes    = int64(1024 * 1024)
	maxNativeExtractedBytes   = int64(2 * 1024 * 1024 * 1024)
	maxNativeArchiveFileBytes = int64(600 * 1024 * 1024)
	maxNativeArchiveEntries   = 100000
)

var (
	errNativeOperationUnsupported = errors.New("online updates require a managed release deployment")
	errNativeOperationInProgress  = errors.New("another system update operation is already running")
	errNativeAlreadyUpToDate      = errors.New("the current version is already up to date")
	errNativeRollbackNotAllowed   = errors.New("the requested rollback version is not available")
)

func newNativeDownloadClient() *http.Client {
	return &http.Client{
		Timeout: nativeUpdateTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("too many release asset redirects")
			}
			return validateNativeAssetURL(req.URL.String())
		},
	}
}

func validateNativeAssetURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("parse release asset URL: %w", err)
	}
	if parsed.Scheme != "https" || parsed.User != nil || parsed.Port() != "" {
		return errors.New("release asset URL must use HTTPS without credentials or a custom port")
	}
	switch strings.ToLower(parsed.Hostname()) {
	case "github.com", "objects.githubusercontent.com", "release-assets.githubusercontent.com":
		return nil
	default:
		return fmt.Errorf("release asset host is not trusted: %s", parsed.Hostname())
	}
}

func signalProcessRestart() error {
	process, err := os.FindProcess(os.Getpid())
	if err != nil {
		return err
	}
	return process.Signal(syscall.SIGTERM)
}

func (s *versionService) performNativeUpdate(ctx context.Context) (string, error) {
	releaseOperation, err := s.acquireNativeOperation()
	if err != nil {
		return "", err
	}
	defer releaseOperation()

	release, err := s.fetchLatestRelease(ctx)
	if err != nil {
		return "", err
	}
	targetVersion, target, ok := parseReleaseTag(release.TagName)
	if !ok {
		return "", errors.New("latest GitHub Release does not use a semantic version")
	}
	if s.pendingNativeRestartVersion() == targetVersion {
		return targetVersion, nil
	}
	_, current, currentOK := parseSemanticVersion(s.currentVersion)
	if currentOK && compareSemanticVersions(current, target) >= 0 {
		return "", errNativeAlreadyUpToDate
	}
	if err := s.applyNativeRelease(ctx, release, targetVersion); err != nil {
		return "", err
	}
	return targetVersion, nil
}

func (s *versionService) rollbackNativeRelease(ctx context.Context, requestedVersion string) (string, error) {
	releaseOperation, err := s.acquireNativeOperation()
	if err != nil {
		return "", err
	}
	defer releaseOperation()

	targetVersion, target, ok := parseSemanticVersion(requestedVersion)
	if !ok {
		return "", errNativeRollbackNotAllowed
	}
	_, current, currentOK := parseSemanticVersion(s.currentVersion)
	if !currentOK || compareSemanticVersions(target, current) >= 0 {
		return "", errNativeRollbackNotAllowed
	}
	if s.pendingNativeRestartVersion() == targetVersion {
		return targetVersion, nil
	}
	if s.installedNativeReleaseValid(targetVersion) {
		if err := s.activateNativeRelease(targetVersion); err != nil {
			return "", err
		}
		return targetVersion, nil
	}

	releases, err := s.fetchRecentReleases(ctx, rollbackReleasePageSize)
	if err != nil {
		return "", err
	}
	var selected *githubRelease
	for index := range releases {
		release := &releases[index]
		if release.Draft || release.Prerelease {
			continue
		}
		canonical, _, valid := parseReleaseTag(release.TagName)
		if valid && canonical == targetVersion {
			selected = release
			break
		}
	}
	if selected == nil {
		return "", errNativeRollbackNotAllowed
	}

	if !s.hasNativeReleaseAssets(*selected, targetVersion) {
		return "", errNativeRollbackNotAllowed
	}
	if err := s.applyNativeRelease(ctx, *selected, targetVersion); err != nil {
		return "", err
	}
	return targetVersion, nil
}

func (s *versionService) acquireNativeOperation() (func(), error) {
	if !s.supportsManagedUpdates() {
		return nil, errNativeOperationUnsupported
	}
	select {
	case s.operation <- struct{}{}:
		return func() { <-s.operation }, nil
	default:
		return nil, errNativeOperationInProgress
	}
}

func (s *versionService) hasNativeReleaseAssets(release githubRelease, version string) bool {
	archiveName, err := s.nativeArchiveName(version)
	if err != nil {
		return false
	}
	hasArchive := false
	hasChecksums := false
	for _, asset := range release.Assets {
		switch asset.Name {
		case archiveName:
			hasArchive = asset.BrowserDownloadURL != "" && asset.Size <= maxNativeArchiveBytes
		case "checksums.txt":
			hasChecksums = asset.BrowserDownloadURL != "" && asset.Size <= maxNativeChecksumBytes
		}
	}
	return hasArchive && hasChecksums
}

func (s *versionService) nativeArchiveName(version string) (string, error) {
	if s.runtimeOS != "linux" {
		return "", fmt.Errorf("native release updates are not available for %s", s.runtimeOS)
	}
	switch s.runtimeArch {
	case "amd64", "arm64":
		return fmt.Sprintf("tokenhub_%s_linux_%s.tar.gz", version, s.runtimeArch), nil
	default:
		return "", fmt.Errorf("native release updates are not available for %s/%s", s.runtimeOS, s.runtimeArch)
	}
}

func (s *versionService) applyNativeRelease(ctx context.Context, release githubRelease, version string) error {
	archiveName, err := s.nativeArchiveName(version)
	if err != nil {
		return err
	}
	var archiveAsset githubAsset
	var checksumAsset githubAsset
	for _, asset := range release.Assets {
		switch asset.Name {
		case archiveName:
			archiveAsset = asset
		case "checksums.txt":
			checksumAsset = asset
		}
	}
	if archiveAsset.BrowserDownloadURL == "" || checksumAsset.BrowserDownloadURL == "" {
		return fmt.Errorf("GitHub Release v%s does not contain the native archive and checksums.txt", version)
	}
	if archiveAsset.Size > maxNativeArchiveBytes || checksumAsset.Size > maxNativeChecksumBytes {
		return errors.New("GitHub Release assets exceed the allowed size")
	}
	if err := s.validateAssetURL(archiveAsset.BrowserDownloadURL); err != nil {
		return err
	}
	if err := s.validateAssetURL(checksumAsset.BrowserDownloadURL); err != nil {
		return err
	}

	root, err := s.nativeInstallRoot()
	if err != nil {
		return err
	}
	releasesDir := filepath.Join(root, "releases")
	if err := os.MkdirAll(releasesDir, 0750); err != nil {
		return fmt.Errorf("create native releases directory: %w", err)
	}
	operationDir, err := os.MkdirTemp(root, ".tokenhub-update-")
	if err != nil {
		return fmt.Errorf("create native update directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(operationDir) }()

	archivePath := filepath.Join(operationDir, archiveName)
	if err := s.downloadNativeAsset(ctx, archiveAsset.BrowserDownloadURL, archivePath, maxNativeArchiveBytes); err != nil {
		return fmt.Errorf("download native release archive: %w", err)
	}
	checksums, err := s.downloadNativeBytes(ctx, checksumAsset.BrowserDownloadURL, maxNativeChecksumBytes)
	if err != nil {
		return fmt.Errorf("download native release checksums: %w", err)
	}
	if err := verifyNativeArchiveChecksum(archivePath, archiveName, checksums); err != nil {
		return err
	}

	extracted := filepath.Join(operationDir, "bundle")
	if err := extractNativeArchive(archivePath, extracted); err != nil {
		return fmt.Errorf("extract native release archive: %w", err)
	}
	if err := validateNativeBundle(extracted, version); err != nil {
		return err
	}
	if err := s.installNativeBundle(extracted, version); err != nil {
		return err
	}
	// The managed-upgrade contract in docs/database-evolution.md requires the
	// target release's own binary to run its startup-compatible preparation
	// and verification against the current database, so a database the target
	// cannot serve is never
	// activated. The managed upgrade never runs contracts; the upgrade state
	// records that so the post-restart auto-rollback gate stays closed.
	targetBinary := filepath.Join(root, "releases", version, "bin", "tokenhub")
	if err := s.runTargetDatabasePreflight(ctx, targetBinary, version); err != nil {
		return err
	}
	if err := s.recordUpgrade(s.currentVersion, version); err != nil {
		return err
	}
	return s.activateNativeRelease(version)
}

// runTargetDatabasePreflight runs the freshly installed target release binary
// against the current database before activation. `db prepare` runs the same
// serialized adoption and expand flow as startup, but without publishing a
// serving heartbeat. This lets a target release safely adopt a supported
// pre-ledger database instead of demanding that it be started before
// activation. `db verify` runs afterwards — semantic verification compares
// the live schema against the target's frozen reference, so it can only pass
// once preparation is complete. Expands remain in the database if the upgrade
// is later rolled back (there is no automatic backup restore).
func (s *versionService) runTargetDatabasePreflight(ctx context.Context, binary, version string) error {
	if s.databaseURL == "" {
		return errors.New("database URL is not configured; cannot preflight the target release")
	}
	for _, args := range [][]string{{"db", "prepare"}, {"db", "verify"}} {
		command := exec.CommandContext(ctx, binary, args...)
		command.Env = append(os.Environ(), "TOKENHUB_DATABASE_URL="+s.databaseURL)
		output, err := command.CombinedOutput()
		if err != nil {
			return fmt.Errorf("target v%s preflight `tokenhub %s` failed: %w\n%s", version, strings.Join(args, " "), err, output)
		}
	}
	return nil
}

// upgradeState is the on-disk record of a pending managed upgrade. It exists
// between activation and the first successful boot of the target release and
// drives the one-shot auto-rollback. BootFailed marks that the
// target release's previous boot started but never completed its schema flow.
type upgradeState struct {
	PreviousVersion string `json:"previous_version"`
	TargetVersion   string `json:"target_version"`
	ContractRan     bool   `json:"contract_ran"`
	BootFailed      bool   `json:"boot_failed"`
}

func (s *versionService) upgradeStatePath() (string, error) {
	root, err := s.nativeInstallRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, ".upgrade-state.json"), nil
}

func (s *versionService) recordUpgrade(previousVersion, targetVersion string) error {
	path, err := s.upgradeStatePath()
	if err != nil {
		return err
	}
	raw, err := json.Marshal(upgradeState{PreviousVersion: previousVersion, TargetVersion: targetVersion, ContractRan: false, BootFailed: false})
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o600)
}

func (s *versionService) upgradeState() (upgradeState, bool, error) {
	path, err := s.upgradeStatePath()
	if err != nil {
		return upgradeState{}, false, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return upgradeState{}, false, nil
		}
		return upgradeState{}, false, err
	}
	var state upgradeState
	if err := json.Unmarshal(raw, &state); err != nil {
		return upgradeState{}, false, fmt.Errorf("parse upgrade state: %w", err)
	}
	return state, true, nil
}

// markUpgradeBootStarted flags the in-progress boot of the target release so
// a crash during its schema flow triggers the auto-rollback on the next boot.
func (s *versionService) markUpgradeBootStarted() error {
	path, err := s.upgradeStatePath()
	if err != nil {
		return err
	}
	state, ok, err := s.upgradeState()
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	state.BootFailed = true
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o600)
}

// settleUpgrade removes the upgrade state so a later boot does not attempt
// the auto-rollback a second time (at most one automatic
// re-activation per upgrade).
func (s *versionService) settleUpgrade() error {
	path, err := s.upgradeStatePath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// activeNativeVersion resolves the release the install root currently points
// at, or reports false when the installation is not managed or unreadable.
func (s *versionService) activeNativeVersion() (string, bool) {
	if !s.supportsManagedUpdates() {
		return "", false
	}
	root, err := s.nativeInstallRoot()
	if err != nil {
		return "", false
	}
	activePath, err := filepath.EvalSymlinks(filepath.Join(root, "current"))
	if err != nil {
		return "", false
	}
	versionData, err := os.ReadFile(filepath.Join(activePath, "VERSION"))
	if err != nil {
		return "", false
	}
	version, _, ok := parseSemanticVersion(strings.TrimSpace(string(versionData)))
	return version, ok
}

// startupGuardNewService is a seam for tests to inject a versionService whose
// restart signal does not terminate the test process.
var startupGuardNewService = newVersionService

// RunStartupGuard runs before any database schema flow on a managed native
// install. After a managed upgrade activated a release, the first
// boot marks the boot as in progress; if that boot crashes inside the schema
// flow, the next boot re-activates the previous release once — provided the
// upgrade ran no contract and the previous release's verified compatibility
// record covers the current database state — and signals a restart. At most
// one automatic re-activation happens per upgrade; an incompatible or missing
// previous release leaves the state for operator recovery instead of cycling
// releases. Non-managed installations are a no-op.
func RunStartupGuard(ctx context.Context, config Config) error {
	versions := startupGuardNewService(config)
	if !versions.supportsManagedUpdates() {
		return nil
	}
	state, ok, err := versions.upgradeState()
	if err != nil {
		return fmt.Errorf("read upgrade state: %w", err)
	}
	if !ok {
		return nil
	}
	// The active release is not the target this state recorded (the operator
	// restarted into the previous release, or a newer upgrade superseded it);
	// leave the state in place — the target's first boot has not happened.
	if active, activeOK := versions.activeNativeVersion(); !activeOK || active != state.TargetVersion {
		return nil
	}
	if !state.BootFailed {
		// First boot of the target: mark it in progress so a crash during the
		// schema flow triggers the rollback on the next boot.
		return versions.markUpgradeBootStarted()
	}
	// The target's previous boot failed. Re-activate the previous release at
	// most once: settle before switching so a second failed boot cannot cycle.
	_ = versions.settleUpgrade()
	if state.ContractRan {
		log.Printf("[tokenhub] auto-rollback: upgrade ran a contract; manual recovery required")
		return nil
	}
	if !versions.installedNativeReleaseValid(state.PreviousVersion) {
		log.Printf("[tokenhub] auto-rollback: previous release v%s is not installed; manual recovery required", state.PreviousVersion)
		return nil
	}
	compatible, reason := upgradeRollbackCompatibility(ctx, versions, config, state.PreviousVersion)
	if !compatible {
		log.Printf("[tokenhub] auto-rollback refused: %s", reason)
		return nil
	}
	if err := versions.activateNativeRelease(state.PreviousVersion); err != nil {
		return fmt.Errorf("auto-rollback to v%s failed: %w", state.PreviousVersion, err)
	}
	log.Printf("[tokenhub] auto-rollback to v%s after failed boot; restarting", state.PreviousVersion)
	return versions.restartProcess()
}

// RecordStartupGuardSuccess settles a pending upgrade once the target release
// completed its database schema flow, so a healthy first boot ends the
// auto-rollback protocol.
func RecordStartupGuardSuccess(config Config) error {
	versions := startupGuardNewService(config)
	if !versions.supportsManagedUpdates() {
		return nil
	}
	return versions.settleUpgrade()
}

// upgradeRollbackCompatibility reports whether the previous release can serve
// the current database state, read directly from the ledger before the store
// opens (the guard runs ahead of the schema flow). A dirty ledger or a state
// version outside the release's verified range refuses the rollback.
func upgradeRollbackCompatibility(ctx context.Context, versions *versionService, config Config, previousVersion string) (bool, string) {
	canonical, _, ok := parseSemanticVersion(previousVersion)
	if !ok {
		return false, "previous release is not a semantic version"
	}
	manifest, known := legacyReleaseCompatibility[canonical]
	if !known {
		return false, fmt.Sprintf("release %s carries no verified compatibility record", canonical)
	}
	if config.DatabaseURL == "" {
		return false, "database URL is not configured"
	}
	driver, db, err := OpenRawDatabase(config.DatabaseURL)
	if err != nil {
		return false, fmt.Sprintf("open database for auto-rollback check: %v", err)
	}
	defer db.Close()
	runner, err := dbschema.NewRunner(db, dbschema.Dialect(driver), nil)
	if err != nil {
		return false, fmt.Sprintf("migration runner: %v", err)
	}
	status, err := runner.Status(ctx)
	if err != nil {
		return false, fmt.Sprintf("migration ledger unreadable: %v", err)
	}
	if status.Dirty {
		return false, fmt.Sprintf("database evolution state is dirty at version %d", status.DirtyVersion)
	}
	if status.CurrentVersion > manifest.MaxCompatible || status.CurrentVersion < manifest.MinCompatible {
		return false, fmt.Sprintf("database state version %d is outside release %s compatibility range [%d, %d]",
			status.CurrentVersion, canonical, manifest.MinCompatible, manifest.MaxCompatible)
	}
	return true, ""
}

func (s *versionService) nativeInstallRoot() (string, error) {
	root := filepath.Clean(strings.TrimSpace(s.installRoot))
	if !filepath.IsAbs(root) || root == string(filepath.Separator) {
		return "", errors.New("native install root must be a non-root absolute path")
	}
	info, err := os.Stat(root)
	if err != nil {
		return "", fmt.Errorf("inspect native install root: %w", err)
	}
	if !info.IsDir() {
		return "", errors.New("native install root is not a directory")
	}
	return root, nil
}

func (s *versionService) downloadNativeAsset(ctx context.Context, rawURL string, destination string, maxBytes int64) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	request.Header.Set("User-Agent", "TokenHub/"+s.currentVersion)
	response, err := s.downloadClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("release asset server returned HTTP %d", response.StatusCode)
	}
	if response.ContentLength > maxBytes {
		return fmt.Errorf("release asset exceeds %d bytes", maxBytes)
	}
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	written, copyErr := io.Copy(file, io.LimitReader(response.Body, maxBytes+1))
	closeErr := file.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if written > maxBytes {
		_ = os.Remove(destination)
		return fmt.Errorf("release asset exceeds %d bytes", maxBytes)
	}
	return nil
}

func (s *versionService) downloadNativeBytes(ctx context.Context, rawURL string, maxBytes int64) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "TokenHub/"+s.currentVersion)
	response, err := s.downloadClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("release asset server returned HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("release asset exceeds %d bytes", maxBytes)
	}
	return data, nil
}

func verifyNativeArchiveChecksum(archivePath string, archiveName string, checksumData []byte) error {
	var expected string
	for _, line := range strings.Split(string(checksumData), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		name := strings.TrimPrefix(fields[1], "*")
		if name == archiveName {
			expected = strings.ToLower(fields[0])
			break
		}
	}
	if len(expected) != sha256.Size*2 {
		return fmt.Errorf("checksums.txt does not contain %s", archiveName)
	}
	if _, err := hex.DecodeString(expected); err != nil {
		return fmt.Errorf("checksums.txt contains an invalid SHA-256 for %s", archiveName)
	}
	file, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return err
	}
	actual := hex.EncodeToString(hash.Sum(nil))
	if actual != expected {
		return fmt.Errorf("SHA-256 verification failed for %s", archiveName)
	}
	return nil
}

func extractNativeArchive(archivePath string, destination string) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer file.Close()
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	// Close on a gzip reader only reports a deferred decompression error, and every
	// read below already checks its own error, so there is nothing left to learn here.
	defer func() { _ = gzipReader.Close() }()
	if err := os.Mkdir(destination, 0750); err != nil {
		return err
	}

	reader := tar.NewReader(gzipReader)
	seen := make(map[string]struct{})
	var totalBytes int64
	entryCount := 0
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		name := strings.TrimPrefix(header.Name, "./")
		if name == "" {
			continue
		}
		entryCount++
		if entryCount > maxNativeArchiveEntries {
			return errors.New("archive contains too many entries")
		}
		clean := filepath.Clean(name)
		if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return fmt.Errorf("archive contains unsafe path %q", header.Name)
		}
		if _, exists := seen[clean]; exists {
			return fmt.Errorf("archive contains duplicate path %q", header.Name)
		}
		seen[clean] = struct{}{}
		target := filepath.Join(destination, clean)
		relative, err := filepath.Rel(destination, target)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return fmt.Errorf("archive path escapes destination: %q", header.Name)
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0755); err != nil {
				return err
			}
		// tar.TypeRegA is not listed: since Go 1.11 the reader rewrites that legacy
		// typeflag to TypeReg, or to TypeDir when the name ends in a slash, before the
		// header reaches this switch.
		case tar.TypeReg:
			if header.Size < 0 || header.Size > maxNativeArchiveFileBytes || totalBytes > maxNativeExtractedBytes-header.Size {
				return fmt.Errorf("archive file %q exceeds the extraction limit", header.Name)
			}
			totalBytes += header.Size
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			mode := os.FileMode(0644)
			if header.FileInfo().Mode().Perm()&0111 != 0 {
				mode = 0755
			}
			output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
			if err != nil {
				return err
			}
			written, copyErr := io.CopyN(output, reader, header.Size)
			closeErr := output.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
			if written != header.Size {
				return fmt.Errorf("archive file %q was truncated", header.Name)
			}
		default:
			return fmt.Errorf("archive contains unsupported entry type for %q", header.Name)
		}
	}
	return nil
}

func validateNativeBundle(bundleRoot string, version string) error {
	required := []struct {
		path       string
		executable bool
	}{
		{path: "bin/tokenhub", executable: true},
		{path: "bin/node", executable: true},
		{path: "bin/tokenhub-run", executable: true},
		{path: "frontend/server.js"},
		{path: "catalog/model-catalog.yaml"},
		{path: "catalog/provider-catalog.json"},
		{path: "deploy/tokenhub.service"},
		{path: "VERSION"},
	}
	for _, item := range required {
		path := filepath.Join(bundleRoot, item.path)
		info, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("native release is missing %s: %w", item.path, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("native release path %s is not a regular file", item.path)
		}
		if item.executable && info.Mode().Perm()&0111 == 0 {
			return fmt.Errorf("native release path %s is not executable", item.path)
		}
	}
	versionData, err := os.ReadFile(filepath.Join(bundleRoot, "VERSION"))
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(versionData)) != version {
		return fmt.Errorf("native release VERSION does not match v%s", version)
	}
	return nil
}

func (s *versionService) installNativeBundle(bundleRoot string, version string) error {
	root, err := s.nativeInstallRoot()
	if err != nil {
		return err
	}
	target := filepath.Join(root, "releases", version)
	currentTarget, _ := filepath.EvalSymlinks(filepath.Join(root, "current"))
	if currentTarget != "" && filepath.Clean(currentTarget) == filepath.Clean(target) {
		return errors.New("refusing to replace the active native release directory")
	}

	backup := target + ".replaced"
	_ = os.RemoveAll(backup)
	targetExists := false
	if _, err := os.Stat(target); err == nil {
		targetExists = true
		if err := os.Rename(target, backup); err != nil {
			return fmt.Errorf("move existing native release: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(bundleRoot, target); err != nil {
		if targetExists {
			_ = os.Rename(backup, target)
		}
		return fmt.Errorf("install native release: %w", err)
	}
	_ = os.RemoveAll(backup)
	return nil
}

func (s *versionService) activateNativeRelease(version string) error {
	root, err := s.nativeInstallRoot()
	if err != nil {
		return err
	}
	target := filepath.Join(root, "releases", version)
	if err := validateNativeBundle(target, version); err != nil {
		return err
	}
	current := filepath.Join(root, "current")
	if info, err := os.Lstat(current); err == nil && info.Mode()&os.ModeSymlink == 0 {
		return errors.New("native current path is not a symbolic link")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}

	next := filepath.Join(root, fmt.Sprintf(".current-%d-%d", os.Getpid(), time.Now().UnixNano()))
	if err := os.Symlink(filepath.Join("releases", version), next); err != nil {
		return fmt.Errorf("create native version link: %w", err)
	}
	if err := os.Rename(next, current); err != nil {
		_ = os.Remove(next)
		return fmt.Errorf("activate native release: %w", err)
	}
	if directory, err := os.Open(root); err == nil {
		_ = directory.Sync()
		_ = directory.Close()
	}
	return nil
}

func (s *versionService) installedNativeReleaseValid(version string) bool {
	root, err := s.nativeInstallRoot()
	if err != nil {
		return false
	}
	return validateNativeBundle(filepath.Join(root, "releases", version), version) == nil
}

func (s *versionService) withNativePendingRestart(info systemVersionInfo) systemVersionInfo {
	info.PendingRestart = s.pendingNativeRestartVersion()
	return info
}

func (s *versionService) pendingNativeRestartVersion() string {
	if !s.supportsManagedUpdates() {
		return ""
	}
	root, err := s.nativeInstallRoot()
	if err != nil {
		return ""
	}
	activePath, err := filepath.EvalSymlinks(filepath.Join(root, "current"))
	if err != nil {
		return ""
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return ""
	}
	versionData, err := os.ReadFile(filepath.Join(activePath, "VERSION"))
	if err != nil {
		return ""
	}
	activeVersion, _, activeOK := parseSemanticVersion(strings.TrimSpace(string(versionData)))
	currentVersion, _, currentOK := parseSemanticVersion(s.currentVersion)
	if !activeOK || !currentOK || activeVersion == currentVersion {
		return ""
	}
	if filepath.Clean(activePath) != filepath.Join(resolvedRoot, "releases", activeVersion) {
		return ""
	}
	if validateNativeBundle(activePath, activeVersion) != nil {
		return ""
	}
	return activeVersion
}

func nativeOperationContext(ctx context.Context) (context.Context, context.CancelFunc) {
	base := context.Background()
	if ctx != nil {
		base = context.WithoutCancel(ctx)
	}
	return context.WithTimeout(base, nativeUpdateTimeout)
}

func nativeOperationHTTPError(err error, action string) error {
	switch {
	case errors.Is(err, errNativeOperationUnsupported):
		return NewHTTPError(http.StatusConflict, "managed_update_unavailable", "Online updates are not enabled for this deployment")
	case errors.Is(err, errNativeOperationInProgress):
		return NewHTTPError(http.StatusConflict, "system_operation_in_progress", "Another update, rollback, or restart operation is already running")
	case errors.Is(err, errNativeAlreadyUpToDate):
		return NewHTTPError(http.StatusConflict, "already_up_to_date", "TokenHub is already up to date")
	case errors.Is(err, errNativeRollbackNotAllowed):
		return NewHTTPError(http.StatusBadRequest, "rollback_version_not_available", "The selected rollback version is not available for this installation")
	default:
		log.Printf("[tokenhub] managed %s failed: %v", action, err)
		return NewHTTPError(http.StatusBadGateway, "managed_"+action+"_failed", "Unable to "+action+" the managed TokenHub installation")
	}
}

func (s *Server) recordSystemVersionAudit(
	r *http.Request,
	actor AdminUser,
	action string,
	targetVersion string,
	status string,
	message string,
) {
	targetVersion = normalizeDisplayVersion(targetVersion)
	after := map[string]any{}
	if targetVersion != "" {
		after["target_version"] = targetVersion
	}
	s.recordAdminAuditWithStatus(
		r,
		actor,
		action,
		"system_version",
		targetVersion,
		status,
		message,
		map[string]any{"current_version": s.versions.currentVersion},
		after,
	)
}

func (s *Server) handleAdminSystemUpdate(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireAdmin(w, r, "system", r.Method)
	if !ok {
		return
	}

	ctx, cancel := nativeOperationContext(r.Context())
	defer cancel()
	version, err := s.versions.performNativeUpdate(ctx)
	if err != nil {
		s.recordSystemVersionAudit(r, actor, "update", "", "failed", err.Error())
		writeError(w, r, nativeOperationHTTPError(err, "update"))
		return
	}
	s.recordSystemVersionAudit(r, actor, "update", version, "success", "")
	log.Printf("[tokenhub] managed update to v%s applied by admin %s", version, actor.ID)
	writeJSON(w, http.StatusOK, map[string]any{
		"message":        "Update applied. Restart TokenHub to use the new version.",
		"need_restart":   true,
		"target_version": version,
	})
}

func (s *Server) handleAdminSystemRollback(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireAdmin(w, r, "system", r.Method)
	if !ok {
		return
	}
	var request struct {
		Version string `json:"version"`
	}
	if err := s.decodeJSON(w, r, &request); err != nil {
		requestErr := NewHTTPError(http.StatusBadRequest, "invalid_request", "A rollback version is required")
		if isPayloadTooLarge(err) {
			requestErr = AsHTTPError(err)
		}
		s.recordSystemVersionAudit(r, actor, "rollback", "", "failed", requestErr.Message)
		writeError(w, r, requestErr)
		return
	}

	ctx, cancel := nativeOperationContext(r.Context())
	defer cancel()
	// Refuse before touching releases or the network when the current
	// database state is not one the target release can safely run on.
	if compatibility := s.rollbackCompatibility(ctx, request.Version); compatibility.Compatibility != rollbackCompatible {
		status := http.StatusConflict
		code := "rollback_incompatible"
		if compatibility.Compatibility == rollbackUnknown {
			status = http.StatusUnprocessableEntity
			code = "rollback_compatibility_unknown"
		}
		message := fmt.Sprintf("Rollback to %s refused: %s", compatibility.Release, compatibility.Reason)
		s.recordSystemVersionAudit(r, actor, "rollback", request.Version, "failed", message)
		writeError(w, r, NewHTTPError(status, code, message))
		return
	}
	version, err := s.versions.rollbackNativeRelease(ctx, request.Version)
	if err != nil {
		s.recordSystemVersionAudit(r, actor, "rollback", request.Version, "failed", err.Error())
		writeError(w, r, nativeOperationHTTPError(err, "rollback"))
		return
	}
	s.recordSystemVersionAudit(r, actor, "rollback", version, "success", "")
	log.Printf("[tokenhub] managed rollback to v%s applied by admin %s", version, actor.ID)
	writeJSON(w, http.StatusOK, map[string]any{
		"message":        "Rollback applied. Restart TokenHub to use the selected version.",
		"need_restart":   true,
		"target_version": version,
	})
}

func (s *Server) handleAdminSystemRestart(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireAdmin(w, r, "system", r.Method)
	if !ok {
		return
	}
	if !s.versions.supportsManagedUpdates() {
		s.recordSystemVersionAudit(r, actor, "restart", s.versions.currentVersion, "failed", errNativeOperationUnsupported.Error())
		writeError(w, r, nativeOperationHTTPError(errNativeOperationUnsupported, "restart"))
		return
	}
	select {
	case s.versions.operation <- struct{}{}:
	default:
		s.recordSystemVersionAudit(r, actor, "restart", s.versions.currentVersion, "failed", errNativeOperationInProgress.Error())
		writeError(w, r, nativeOperationHTTPError(errNativeOperationInProgress, "restart"))
		return
	}

	targetVersion := s.versions.pendingNativeRestartVersion()
	if targetVersion == "" {
		targetVersion = s.versions.currentVersion
	}
	auditRequest := r.Clone(context.Background())
	log.Printf("[tokenhub] managed restart requested by admin %s", actor.ID)
	writeJSON(w, http.StatusOK, map[string]any{"message": "TokenHub restart initiated"})
	time.AfterFunc(500*time.Millisecond, func() {
		if err := s.versions.restartProcess(); err != nil {
			<-s.versions.operation
			s.recordSystemVersionAudit(auditRequest, actor, "restart", targetVersion, "failed", err.Error())
			log.Printf("[tokenhub] managed restart signal failed: %v", err)
			return
		}
		s.recordSystemVersionAudit(auditRequest, actor, "restart", targetVersion, "success", "")
	})
}
