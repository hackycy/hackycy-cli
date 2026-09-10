// Package zip owns the interactive archive command.
package zip

import (
	"encoding/json"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

var (
	viteConfigFiles = []string{
		"vite.config.ts",
		"vite.config.js",
		"vite.config.mts",
		"vite.config.mjs",
		"vite.config.cts",
		"vite.config.cjs",
	}
	webpackConfigFiles = []string{
		"webpack.config.ts",
		"webpack.config.js",
		"webpack.config.mts",
		"webpack.config.mjs",
		"webpack.config.cts",
		"webpack.config.cjs",
	}
	uniappSignalFiles = []string{
		"pages.json",
		"src/pages.json",
		"manifest.json",
		"src/manifest.json",
		"uni.config.ts",
		"uni.config.js",
	}
	pnpmWorkspacePattern = regexp.MustCompile(`^\s*-\s*['\"]?([^'\"]+)['\"]?\s*$`)
	sshRemotePattern     = regexp.MustCompile(`^[^@]+@[^:]+:(.+)$`)
)

var scanIgnoredDirectories = map[string]struct{}{
	".git":         {},
	".hg":          {},
	".idea":        {},
	".nx":          {},
	".svn":         {},
	".turbo":       {},
	".vscode":      {},
	"coverage":     {},
	"node_modules": {},
}

var knownDirectoryNameScores = map[string]int{
	"build":     26,
	"dist":      30,
	"h5":        24,
	"out":       24,
	"public":    12,
	"release":   20,
	"unpackage": 18,
}

type artifactDirectorySpec struct {
	Relative  string
	AppliesTo []ProjectKind
	BaseScore int
	Reason    string
}

var artifactDirectorySpecs = []artifactDirectorySpec{
	{Relative: "dist/build/h5", AppliesTo: []ProjectKind{ProjectKindUniappH5}, BaseScore: 100, Reason: "matches common uniapp h5 output"},
	{Relative: "unpackage/dist/build/h5", AppliesTo: []ProjectKind{ProjectKindUniappH5}, BaseScore: 98, Reason: "matches common uniapp h5 output"},
	{Relative: "dist/dev/h5", AppliesTo: []ProjectKind{ProjectKindUniappH5}, BaseScore: 76, Reason: "matches uniapp h5 dev output"},
	{Relative: "unpackage/dist/dev/h5", AppliesTo: []ProjectKind{ProjectKindUniappH5}, BaseScore: 74, Reason: "matches uniapp h5 dev output"},
	{Relative: "dist", AppliesTo: []ProjectKind{ProjectKindVite, ProjectKindWebpack, ProjectKindFrontend, ProjectKindGeneric}, BaseScore: 88, Reason: "matches a standard frontend output directory"},
	{Relative: "build", AppliesTo: []ProjectKind{ProjectKindWebpack, ProjectKindFrontend, ProjectKindGeneric}, BaseScore: 82, Reason: "matches a standard frontend output directory"},
	{Relative: "out", AppliesTo: []ProjectKind{ProjectKindFrontend, ProjectKindGeneric}, BaseScore: 78, Reason: "matches a standard frontend output directory"},
	{Relative: "release", AppliesTo: []ProjectKind{ProjectKindFrontend, ProjectKindGeneric}, BaseScore: 74, Reason: "matches a standard release directory"},
	{Relative: "public", AppliesTo: []ProjectKind{ProjectKindFrontend, ProjectKindGeneric}, BaseScore: 42, Reason: "public is available, but may still be source assets"},
}

// ProjectKind identifies the legacy project's inferred frontend family.
type ProjectKind string

const (
	ProjectKindVite     ProjectKind = "vite"
	ProjectKindWebpack  ProjectKind = "webpack"
	ProjectKindUniappH5 ProjectKind = "uniapp-h5"
	ProjectKindFrontend ProjectKind = "frontend"
	ProjectKindGeneric  ProjectKind = "generic"
)

// ProjectSignals records the winning project-kind inference and its evidence.
type ProjectSignals struct {
	Kind         ProjectKind
	Reasons      []string
	HasIndexHTML bool
}

// PackageSelection identifies a workspace package root that can later be offered to the user.
type PackageSelection struct {
	Root        string
	PackageName string
}

// WorkspaceInspection is the command-owned result of workspace discovery.
type WorkspaceInspection struct {
	Root           string
	Reasons        []string
	Packages       []PackageSelection
	DefaultPackage PackageSelection
}

// CandidateDirectory is one source directory offered by a later planning step.
type CandidateDirectory struct {
	Absolute string
	Relative string
	Score    int
	Reasons  []string
}

// RecommendationConfidence summarizes the difference between the best two candidates.
type RecommendationConfidence string

const (
	RecommendationHigh   RecommendationConfidence = "high"
	RecommendationMedium RecommendationConfidence = "medium"
	RecommendationLow    RecommendationConfidence = "low"
)

// SourceSelectionModel is the command-owned discovery result used by the later prompt planner.
type SourceSelectionModel struct {
	PackageRoot string
	PackageName string
	Project     ProjectSignals
	Candidates  []CandidateDirectory
	Recommended CandidateDirectory
	Confidence  RecommendationConfidence
}

// InspectWorkspaceRoot discovers workspace packages without treating workspace signals alone as packages.
func InspectWorkspaceRoot(root string) (WorkspaceInspection, error) {
	resolvedRoot, err := filepath.Abs(root)
	if err != nil {
		return WorkspaceInspection{}, err
	}

	patterns, reasons := collectWorkspacePatterns(resolvedRoot)
	packages := []PackageSelection(nil)
	if len(patterns) > 0 {
		packages = findWorkspacePackages(resolvedRoot, patterns)
	}

	return WorkspaceInspection{
		Root:           resolvedRoot,
		Reasons:        reasons,
		Packages:       packages,
		DefaultPackage: resolvePackage(resolvedRoot),
	}, nil
}

// DetectProjectSignals applies the frozen frontend detection priority to one package root.
func DetectProjectSignals(packageRoot string) (ProjectSignals, error) {
	resolvedRoot, err := filepath.Abs(packageRoot)
	if err != nil {
		return ProjectSignals{}, err
	}
	return detectProjectSignals(resolvedRoot, readPackage(filepath.Join(resolvedRoot, "package.json"))), nil
}

// BuildSourceSelectionModel discovers and ranks source-directory candidates for one package root.
func BuildSourceSelectionModel(packageRoot string) (SourceSelectionModel, error) {
	resolvedRoot, err := filepath.Abs(packageRoot)
	if err != nil {
		return SourceSelectionModel{}, err
	}
	document := readPackage(filepath.Join(resolvedRoot, "package.json"))
	project := detectProjectSignals(resolvedRoot, document)
	candidates := buildDirectoryCandidates(resolvedRoot, project)
	if len(candidates) == 0 {
		return SourceSelectionModel{}, os.ErrNotExist
	}
	packageSelection := resolvePackage(resolvedRoot)
	return SourceSelectionModel{
		PackageRoot: resolvedRoot,
		PackageName: packageSelection.PackageName,
		Project:     project,
		Candidates:  candidates,
		Recommended: candidates[0],
		Confidence:  deriveRecommendationConfidence(candidates),
	}, nil
}

// NormalizeRelativePath renders a target relative to a root, retaining the root as ".".
func NormalizeRelativePath(root, target string) string {
	relative, err := filepath.Rel(root, target)
	if err != nil || relative == "." {
		return "."
	}
	return filepath.ToSlash(relative)
}

// SanitizeFileName keeps the frozen legacy archive-name normalization.
func SanitizeFileName(value string) string {
	if slash := strings.LastIndex(value, "/"); slash >= 0 {
		value = value[slash+1:]
	}
	value = strings.TrimSpace(value)

	var builder strings.Builder
	lastHyphen := false
	for _, character := range value {
		replace := character <= 0x1F || strings.ContainsRune(`<>:"/\\|?*`, character) || unicode.IsSpace(character)
		if replace {
			if !lastHyphen {
				builder.WriteByte('-')
				lastHyphen = true
			}
			continue
		}
		if character == '-' {
			if !lastHyphen {
				builder.WriteRune(character)
			}
			lastHyphen = true
			continue
		}
		builder.WriteRune(character)
		lastHyphen = false
	}

	value = strings.Trim(builder.String(), ".")
	if value == "" {
		return "archive"
	}
	return value
}

// ArchiveNameFromRemoteURL derives the legacy default archive name from one Git remote URL.
func ArchiveNameFromRemoteURL(remote string) string {
	repositoryPath := ""
	if match := sshRemotePattern.FindStringSubmatch(remote); len(match) == 2 {
		repositoryPath = match[1]
	} else {
		parsed, err := url.Parse(remote)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return ""
		}
		repositoryPath = strings.TrimPrefix(parsed.Path, "/")
	}
	if repositoryPath == "" {
		return ""
	}
	repositoryPath = strings.TrimSuffix(repositoryPath, ".git")
	return SanitizeFileName(strings.ReplaceAll(repositoryPath, "/", "-"))
}

func defaultArchiveName(remoteName, packageName, packageRoot string) string {
	if remoteName != "" {
		return remoteName
	}
	if packageName != "" {
		return SanitizeFileName(packageName)
	}
	return SanitizeFileName(filepath.Base(packageRoot))
}

func collectWorkspacePatterns(root string) ([]string, []string) {
	patterns := workspacePatterns(readPackage(filepath.Join(root, "package.json")))
	reasons := make([]string, 0, 4)
	if len(patterns) > 0 {
		reasons = append(reasons, "package.json workspaces")
	}

	pnpmPatterns := readPnpmWorkspacePatterns(root)
	if len(pnpmPatterns) > 0 {
		patterns = append(patterns, pnpmPatterns...)
		reasons = append(reasons, "pnpm-workspace.yaml")
	}
	if pathExists(filepath.Join(root, "turbo.json")) {
		reasons = append(reasons, "turbo.json")
	}
	if pathExists(filepath.Join(root, "nx.json")) {
		reasons = append(reasons, "nx.json")
	}
	if isDirectory(filepath.Join(root, "packages")) {
		patterns = append(patterns, "packages/*")
		reasons = append(reasons, "packages/* layout")
	}

	return uniqueStrings(patterns), uniqueStrings(reasons)
}

func workspacePatterns(document map[string]json.RawMessage) []string {
	raw, ok := document["workspaces"]
	if !ok {
		return nil
	}

	var patterns []string
	if err := json.Unmarshal(raw, &patterns); err == nil {
		return nonBlankStrings(patterns)
	}

	var workspaceConfig struct {
		Packages []string `json:"packages"`
	}
	if err := json.Unmarshal(raw, &workspaceConfig); err != nil {
		return nil
	}
	return nonBlankStrings(workspaceConfig.Packages)
}

func readPnpmWorkspacePatterns(root string) []string {
	contents, err := os.ReadFile(filepath.Join(root, "pnpm-workspace.yaml"))
	if err != nil {
		return nil
	}

	patterns := make([]string, 0)
	inPackagesBlock := false
	for _, line := range strings.Split(string(contents), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if strings.TrimSpace(line) == "packages:" {
			inPackagesBlock = true
			continue
		}
		if !inPackagesBlock {
			continue
		}
		if line != "" && line[0] != ' ' && line[0] != '\t' {
			break
		}
		match := pnpmWorkspacePattern.FindStringSubmatch(line)
		if len(match) == 2 && match[1] != "" {
			patterns = append(patterns, match[1])
		}
	}
	return patterns
}

func findWorkspacePackages(root string, patterns []string) []PackageSelection {
	candidates := packageRoots(root)
	seen := make(map[string]bool, len(candidates))
	packages := make([]PackageSelection, 0, len(candidates))
	for _, pattern := range patterns {
		for _, candidate := range candidates {
			relative, err := filepath.Rel(root, candidate)
			if err != nil || !workspacePatternMatches(pattern, filepath.ToSlash(relative)) || seen[candidate] {
				continue
			}
			seen[candidate] = true
			packages = append(packages, resolvePackage(candidate))
		}
	}

	sort.Slice(packages, func(left, right int) bool {
		leftLabel := packages[left].PackageName
		if leftLabel == "" {
			leftLabel = filepath.Base(packages[left].Root)
		}
		rightLabel := packages[right].PackageName
		if rightLabel == "" {
			rightLabel = filepath.Base(packages[right].Root)
		}
		if leftLabel == rightLabel {
			return packages[left].Root < packages[right].Root
		}
		return leftLabel < rightLabel
	})
	return packages
}

func packageRoots(root string) []string {
	roots := make([]string, 0)
	_ = filepath.WalkDir(root, func(candidate string, entry fs.DirEntry, err error) error {
		if err != nil {
			if entry != nil && entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if entry.IsDir() || entry.Name() != "package.json" || entry.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		roots = append(roots, filepath.Dir(candidate))
		return nil
	})
	return roots
}

func workspacePatternMatches(pattern, relative string) bool {
	pattern = strings.TrimSuffix(strings.TrimPrefix(strings.ReplaceAll(pattern, "\\", "/"), "./"), "/")
	if pattern == "" || pattern == "." {
		return relative == "."
	}
	if relative == "." {
		return false
	}
	return matchWorkspaceSegments(strings.Split(pattern, "/"), strings.Split(relative, "/"))
}

func matchWorkspaceSegments(pattern, candidate []string) bool {
	if len(pattern) == 0 {
		return len(candidate) == 0
	}
	if pattern[0] == "**" {
		if len(pattern) == 1 {
			return true
		}
		for index := 0; index <= len(candidate); index++ {
			if matchWorkspaceSegments(pattern[1:], candidate[index:]) {
				return true
			}
		}
		return false
	}
	if len(candidate) == 0 {
		return false
	}
	matched, err := path.Match(pattern[0], candidate[0])
	return err == nil && matched && matchWorkspaceSegments(pattern[1:], candidate[1:])
}

func resolvePackage(root string) PackageSelection {
	document := readPackage(filepath.Join(root, "package.json"))
	var packageName string
	if raw, ok := document["name"]; ok {
		_ = json.Unmarshal(raw, &packageName)
	}
	return PackageSelection{Root: root, PackageName: packageName}
}

func detectProjectSignals(root string, document map[string]json.RawMessage) ProjectSignals {
	hasIndexHTML := pathExists(filepath.Join(root, "index.html"))
	if config, ok := firstExistingRelativePath(root, uniappSignalFiles); ok || packageHasDependency(document, "@dcloudio/vite-plugin-uni") || packageHasDependency(document, "@dcloudio/uni-app") || packageScriptsContain(document, "uni") {
		reason := "found uniapp dependencies or scripts"
		if ok {
			reason = "found " + config
		}
		return ProjectSignals{Kind: ProjectKindUniappH5, Reasons: []string{reason}, HasIndexHTML: hasIndexHTML}
	}
	if config, ok := firstExistingRelativePath(root, viteConfigFiles); ok || packageHasDependency(document, "vite") || packageScriptsContain(document, "vite") {
		reason := "found vite dependencies or scripts"
		if ok {
			reason = "found " + config
		}
		return ProjectSignals{Kind: ProjectKindVite, Reasons: []string{reason}, HasIndexHTML: hasIndexHTML}
	}
	if config, ok := firstExistingRelativePath(root, webpackConfigFiles); ok || packageHasDependency(document, "webpack") || packageScriptsContain(document, "webpack") {
		reason := "found webpack dependencies or scripts"
		if ok {
			reason = "found " + config
		}
		return ProjectSignals{Kind: ProjectKindWebpack, Reasons: []string{reason}, HasIndexHTML: hasIndexHTML}
	}
	if hasIndexHTML {
		return ProjectSignals{Kind: ProjectKindFrontend, Reasons: []string{"package root contains index.html"}, HasIndexHTML: true}
	}
	return ProjectSignals{Kind: ProjectKindGeneric, Reasons: []string{"no strong frontend build signal found"}}
}

func firstExistingRelativePath(root string, candidates []string) (string, bool) {
	for _, candidate := range candidates {
		if pathExists(filepath.Join(root, candidate)) {
			return candidate, true
		}
	}
	return "", false
}

func packageHasDependency(document map[string]json.RawMessage, dependency string) bool {
	for _, field := range []string{"dependencies", "devDependencies", "peerDependencies"} {
		raw, ok := document[field]
		if !ok {
			continue
		}
		var values map[string]json.RawMessage
		if json.Unmarshal(raw, &values) != nil {
			continue
		}
		var value string
		if rawValue, ok := values[dependency]; ok && json.Unmarshal(rawValue, &value) == nil {
			return true
		}
	}
	return false
}

func packageScriptsContain(document map[string]json.RawMessage, token string) bool {
	raw, ok := document["scripts"]
	if !ok {
		return false
	}
	var values map[string]json.RawMessage
	if json.Unmarshal(raw, &values) != nil {
		return false
	}
	for _, rawValue := range values {
		var value string
		if json.Unmarshal(rawValue, &value) == nil && strings.Contains(value, token) {
			return true
		}
	}
	return false
}

func readPackage(path string) map[string]json.RawMessage {
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var document map[string]json.RawMessage
	if json.Unmarshal(contents, &document) != nil {
		return nil
	}
	return document
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func isDirectory(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func nonBlankStrings(values []string) []string {
	filtered := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			filtered = append(filtered, value)
		}
	}
	return filtered
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]bool, len(values))
	unique := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		unique = append(unique, value)
	}
	return unique
}

func buildDirectoryCandidates(packageRoot string, project ProjectSignals) []CandidateDirectory {
	candidates := make(map[string]CandidateDirectory)
	for _, spec := range artifactDirectorySpecs {
		if !artifactSpecApplies(spec, project.Kind) {
			continue
		}
		absolute := filepath.Join(packageRoot, spec.Relative)
		if !isDirectory(absolute) {
			continue
		}
		candidate := scoreArtifactDirectory(packageRoot, absolute, spec.Relative, project.Kind)
		candidate.Score += spec.BaseScore
		candidate.Reasons = mergeReasonLists(candidate.Reasons, []string{spec.Reason})
		addCandidate(candidates, candidate)
	}

	scanned := scanDirectories(packageRoot, 2)
	for _, directory := range scanned {
		hasIndexHTML := pathExists(filepath.Join(directory.Absolute, "index.html"))
		knownNameScore := knownDirectoryNameScores[filepath.Base(directory.Absolute)]
		if !hasIndexHTML && knownNameScore == 0 {
			continue
		}
		candidate := scoreArtifactDirectory(packageRoot, directory.Absolute, directory.Relative, project.Kind)
		if directory.Depth == 1 {
			candidate.Score += 10
		} else {
			candidate.Score += 4
		}
		if !hasIndexHTML && knownNameScore > 0 {
			candidate.Reasons = mergeReasonLists(candidate.Reasons, []string{"surface-level candidate for manual review"})
		}
		addCandidate(candidates, candidate)
	}

	rootCandidate := scoreArtifactDirectory(packageRoot, packageRoot, ".", project.Kind)
	if project.HasIndexHTML {
		rootCandidate.Score += 20
	}
	addCandidate(candidates, rootCandidate)

	ranked := sortedCandidates(candidates)
	if len(ranked) <= 1 {
		for _, directory := range scanned {
			if directory.Depth > 1 {
				continue
			}
			addCandidate(candidates, CandidateDirectory{
				Absolute: directory.Absolute,
				Relative: directory.Relative,
				Score:    12,
				Reasons:  []string{"fallback candidate for manual selection"},
			})
			if len(candidates) >= 11 {
				break
			}
		}
		ranked = sortedCandidates(candidates)
	}
	return ranked
}

type scannedDirectory struct {
	Absolute string
	Relative string
	Depth    int
}

func scanDirectories(root string, maximumDepth int) []scannedDirectory {
	queue := []scannedDirectory{{Absolute: root}}
	scanned := make([]scannedDirectory, 0)
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if current.Depth >= maximumDepth {
			continue
		}
		entries, err := os.ReadDir(current.Absolute)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			if _, ignored := scanIgnoredDirectories[entry.Name()]; ignored {
				continue
			}
			directory := scannedDirectory{
				Absolute: filepath.Join(current.Absolute, entry.Name()),
				Depth:    current.Depth + 1,
			}
			directory.Relative = NormalizeRelativePath(root, directory.Absolute)
			scanned = append(scanned, directory)
			queue = append(queue, directory)
		}
	}
	return scanned
}

func scoreArtifactDirectory(packageRoot, absoluteDirectory, relativeDirectory string, kind ProjectKind) CandidateDirectory {
	score := 0
	reasons := make([]string, 0)
	for _, part := range strings.Split(relativeDirectory, "/") {
		if partScore := knownDirectoryNameScores[part]; partScore != 0 {
			score += partScore
			reasons = append(reasons, "matched directory name "+part)
		}
	}
	if pathExists(filepath.Join(absoluteDirectory, "index.html")) {
		score += 24
		reasons = append(reasons, "contains index.html")
	}
	if kind == ProjectKindVite && relativeDirectory == "dist" {
		score += 18
		reasons = append(reasons, "matches vite output convention")
	}
	if kind == ProjectKindWebpack && (relativeDirectory == "dist" || relativeDirectory == "build") {
		score += 16
		reasons = append(reasons, "matches webpack output convention")
	}
	if kind == ProjectKindUniappH5 && strings.HasSuffix(relativeDirectory, "/h5") {
		score += 28
		reasons = append(reasons, "matches uniapp h5 output convention")
	}
	if strings.HasPrefix(relativeDirectory, "dist/") || strings.HasPrefix(relativeDirectory, "unpackage/") {
		score += 8
		reasons = append(reasons, "nested under a common output tree")
	}
	if absoluteDirectory == packageRoot {
		score += 8
		reasons = append(reasons, "package root fallback")
	}
	return CandidateDirectory{Absolute: absoluteDirectory, Relative: relativeDirectory, Score: score, Reasons: uniqueStrings(reasons)}
}

func artifactSpecApplies(spec artifactDirectorySpec, kind ProjectKind) bool {
	for _, applicable := range spec.AppliesTo {
		if applicable == kind {
			return true
		}
	}
	return false
}

func addCandidate(candidates map[string]CandidateDirectory, candidate CandidateDirectory) {
	existing, ok := candidates[candidate.Absolute]
	if !ok {
		candidates[candidate.Absolute] = candidate
		return
	}
	if candidate.Score > existing.Score {
		existing.Score = candidate.Score
	}
	existing.Reasons = mergeReasonLists(existing.Reasons, candidate.Reasons)
	candidates[candidate.Absolute] = existing
}

func sortedCandidates(candidates map[string]CandidateDirectory) []CandidateDirectory {
	ranked := make([]CandidateDirectory, 0, len(candidates))
	for _, candidate := range candidates {
		ranked = append(ranked, candidate)
	}
	sort.Slice(ranked, func(left, right int) bool {
		if ranked[left].Score != ranked[right].Score {
			return ranked[left].Score > ranked[right].Score
		}
		return ranked[left].Relative < ranked[right].Relative
	})
	return ranked
}

func deriveRecommendationConfidence(candidates []CandidateDirectory) RecommendationConfidence {
	if len(candidates) == 0 {
		return RecommendationLow
	}
	gap := candidates[0].Score
	if len(candidates) > 1 {
		gap -= candidates[1].Score
	}
	if candidates[0].Score >= 92 && gap >= 18 {
		return RecommendationHigh
	}
	if candidates[0].Score >= 78 && gap >= 8 {
		return RecommendationMedium
	}
	return RecommendationLow
}

func mergeReasonLists(existing, incoming []string) []string {
	return uniqueStrings(append(append([]string(nil), existing...), incoming...))
}
