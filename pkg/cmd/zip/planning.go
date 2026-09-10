package zip

import (
	"fmt"
	"path/filepath"
	"strings"
)

const defaultGlobPattern = "**/*"

var zipGlobOptions = []PlanningChoice{
	{Value: defaultGlobPattern, Label: "All files (recommended)"},
	{Value: "**/*.html", Label: "HTML files"},
	{Value: "**/*.js", Label: "JavaScript files"},
	{Value: "**/*.css", Label: "CSS files"},
	{Value: "assets/**/*", Label: "assets directory"},
	{Value: "static/**/*", Label: "static directory"},
}

// PlanningChoice is one option in a command-owned planning step.
type PlanningChoice struct {
	Value string
	Label string
	Hint  string
}

// PlanningNote is supplemental information a terminal adapter may render around a step.
type PlanningNote struct {
	Title string
	Lines []string
}

// ZipPlan is the completed immutable archive request, before collection or publication begins.
type ZipPlan struct {
	Input       string
	File        string
	Glob        []string
	PackageRoot string
	PackageName string
	Confidence  RecommendationConfidence
}

// PlanningSession retains the answers collected by the interactive planning flow.
type PlanningSession struct {
	RootDir           string
	Workspace         WorkspaceInspection
	PackageRoot       string
	PackageName       string
	SourceSelection   *SourceSelectionModel
	SelectedSource    string
	GlobPatterns      []string
	GlobSelected      bool
	OutputFileName    string
	RemoteName        string
	RemoteNameFetched bool
}

// PlanningStep is one state-machine output consumed by a later terminal adapter.
type PlanningStep interface {
	planningStep()
}

// SelectPackageStep requests one workspace package root.
type SelectPackageStep struct {
	Note    PlanningNote
	Message string
	Options []PlanningChoice
}

func (SelectPackageStep) planningStep() {}

// SelectSourceStep requests one candidate source directory.
type SelectSourceStep struct {
	Note    PlanningNote
	Message string
	Options []PlanningChoice
}

func (SelectSourceStep) planningStep() {}

// SelectGlobStep requests a set of fixed archive patterns.
type SelectGlobStep struct {
	Message       string
	Options       []PlanningChoice
	InitialValues []string
}

func (SelectGlobStep) planningStep() {}

// EditOutputFileStep requests the archive's output base name.
type EditOutputFileStep struct {
	Message      string
	InitialValue string
}

func (EditOutputFileStep) planningStep() {}

// CompleteStep presents the finished plan without performing archive work.
type CompleteStep struct {
	Note PlanningNote
	Plan ZipPlan
}

func (CompleteStep) planningStep() {}

// PlanningAnswerType identifies the response accepted by a matching planning step.
type PlanningAnswerType string

const (
	PlanningAnswerPackage PlanningAnswerType = "package-root"
	PlanningAnswerSource  PlanningAnswerType = "source-directory"
	PlanningAnswerGlob    PlanningAnswerType = "glob-patterns"
	PlanningAnswerOutput  PlanningAnswerType = "output-file"
)

// PlanningAnswer carries the selected value or values from a terminal adapter.
type PlanningAnswer struct {
	Type   PlanningAnswerType
	Value  string
	Values []string
}

// PlanningResolution pairs a normalized session with its next step.
type PlanningResolution struct {
	Session PlanningSession
	Step    PlanningStep
}

// RemoteNameResolver resolves the already-selected workspace's preferred archive name.
type RemoteNameResolver interface {
	ResolveRemoteName(string) (string, error)
}

// CreatePlanningSession initializes discovery and defers all interactions to later steps.
func CreatePlanningSession(directory string) (PlanningSession, error) {
	workspace, err := InspectWorkspaceRoot(directory)
	if err != nil {
		return PlanningSession{}, err
	}
	session := PlanningSession{
		RootDir:   workspace.Root,
		Workspace: workspace,
	}
	if len(workspace.Packages) == 0 {
		session.PackageRoot = workspace.DefaultPackage.Root
		session.PackageName = workspace.DefaultPackage.PackageName
	}
	return session, nil
}

// ResolvePlanningStep advances derived state and describes exactly one next interaction.
func ResolvePlanningStep(session PlanningSession, resolver RemoteNameResolver) (PlanningResolution, error) {
	if session.PackageRoot == "" && len(session.Workspace.Packages) > 0 {
		return PlanningResolution{Session: session, Step: buildPackageStep(session)}, nil
	}

	hydrated, err := hydrateSourceSelection(session)
	if err != nil {
		return PlanningResolution{}, err
	}
	if hydrated.SelectedSource == "" {
		return PlanningResolution{Session: hydrated, Step: buildSourceStep(hydrated)}, nil
	}
	if !hydrated.GlobSelected {
		return PlanningResolution{Session: hydrated, Step: SelectGlobStep{
			Message:       "Select file patterns to include in the zip:",
			Options:       clonePlanningChoices(zipGlobOptions),
			InitialValues: []string{defaultGlobPattern},
		}}, nil
	}
	if hydrated.OutputFileName == "" {
		hydrated = hydrateRemoteName(hydrated, resolver)
		return PlanningResolution{Session: hydrated, Step: EditOutputFileStep{
			Message:      "Enter the name for the zip file (without .zip extension):",
			InitialValue: defaultArchiveName(hydrated.RemoteName, hydrated.PackageName, hydrated.PackageRoot),
		}}, nil
	}
	return PlanningResolution{Session: hydrated, Step: buildCompleteStep(hydrated)}, nil
}

// ApplyPlanningAnswer records one answer without performing I/O or archive work.
func ApplyPlanningAnswer(session PlanningSession, answer PlanningAnswer) (PlanningSession, error) {
	switch answer.Type {
	case PlanningAnswerPackage:
		root, err := filepath.Abs(answer.Value)
		if err != nil {
			return PlanningSession{}, err
		}
		selected, _ := selectablePackage(session, root)
		session.PackageRoot = root
		session.PackageName = selected.PackageName
		session.SourceSelection = nil
		session.SelectedSource = ""
		session.GlobPatterns = nil
		session.GlobSelected = false
		session.OutputFileName = ""
		return session, nil
	case PlanningAnswerSource:
		source, err := filepath.Abs(answer.Value)
		if err != nil {
			return PlanningSession{}, err
		}
		session.SelectedSource = source
		return session, nil
	case PlanningAnswerGlob:
		session.GlobPatterns = normalizeSelectedPatterns(answer.Values)
		session.GlobSelected = true
		return session, nil
	case PlanningAnswerOutput:
		session.OutputFileName = SanitizeFileName(answer.Value)
		return session, nil
	default:
		return PlanningSession{}, fmt.Errorf("unknown zip planning answer %q", answer.Type)
	}
}

func hydrateSourceSelection(session PlanningSession) (PlanningSession, error) {
	if session.PackageRoot == "" || session.SourceSelection != nil {
		return session, nil
	}
	selection, err := BuildSourceSelectionModel(session.PackageRoot)
	if err != nil {
		return PlanningSession{}, err
	}
	session.SourceSelection = &selection
	if selection.PackageName != "" {
		session.PackageName = selection.PackageName
	}
	return session, nil
}

func hydrateRemoteName(session PlanningSession, resolver RemoteNameResolver) PlanningSession {
	if session.RemoteNameFetched {
		return session
	}
	session.RemoteNameFetched = true
	if resolver == nil {
		return session
	}
	name, err := resolver.ResolveRemoteName(session.RootDir)
	if err == nil {
		session.RemoteName = name
	}
	return session
}

func buildPackageStep(session PlanningSession) SelectPackageStep {
	packages := selectablePackages(session)
	options := make([]PlanningChoice, 0, len(packages))
	for _, selection := range packages {
		relative := NormalizeRelativePath(session.RootDir, selection.Root)
		isRoot := relative == "."
		hint := "workspace package"
		if selection.PackageName != "" {
			if isRoot {
				hint = "root package: " + selection.PackageName
			} else {
				hint = "package: " + selection.PackageName
			}
		} else if isRoot {
			hint = "workspace root"
		}
		options = append(options, PlanningChoice{Value: selection.Root, Label: relative, Hint: hint})
	}
	return SelectPackageStep{
		Note: PlanningNote{
			Title: "Monorepo detected",
			Lines: []string{
				fmt.Sprintf("Found %d workspace package%s", len(session.Workspace.Packages), pluralSuffix(len(session.Workspace.Packages))),
				"Current directory is available as .",
				"Signals: " + summarizeItems(session.Workspace.Reasons),
			},
		},
		Message: "Select a package to zip:",
		Options: options,
	}
}

func buildSourceStep(session PlanningSession) SelectSourceStep {
	selection := session.SourceSelection
	lowConfidence := selection.Confidence == RecommendationLow
	lines := []string{
		"Project type: " + describeProjectKind(selection.Project.Kind),
		"Confidence: " + confidenceLabel(selection.Confidence),
	}
	if lowConfidence {
		lines = append(lines, "No clear build output was found. Pick the directory to ship.")
	} else {
		lines = append(lines, "Recommended: "+selection.Recommended.Relative)
	}
	options := make([]PlanningChoice, 0, len(selection.Candidates))
	for index, candidate := range selection.Candidates {
		recommended := index == 0
		label := candidate.Relative
		if recommended {
			label += " (recommended)"
		}
		options = append(options, PlanningChoice{
			Value: candidate.Absolute,
			Label: label,
			Hint:  candidateHint(candidate, selection.Confidence, recommended),
		})
	}
	return SelectSourceStep{
		Note:    PlanningNote{Title: "Artifact selection", Lines: lines},
		Message: "Select a directory to zip:",
		Options: options,
	}
}

func buildCompleteStep(session PlanningSession) CompleteStep {
	plan := ZipPlan{
		Input:       session.SelectedSource,
		File:        session.OutputFileName,
		Glob:        append([]string(nil), session.GlobPatterns...),
		PackageRoot: session.PackageRoot,
		PackageName: session.PackageName,
		Confidence:  session.SourceSelection.Confidence,
	}
	packageLabel := session.PackageName
	if packageLabel == "" {
		packageLabel = filepath.Base(session.PackageRoot)
	}
	return CompleteStep{
		Note: PlanningNote{
			Title: "Zip plan",
			Lines: []string{
				"Package: " + packageLabel,
				"Source: " + NormalizeRelativePath(session.PackageRoot, plan.Input),
				"Patterns: " + strings.Join(plan.Glob, ", "),
				"Output: " + plan.File + ".zip",
			},
		},
		Plan: plan,
	}
}

func selectablePackages(session PlanningSession) []PackageSelection {
	packages := make([]PackageSelection, 0, len(session.Workspace.Packages)+1)
	seen := make(map[string]bool, len(session.Workspace.Packages)+1)
	for _, selection := range append([]PackageSelection{session.Workspace.DefaultPackage}, session.Workspace.Packages...) {
		if seen[selection.Root] {
			continue
		}
		seen[selection.Root] = true
		packages = append(packages, selection)
	}
	return packages
}

func selectablePackage(session PlanningSession, root string) (PackageSelection, bool) {
	for _, selection := range selectablePackages(session) {
		if selection.Root == root {
			return selection, true
		}
	}
	return PackageSelection{}, false
}

func normalizeSelectedPatterns(patterns []string) []string {
	for _, pattern := range patterns {
		if pattern == defaultGlobPattern {
			return []string{defaultGlobPattern}
		}
	}
	if len(patterns) == 0 {
		return []string{defaultGlobPattern}
	}
	return append([]string(nil), patterns...)
}

func clonePlanningChoices(choices []PlanningChoice) []PlanningChoice {
	return append([]PlanningChoice(nil), choices...)
}

func candidateHint(candidate CandidateDirectory, confidence RecommendationConfidence, recommended bool) string {
	if recommended {
		return confidenceLabel(confidence) + " confidence"
	}
	if candidate.Relative == "." {
		return "package root"
	}
	if containsReason(candidate.Reasons, "surface-level candidate for manual review") || containsReason(candidate.Reasons, "fallback candidate for manual selection") {
		return "manual review"
	}
	if containsReason(candidate.Reasons, "contains index.html") {
		return "contains index.html"
	}
	return "possible output"
}

func containsReason(reasons []string, target string) bool {
	for _, reason := range reasons {
		if reason == target {
			return true
		}
	}
	return false
}

func describeProjectKind(kind ProjectKind) string {
	switch kind {
	case ProjectKindVite:
		return "Vite frontend"
	case ProjectKindWebpack:
		return "Webpack frontend"
	case ProjectKindUniappH5:
		return "uniapp h5 frontend"
	case ProjectKindFrontend:
		return "generic frontend"
	default:
		return "generic directory"
	}
}

func confidenceLabel(confidence RecommendationConfidence) string {
	return string(confidence)
}

func summarizeItems(values []string) string {
	if len(values) <= 2 {
		return strings.Join(values, ", ")
	}
	return strings.Join(values[:2], ", ") + fmt.Sprintf(", +%d more", len(values)-2)
}

func pluralSuffix(count int) string {
	if count == 1 {
		return ""
	}
	return "s"
}
