package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	DefaultAppVersion = "0.6.0"

	defaultBuildType         = "source"
	releaseBuildType         = "release"
	sourceDeploymentType     = "source"
	containerDeploymentType  = "container"
	nativeDeploymentType     = "native"
	defaultNativeInstallRoot = "/opt/tokenhub"
	defaultReleaseRepository = "astaxie/TokenHub"
	githubAPIBaseURL         = "https://api.github.com"
	versionCacheTTL          = 20 * time.Minute
	releaseRequestTimeout    = 8 * time.Second
	maxReleaseResponseBytes  = 2 * 1024 * 1024
	maxRollbackVersions      = 3
	rollbackReleasePageSize  = 15
)

var errNoGitHubReleases = errors.New("repository has no GitHub releases")

type versionReleaseInfo struct {
	Version     string `json:"version"`
	Name        string `json:"name"`
	PublishedAt string `json:"published_at"`
	HTMLURL     string `json:"html_url"`
}

type systemVersionInfo struct {
	CurrentVersion  string              `json:"current_version"`
	LatestVersion   string              `json:"latest_version"`
	HasUpdate       bool                `json:"has_update"`
	BuildType       string              `json:"build_type"`
	DeploymentType  string              `json:"deployment_type"`
	UpdateSupported bool                `json:"update_supported"`
	PendingRestart  string              `json:"pending_restart_version,omitempty"`
	ReleasesURL     string              `json:"releases_url"`
	ReleaseInfo     *versionReleaseInfo `json:"release_info,omitempty"`
	Cached          bool                `json:"cached"`
	Warning         string              `json:"warning,omitempty"`
}

type rollbackVersionInfo struct {
	Version     string `json:"version"`
	PublishedAt string `json:"published_at"`
	HTMLURL     string `json:"html_url"`
	// Compatibility marks whether the current database state allows
	// activating this release: compatible, incompatible, or unknown.
	Compatibility             string         `json:"compatibility,omitempty"`
	CompatibilityReason       string         `json:"compatibility_reason,omitempty"`
	CompatibilityReasonCode   string         `json:"compatibility_reason_code,omitempty"`
	CompatibilityReasonParams map[string]any `json:"compatibility_reason_params,omitempty"`
}

type githubRelease struct {
	TagName     string        `json:"tag_name"`
	Name        string        `json:"name"`
	PublishedAt string        `json:"published_at"`
	Draft       bool          `json:"draft"`
	Prerelease  bool          `json:"prerelease"`
	Assets      []githubAsset `json:"assets"`
}

type githubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

type latestVersionCall struct {
	done chan struct{}
	info systemVersionInfo
}

type rollbackVersionsCall struct {
	done     chan struct{}
	versions []rollbackVersionInfo
	err      error
}

type versionService struct {
	client           *http.Client
	apiBaseURL       string
	repository       string
	currentVersion   string
	buildType        string
	deploymentType   string
	managedUpdates   bool
	installRoot      string
	databaseURL      string
	now              func() time.Time
	downloadClient   *http.Client
	validateAssetURL func(string) error
	operation        chan struct{}
	restartProcess   func() error
	runtimeOS        string
	runtimeArch      string

	mu                  sync.Mutex
	latestCache         *systemVersionInfo
	latestCacheExpires  time.Time
	latestInFlight      *latestVersionCall
	rollbackCache       []rollbackVersionInfo
	rollbackCacheExpiry time.Time
	rollbackInFlight    *rollbackVersionsCall
}

func newVersionService(config Config) *versionService {
	currentVersion := normalizeDisplayVersion(config.AppVersion)
	if currentVersion == "" {
		currentVersion = DefaultAppVersion
	}
	buildType := strings.ToLower(strings.TrimSpace(config.BuildType))
	if buildType != releaseBuildType {
		buildType = defaultBuildType
	}
	deploymentType := normalizeDeploymentType(config.DeploymentType, buildType)
	repository := strings.TrimSpace(config.ReleaseRepository)
	if !validReleaseRepository(repository) {
		repository = defaultReleaseRepository
	}
	return &versionService{
		client:           &http.Client{Timeout: releaseRequestTimeout},
		apiBaseURL:       githubAPIBaseURL,
		repository:       repository,
		currentVersion:   currentVersion,
		buildType:        buildType,
		deploymentType:   deploymentType,
		managedUpdates:   config.ManagedUpdates,
		installRoot:      filepath.Clean(strings.TrimSpace(config.InstallRoot)),
		databaseURL:      strings.TrimSpace(config.DatabaseURL),
		now:              time.Now,
		operation:        make(chan struct{}, 1),
		downloadClient:   newNativeDownloadClient(),
		validateAssetURL: validateNativeAssetURL,
		restartProcess:   signalProcessRestart,
		runtimeOS:        runtime.GOOS,
		runtimeArch:      runtime.GOARCH,
	}
}

func (s *versionService) checkUpdate(ctx context.Context, force bool) systemVersionInfo {
	cached, call, leader := s.beginLatestCheck(force)
	if cached != nil {
		return s.withNativePendingRestart(*cached)
	}
	if leader {
		go func() {
			lookupCtx, cancel := context.WithTimeout(context.Background(), releaseRequestTimeout)
			defer cancel()
			s.finishLatestCheck(call, s.checkUpdateRemote(lookupCtx))
		}()
	}

	select {
	case <-call.done:
		return s.withNativePendingRestart(cloneSystemVersionInfo(call.info))
	case <-ctx.Done():
		info := s.baseVersionInfo()
		info.Warning = "GitHub release lookup canceled"
		return info
	}
}

func (s *versionService) checkUpdateRemote(ctx context.Context) systemVersionInfo {
	release, err := s.fetchLatestRelease(ctx)
	if err != nil {
		if errors.Is(err, errNoGitHubReleases) {
			info := s.baseVersionInfo()
			s.storeLatest(info)
			return info
		}
		if cached, ok := s.cachedLatest(true); ok {
			cached.Warning = "GitHub release lookup failed; showing cached data"
			return s.withNativePendingRestart(cached)
		}
		info := s.baseVersionInfo()
		info.Warning = "GitHub release lookup failed"
		return info
	}

	latestVersion, latest, ok := parseReleaseTag(release.TagName)
	if !ok {
		info := s.baseVersionInfo()
		info.Warning = "Latest GitHub release does not use a semantic version"
		return info
	}

	info := s.baseVersionInfo()
	info.LatestVersion = latestVersion
	info.ReleaseInfo = &versionReleaseInfo{
		Version:     latestVersion,
		Name:        strings.TrimSpace(release.Name),
		PublishedAt: strings.TrimSpace(release.PublishedAt),
		HTMLURL:     s.releaseTagURL(latestVersion),
	}
	_, current, currentOK := parseSemanticVersion(s.currentVersion)
	if currentOK {
		info.HasUpdate = compareSemanticVersions(current, latest) < 0
	} else {
		info.Warning = "Current build does not use a semantic release version"
	}
	if info.HasUpdate && s.supportsManagedUpdates() && !s.hasNativeReleaseAssets(release, latestVersion) {
		info.Warning = "Latest GitHub release does not include managed release assets for this platform"
	}
	s.storeLatest(info)
	return info
}

func (s *versionService) listRollbackVersions(ctx context.Context) ([]rollbackVersionInfo, error) {
	cached, call, leader := s.beginRollbackCheck()
	if cached != nil {
		return cached, nil
	}
	if leader {
		go func() {
			lookupCtx, cancel := context.WithTimeout(context.Background(), releaseRequestTimeout)
			defer cancel()
			versions, err := s.listRollbackVersionsRemote(lookupCtx)
			s.finishRollbackCheck(call, versions, err)
		}()
	}

	select {
	case <-call.done:
		return append([]rollbackVersionInfo{}, call.versions...), call.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *versionService) listRollbackVersionsRemote(ctx context.Context) ([]rollbackVersionInfo, error) {
	_, current, currentOK := parseSemanticVersion(s.currentVersion)
	if !currentOK {
		versions := []rollbackVersionInfo{}
		s.storeRollbacks(versions)
		return versions, nil
	}

	releases, err := s.fetchRecentReleases(ctx, rollbackReleasePageSize)
	if err != nil {
		if versions := s.installedRollbackVersions(current); len(versions) > 0 {
			s.storeRollbacks(versions)
			return versions, nil
		}
		if errors.Is(err, errNoGitHubReleases) {
			versions := []rollbackVersionInfo{}
			s.storeRollbacks(versions)
			return versions, nil
		}
		return nil, err
	}

	type candidate struct {
		version   semanticVersion
		canonical string
		release   githubRelease
	}
	candidates := make([]candidate, 0, len(releases))
	seen := make(map[string]struct{}, len(releases))
	for _, release := range releases {
		if release.Draft || release.Prerelease {
			continue
		}
		canonical, parsed, ok := parseReleaseTag(release.TagName)
		if !ok {
			continue
		}
		if _, exists := seen[canonical]; exists {
			continue
		}
		if compareSemanticVersions(parsed, current) >= 0 {
			continue
		}
		if s.supportsManagedUpdates() &&
			!s.installedNativeReleaseValid(canonical) &&
			!s.hasNativeReleaseAssets(release, canonical) {
			continue
		}
		seen[canonical] = struct{}{}
		candidates = append(candidates, candidate{
			version:   parsed,
			canonical: canonical,
			release:   release,
		})
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return compareSemanticVersions(candidates[i].version, candidates[j].version) > 0
	})
	if len(candidates) > maxRollbackVersions {
		candidates = candidates[:maxRollbackVersions]
	}

	versions := make([]rollbackVersionInfo, 0, len(candidates))
	for _, candidate := range candidates {
		versions = append(versions, rollbackVersionInfo{
			Version:     candidate.canonical,
			PublishedAt: strings.TrimSpace(candidate.release.PublishedAt),
			HTMLURL:     s.releaseTagURL(candidate.canonical),
		})
	}
	s.storeRollbacks(versions)
	return versions, nil
}

func (s *versionService) installedRollbackVersions(current semanticVersion) []rollbackVersionInfo {
	if !s.supportsManagedUpdates() {
		return nil
	}
	root, err := s.nativeInstallRoot()
	if err != nil {
		return nil
	}
	entries, err := os.ReadDir(filepath.Join(root, "releases"))
	if err != nil {
		return nil
	}
	type candidate struct {
		version   semanticVersion
		canonical string
	}
	candidates := make([]candidate, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		canonical, parsed, ok := parseSemanticVersion(entry.Name())
		if !ok || canonical != entry.Name() || compareSemanticVersions(parsed, current) >= 0 {
			continue
		}
		if !s.installedNativeReleaseValid(canonical) {
			continue
		}
		candidates = append(candidates, candidate{version: parsed, canonical: canonical})
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return compareSemanticVersions(candidates[i].version, candidates[j].version) > 0
	})
	if len(candidates) > maxRollbackVersions {
		candidates = candidates[:maxRollbackVersions]
	}
	versions := make([]rollbackVersionInfo, 0, len(candidates))
	for _, candidate := range candidates {
		versions = append(versions, rollbackVersionInfo{
			Version: candidate.canonical,
			HTMLURL: s.releaseTagURL(candidate.canonical),
		})
	}
	return versions
}

func (s *versionService) baseVersionInfo() systemVersionInfo {
	return s.withNativePendingRestart(systemVersionInfo{
		CurrentVersion:  s.currentVersion,
		LatestVersion:   s.currentVersion,
		BuildType:       s.buildType,
		DeploymentType:  s.deploymentType,
		UpdateSupported: s.supportsManagedUpdates(),
		ReleasesURL:     s.releasesURL(),
	})
}

func (s *versionService) supportsManagedUpdates() bool {
	return s.deploymentType == nativeDeploymentType ||
		(s.deploymentType == containerDeploymentType && s.managedUpdates)
}

func (s *versionService) fetchLatestRelease(ctx context.Context) (githubRelease, error) {
	var release githubRelease
	path := fmt.Sprintf("/repos/%s/releases/latest", s.repository)
	if err := s.getJSON(ctx, path, &release); err != nil {
		return githubRelease{}, err
	}
	return release, nil
}

func (s *versionService) fetchRecentReleases(ctx context.Context, perPage int) ([]githubRelease, error) {
	var releases []githubRelease
	path := fmt.Sprintf("/repos/%s/releases?per_page=%d", s.repository, perPage)
	if err := s.getJSON(ctx, path, &releases); err != nil {
		return nil, err
	}
	return releases, nil
}

func (s *versionService) getJSON(ctx context.Context, path string, target any) error {
	endpoint := strings.TrimRight(s.apiBaseURL, "/") + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "TokenHub/"+s.currentVersion)

	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return errNoGitHubReleases
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("GitHub release API returned HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxReleaseResponseBytes+1))
	if err != nil {
		return fmt.Errorf("read GitHub release response: %w", err)
	}
	if len(data) > maxReleaseResponseBytes {
		return fmt.Errorf("GitHub release response exceeds %d bytes", maxReleaseResponseBytes)
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("decode GitHub release response: %w", err)
	}
	return nil
}

func (s *versionService) beginLatestCheck(force bool) (*systemVersionInfo, *latestVersionCall, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !force && s.latestCache != nil && s.now().Before(s.latestCacheExpires) {
		cached := cloneSystemVersionInfo(*s.latestCache)
		cached.Cached = true
		return &cached, nil, false
	}
	if s.latestInFlight != nil {
		return nil, s.latestInFlight, false
	}
	call := &latestVersionCall{done: make(chan struct{})}
	s.latestInFlight = call
	return nil, call, true
}

func (s *versionService) finishLatestCheck(call *latestVersionCall, info systemVersionInfo) {
	s.mu.Lock()
	defer s.mu.Unlock()
	call.info = cloneSystemVersionInfo(info)
	if s.latestInFlight == call {
		s.latestInFlight = nil
	}
	close(call.done)
}

func (s *versionService) beginRollbackCheck() ([]rollbackVersionInfo, *rollbackVersionsCall, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.rollbackCache != nil && s.now().Before(s.rollbackCacheExpiry) {
		return append([]rollbackVersionInfo{}, s.rollbackCache...), nil, false
	}
	if s.rollbackInFlight != nil {
		return nil, s.rollbackInFlight, false
	}
	call := &rollbackVersionsCall{done: make(chan struct{})}
	s.rollbackInFlight = call
	return nil, call, true
}

func (s *versionService) finishRollbackCheck(call *rollbackVersionsCall, versions []rollbackVersionInfo, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	call.versions = append([]rollbackVersionInfo{}, versions...)
	call.err = err
	if s.rollbackInFlight == call {
		s.rollbackInFlight = nil
	}
	close(call.done)
}

func (s *versionService) cachedLatest(allowExpired bool) (systemVersionInfo, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.latestCache == nil {
		return systemVersionInfo{}, false
	}
	if !allowExpired && !s.now().Before(s.latestCacheExpires) {
		return systemVersionInfo{}, false
	}
	cached := cloneSystemVersionInfo(*s.latestCache)
	cached.Cached = true
	return cached, true
}

func (s *versionService) storeLatest(info systemVersionInfo) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cached := cloneSystemVersionInfo(info)
	s.latestCache = &cached
	s.latestCacheExpires = s.now().Add(versionCacheTTL)
}

func (s *versionService) storeRollbacks(versions []rollbackVersionInfo) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rollbackCache = append([]rollbackVersionInfo{}, versions...)
	s.rollbackCacheExpiry = s.now().Add(versionCacheTTL)
}

func cloneSystemVersionInfo(info systemVersionInfo) systemVersionInfo {
	if info.ReleaseInfo != nil {
		release := *info.ReleaseInfo
		info.ReleaseInfo = &release
	}
	return info
}

func normalizeDisplayVersion(raw string) string {
	return strings.TrimPrefix(strings.TrimSpace(raw), "v")
}

func normalizeDeploymentType(raw string, buildType string) string {
	if strings.ToLower(strings.TrimSpace(buildType)) != releaseBuildType {
		return sourceDeploymentType
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case nativeDeploymentType:
		return nativeDeploymentType
	case containerDeploymentType:
		return containerDeploymentType
	default:
		return containerDeploymentType
	}
}

func validReleaseRepository(repository string) bool {
	parts := strings.Split(strings.TrimSpace(repository), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return false
	}
	for index, part := range parts {
		for _, char := range part {
			valid := (char >= '0' && char <= '9') ||
				(char >= 'A' && char <= 'Z') ||
				(char >= 'a' && char <= 'z') ||
				char == '-'
			if index == 1 {
				valid = valid || char == '_' || char == '.'
			}
			if !valid {
				return false
			}
		}
	}
	return true
}

func (s *versionService) releasesURL() string {
	return "https://github.com/" + s.repository + "/releases"
}

func (s *versionService) releaseTagURL(version string) string {
	return s.releasesURL() + "/tag/v" + url.PathEscape(version)
}

type semanticVersion struct {
	major      uint64
	minor      uint64
	patch      uint64
	prerelease []string
}

func parseSemanticVersion(raw string) (string, semanticVersion, bool) {
	value := normalizeDisplayVersion(raw)
	if value == "" {
		return "", semanticVersion{}, false
	}
	versionAndBuild := strings.SplitN(value, "+", 2)
	withoutBuild := versionAndBuild[0]
	if len(versionAndBuild) == 2 {
		buildIdentifiers := strings.Split(versionAndBuild[1], ".")
		for _, identifier := range buildIdentifiers {
			if !validSemanticIdentifier(identifier) {
				return "", semanticVersion{}, false
			}
		}
	}
	versionAndPrerelease := strings.SplitN(withoutBuild, "-", 2)
	core := strings.Split(versionAndPrerelease[0], ".")
	if len(core) != 3 {
		return "", semanticVersion{}, false
	}
	numbers := [3]uint64{}
	for index, part := range core {
		if part == "" || (len(part) > 1 && part[0] == '0') {
			return "", semanticVersion{}, false
		}
		number, err := strconv.ParseUint(part, 10, 64)
		if err != nil {
			return "", semanticVersion{}, false
		}
		numbers[index] = number
	}

	parsed := semanticVersion{major: numbers[0], minor: numbers[1], patch: numbers[2]}
	canonical := fmt.Sprintf("%d.%d.%d", parsed.major, parsed.minor, parsed.patch)
	if len(versionAndPrerelease) == 2 {
		if versionAndPrerelease[1] == "" {
			return "", semanticVersion{}, false
		}
		identifiers := strings.Split(versionAndPrerelease[1], ".")
		for _, identifier := range identifiers {
			if !validPrereleaseIdentifier(identifier) {
				return "", semanticVersion{}, false
			}
		}
		parsed.prerelease = identifiers
		canonical += "-" + strings.Join(identifiers, ".")
	}
	return canonical, parsed, true
}

func parseReleaseTag(raw string) (string, semanticVersion, bool) {
	value := strings.TrimSpace(raw)
	if !strings.HasPrefix(value, "v") || strings.Contains(value, "+") {
		return "", semanticVersion{}, false
	}
	return parseSemanticVersion(value)
}

func validPrereleaseIdentifier(value string) bool {
	if !validSemanticIdentifier(value) {
		return false
	}
	numeric := true
	for _, char := range value {
		if char < '0' || char > '9' {
			numeric = false
		}
	}
	return !numeric || len(value) == 1 || value[0] != '0'
}

func validSemanticIdentifier(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if (char < '0' || char > '9') &&
			(char < 'A' || char > 'Z') &&
			(char < 'a' || char > 'z') &&
			char != '-' {
			return false
		}
	}
	return true
}

func compareSemanticVersions(left, right semanticVersion) int {
	for _, pair := range [][2]uint64{
		{left.major, right.major},
		{left.minor, right.minor},
		{left.patch, right.patch},
	} {
		if pair[0] < pair[1] {
			return -1
		}
		if pair[0] > pair[1] {
			return 1
		}
	}
	if len(left.prerelease) == 0 && len(right.prerelease) == 0 {
		return 0
	}
	if len(left.prerelease) == 0 {
		return 1
	}
	if len(right.prerelease) == 0 {
		return -1
	}
	for index := 0; index < len(left.prerelease) && index < len(right.prerelease); index++ {
		leftPart := left.prerelease[index]
		rightPart := right.prerelease[index]
		leftNumber, leftNumeric := parseNumericIdentifier(leftPart)
		rightNumber, rightNumeric := parseNumericIdentifier(rightPart)
		switch {
		case leftNumeric && rightNumeric:
			if leftNumber < rightNumber {
				return -1
			}
			if leftNumber > rightNumber {
				return 1
			}
		case leftNumeric:
			return -1
		case rightNumeric:
			return 1
		default:
			if leftPart < rightPart {
				return -1
			}
			if leftPart > rightPart {
				return 1
			}
		}
	}
	if len(left.prerelease) < len(right.prerelease) {
		return -1
	}
	if len(left.prerelease) > len(right.prerelease) {
		return 1
	}
	return 0
}

func parseNumericIdentifier(value string) (uint64, bool) {
	for _, char := range value {
		if char < '0' || char > '9' {
			return 0, false
		}
	}
	number, err := strconv.ParseUint(value, 10, 64)
	return number, err == nil
}

func (s *Server) handleAdminSystemVersion(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r, "system", r.Method); !ok {
		return
	}
	force := r.URL.Query().Get("force")
	info := s.versions.checkUpdate(r.Context(), force == "1" || strings.EqualFold(force, "true"))
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) handleAdminRollbackVersions(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r, "system", r.Method); !ok {
		return
	}
	versions, err := s.versions.listRollbackVersions(r.Context())
	if err != nil {
		writeError(w, r, NewHTTPError(http.StatusBadGateway, "release_lookup_failed", "Unable to load release history"))
		return
	}
	s.annotateRollbackCompatibility(r.Context(), versions)
	writeJSON(w, http.StatusOK, map[string]any{"versions": versions})
}
