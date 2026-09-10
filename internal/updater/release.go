package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

const (
	Repository            = "hackycy/hackycy-cli"
	LatestReleasePath     = "https://api.github.com/repos/" + Repository + "/releases/latest"
	ReleaseDownloadBase   = "https://github.com/" + Repository + "/releases/download"
	ChecksumManifest      = "SHA256SUMS"
	ChecksumReleaseDigest = "release-digest"
	ChecksumManifestFile  = "SHA256SUMS"
)

var versionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$`)
var digestPattern = regexp.MustCompile(`^[a-fA-F0-9]{64}$`)

// ReleaseAsset is the small subset of release metadata used by Upgrade.
type ReleaseAsset struct {
	Name   string
	Digest *string
}

// ReleaseMetadata is the validated latest-release response.
type ReleaseMetadata struct {
	TagName string
	Assets  []ReleaseAsset
}

// Artifact describes one public release executable.
type Artifact struct {
	GOOS   string
	GOARCH string
	Name   string
}

// ReleaseResolution contains all immutable values needed by the candidate slice.
type ReleaseResolution struct {
	CurrentVersion string
	Version        string
	Tag            string
	Artifact       Artifact
	ArtifactURL    string
	ExpectedHash   string
	ChecksumSource string
}

// ReleaseResolverOptions keeps network and platform facts injectable for fixtures.
type ReleaseResolverOptions struct {
	Client            HTTPDoer
	LatestURL         string
	DownloadBaseURL   string
	CurrentVersion    string
	GOOS              string
	GOARCH            string
	AcceptHeaderValue string
}

// HTTPDoer is implemented by *http.Client and local fixture transports.
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// AlreadyCurrentError is returned as a successful no-op by the command layer.
type AlreadyCurrentError struct {
	Current string
	Latest  string
}

func (err *AlreadyCurrentError) Error() string {
	return fmt.Sprintf("already current: %s", err.Current)
}

// HTTPStatusError preserves the observed status for user-facing mapping.
type HTTPStatusError struct {
	URL    string
	Status int
}

func (err *HTTPStatusError) Error() string {
	if err.Status == http.StatusForbidden {
		return "GitHub API rate limit exceeded. Please try again later."
	}
	return fmt.Sprintf("HTTP %d while requesting %s", err.Status, err.URL)
}

// ResolveRelease validates release identity, artifact selection, and the checksum chain.
func ResolveRelease(ctx context.Context, options ReleaseResolverOptions) (ReleaseResolution, error) {
	return resolveRelease(ctx, options, UpgradeObserver{})
}

func resolveRelease(ctx context.Context, options ReleaseResolverOptions, observer UpgradeObserver) (ReleaseResolution, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if options.Client == nil {
		options.Client = http.DefaultClient
	}
	if options.LatestURL == "" {
		options.LatestURL = LatestReleasePath
	}
	if options.DownloadBaseURL == "" {
		options.DownloadBaseURL = ReleaseDownloadBase
	}
	if options.AcceptHeaderValue == "" {
		options.AcceptHeaderValue = "application/vnd.github.v3+json"
	}
	observer.begin(UpgradePhaseResolveRelease)
	artifact, err := ArtifactFor(options.GOOS, options.GOARCH)
	if err != nil {
		observer.end(ctx, UpgradePhaseResolveRelease, err, UpgradePhaseEvent{})
		return ReleaseResolution{}, err
	}
	if strings.TrimSpace(options.CurrentVersion) == "" {
		err := errors.New("current CLI version is required")
		observer.end(ctx, UpgradePhaseResolveRelease, err, UpgradePhaseEvent{})
		return ReleaseResolution{}, err
	}
	if _, err := parseVersion(options.CurrentVersion); err != nil {
		err = fmt.Errorf("current CLI version is invalid: %w", err)
		observer.end(ctx, UpgradePhaseResolveRelease, err, UpgradePhaseEvent{})
		return ReleaseResolution{}, err
	}

	release, err := fetchRelease(ctx, options.Client, options.LatestURL, options.AcceptHeaderValue)
	if err != nil {
		observer.end(ctx, UpgradePhaseResolveRelease, err, UpgradePhaseEvent{})
		return ReleaseResolution{}, err
	}
	version, err := releaseVersion(release.TagName)
	if err != nil {
		observer.end(ctx, UpgradePhaseResolveRelease, err, UpgradePhaseEvent{})
		return ReleaseResolution{}, err
	}
	comparison, err := CompareVersions(options.CurrentVersion, version)
	if err != nil {
		observer.end(ctx, UpgradePhaseResolveRelease, err, UpgradePhaseEvent{})
		return ReleaseResolution{}, err
	}
	base := ReleaseResolution{
		CurrentVersion: options.CurrentVersion,
		Version:        version,
		Tag:            "v" + version,
		Artifact:       artifact,
	}
	observer.complete(UpgradePhaseResolveRelease, UpgradePhaseEvent{
		Detail:             "Release metadata resolved",
		CurrentVersion:     options.CurrentVersion,
		CandidateVersion:   version,
		TargetOS:           artifact.GOOS,
		TargetArchitecture: artifact.GOARCH,
	})
	if comparison >= 0 {
		return base, &AlreadyCurrentError{Current: options.CurrentVersion, Latest: version}
	}

	observer.begin(UpgradePhaseResolveArtifact)
	asset, found, err := findAsset(release.Assets, artifact.Name)
	if err != nil {
		observer.end(ctx, UpgradePhaseResolveArtifact, err, UpgradePhaseEvent{})
		return ReleaseResolution{}, err
	}
	expectedHash := ""
	checksumSource := ""
	if found && asset.Digest != nil && strings.TrimSpace(*asset.Digest) != "" {
		expectedHash, err = normalizeAssetDigest(*asset.Digest)
		if err != nil {
			observer.end(ctx, UpgradePhaseResolveArtifact, err, UpgradePhaseEvent{})
			return ReleaseResolution{}, err
		}
		checksumSource = ChecksumReleaseDigest
	}
	if expectedHash == "" {
		manifestURL := strings.TrimRight(options.DownloadBaseURL, "/") + "/" + base.Tag + "/" + ChecksumManifest
		expectedHash, err = fetchChecksum(ctx, options.Client, manifestURL, artifact.Name)
		if err != nil {
			observer.end(ctx, UpgradePhaseResolveArtifact, err, UpgradePhaseEvent{})
			return ReleaseResolution{}, err
		}
		checksumSource = ChecksumManifestFile
	}
	base.ArtifactURL = strings.TrimRight(options.DownloadBaseURL, "/") + "/" + base.Tag + "/" + artifact.Name
	base.ExpectedHash = expectedHash
	base.ChecksumSource = checksumSource
	observer.complete(UpgradePhaseResolveArtifact, UpgradePhaseEvent{
		Detail:             "Artifact and checksum resolved",
		CandidateVersion:   base.Version,
		TargetOS:           artifact.GOOS,
		TargetArchitecture: artifact.GOARCH,
		ArtifactName:       artifact.Name,
		ChecksumSource:     checksumSource,
	})
	return base, nil
}

// ArtifactFor maps Go's target vocabulary to the public six-artifact vocabulary.
func ArtifactFor(goos, goarch string) (Artifact, error) {
	osName := map[string]string{"darwin": "macos", "linux": "linux", "windows": "windows"}[goos]
	if osName == "" {
		return Artifact{}, fmt.Errorf("unsupported platform: %s", goos)
	}
	var archName string
	switch goarch {
	case "amd64", "x64":
		archName = "x64"
	case "arm64":
		archName = "arm64"
	default:
		return Artifact{}, fmt.Errorf("unsupported architecture: %s", goarch)
	}
	name := "ycy-" + osName + "-" + archName
	if goos == "windows" {
		name += ".exe"
	}
	return Artifact{GOOS: goos, GOARCH: goarch, Name: name}, nil
}

// CompareVersions implements semantic-version precedence, ignoring build metadata.
func CompareVersions(left, right string) (int, error) {
	a, err := parseVersion(left)
	if err != nil {
		return 0, fmt.Errorf("invalid version %q: %w", left, err)
	}
	b, err := parseVersion(right)
	if err != nil {
		return 0, fmt.Errorf("invalid version %q: %w", right, err)
	}
	for index, value := range []uint64{a.major, a.minor, a.patch} {
		other := []uint64{b.major, b.minor, b.patch}[index]
		if value < other {
			return -1, nil
		}
		if value > other {
			return 1, nil
		}
	}
	if len(a.pre) == 0 && len(b.pre) == 0 {
		return 0, nil
	}
	if len(a.pre) == 0 {
		return 1, nil
	}
	if len(b.pre) == 0 {
		return -1, nil
	}
	for index := 0; index < len(a.pre) && index < len(b.pre); index++ {
		leftID, rightID := a.pre[index], b.pre[index]
		if leftID.numeric && rightID.numeric {
			if leftID.number < rightID.number {
				return -1, nil
			}
			if leftID.number > rightID.number {
				return 1, nil
			}
			continue
		}
		if leftID.numeric != rightID.numeric {
			if leftID.numeric {
				return -1, nil
			}
			return 1, nil
		}
		if leftID.text < rightID.text {
			return -1, nil
		}
		if leftID.text > rightID.text {
			return 1, nil
		}
	}
	if len(a.pre) < len(b.pre) {
		return -1, nil
	}
	if len(a.pre) > len(b.pre) {
		return 1, nil
	}
	return 0, nil
}

// ParseChecksumManifest parses the accepted GNU/plain checksum forms.
func ParseChecksumManifest(content string) (map[string]string, error) {
	checksums := make(map[string]string)
	for lineNumber, line := range strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		fields := strings.Fields(trimmed)
		if len(fields) != 2 {
			return nil, fmt.Errorf("invalid checksum manifest line %d", lineNumber+1)
		}
		name := strings.TrimPrefix(fields[1], "*")
		if !digestPattern.MatchString(fields[0]) || name == "" {
			return nil, fmt.Errorf("invalid checksum manifest line %d", lineNumber+1)
		}
		if _, exists := checksums[name]; exists {
			return nil, fmt.Errorf("duplicate checksum entry for %s", name)
		}
		checksums[name] = strings.ToLower(fields[0])
	}
	return checksums, nil
}

func fetchRelease(ctx context.Context, client HTTPDoer, endpoint, accept string) (ReleaseMetadata, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return ReleaseMetadata{}, fmt.Errorf("create release request: %w", err)
	}
	request.Header.Set("Accept", accept)
	response, err := client.Do(request)
	if err != nil {
		return ReleaseMetadata{}, fmt.Errorf("check for updates: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return ReleaseMetadata{}, &HTTPStatusError{URL: endpoint, Status: response.StatusCode}
	}
	var payload struct {
		TagName *string `json:"tag_name"`
		Assets  []struct {
			Name   *string `json:"name"`
			Digest *string `json:"digest"`
		} `json:"assets"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 4<<20))
	if err := decoder.Decode(&payload); err != nil {
		return ReleaseMetadata{}, fmt.Errorf("decode release metadata: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return ReleaseMetadata{}, errors.New("release metadata contains trailing JSON")
		}
		return ReleaseMetadata{}, fmt.Errorf("decode release metadata trailer: %w", err)
	}
	if payload.TagName == nil || *payload.TagName == "" {
		return ReleaseMetadata{}, errors.New("release metadata has no tag_name")
	}
	assets := make([]ReleaseAsset, 0, len(payload.Assets))
	for index, asset := range payload.Assets {
		if asset.Name == nil || *asset.Name == "" {
			return ReleaseMetadata{}, fmt.Errorf("release asset %d has no name", index)
		}
		assets = append(assets, ReleaseAsset{Name: *asset.Name, Digest: asset.Digest})
	}
	return ReleaseMetadata{TagName: *payload.TagName, Assets: assets}, nil
}

func releaseVersion(tag string) (string, error) {
	if !strings.HasPrefix(tag, "v") || strings.HasPrefix(tag, "vv") {
		return "", fmt.Errorf("release tag %q is not a v-prefixed semantic version", tag)
	}
	version := strings.TrimPrefix(tag, "v")
	if _, err := parseVersion(version); err != nil {
		return "", fmt.Errorf("release tag %q is invalid: %w", tag, err)
	}
	return version, nil
}

func findAsset(assets []ReleaseAsset, name string) (ReleaseAsset, bool, error) {
	var found ReleaseAsset
	foundCount := 0
	for _, asset := range assets {
		if asset.Name == name {
			found = asset
			foundCount++
		}
	}
	if foundCount > 1 {
		return ReleaseAsset{}, false, fmt.Errorf("release contains duplicate asset %s", name)
	}
	return found, foundCount == 1, nil
}

func normalizeAssetDigest(value string) (string, error) {
	digest := strings.TrimSpace(value)
	if !strings.HasPrefix(strings.ToLower(digest), "sha256:") {
		return "", fmt.Errorf("asset digest is not a SHA-256 digest")
	}
	digest = digest[len("sha256:"):]
	if !digestPattern.MatchString(digest) {
		return "", errors.New("asset digest is malformed")
	}
	return strings.ToLower(digest), nil
}

func fetchChecksum(ctx context.Context, client HTTPDoer, endpoint, artifact string) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", fmt.Errorf("create checksum request: %w", err)
	}
	response, err := client.Do(request)
	if err != nil {
		return "", fmt.Errorf("download checksums: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", &HTTPStatusError{URL: endpoint, Status: response.StatusCode}
	}
	contents, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return "", fmt.Errorf("read checksums: %w", err)
	}
	checksums, err := ParseChecksumManifest(string(contents))
	if err != nil {
		return "", err
	}
	hash, ok := checksums[artifact]
	if !ok {
		return "", fmt.Errorf("missing checksum for %s", artifact)
	}
	return hash, nil
}

type semanticVersion struct {
	major, minor, patch uint64
	pre                 []versionIdentifier
}

type versionIdentifier struct {
	numeric bool
	number  uint64
	text    string
}

func parseVersion(value string) (semanticVersion, error) {
	matches := versionPattern.FindStringSubmatch(value)
	if matches == nil {
		return semanticVersion{}, errors.New("expected MAJOR.MINOR.PATCH with optional prerelease/build metadata")
	}
	major, err := strconv.ParseUint(matches[1], 10, 64)
	if err != nil {
		return semanticVersion{}, errors.New("major version is too large")
	}
	minor, err := strconv.ParseUint(matches[2], 10, 64)
	if err != nil {
		return semanticVersion{}, errors.New("minor version is too large")
	}
	patch, err := strconv.ParseUint(matches[3], 10, 64)
	if err != nil {
		return semanticVersion{}, errors.New("patch version is too large")
	}
	result := semanticVersion{major: major, minor: minor, patch: patch}
	if matches[4] != "" {
		for _, part := range strings.Split(matches[4], ".") {
			identifier := versionIdentifier{text: part}
			if isNumericIdentifier(part) {
				identifier.numeric = true
				identifier.number, err = strconv.ParseUint(part, 10, 64)
				if err != nil {
					return semanticVersion{}, errors.New("numeric prerelease identifier is too large")
				}
			}
			result.pre = append(result.pre, identifier)
		}
	}
	return result, nil
}

func isNumericIdentifier(value string) bool {
	if value == "" || (len(value) > 1 && value[0] == '0') {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func sha256Bytes(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}
