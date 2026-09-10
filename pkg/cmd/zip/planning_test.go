package zip

import (
	"errors"
	"path/filepath"
	"reflect"
	"testing"
)

func TestPlanningSessionOffersRootAndWorkspacePackages(t *testing.T) {
	root := t.TempDir()
	writeZipFile(t, filepath.Join(root, "package.json"), `{"name":"root","workspaces":["apps/*"]}`)
	writeZipFile(t, filepath.Join(root, "apps", "web", "package.json"), `{"name":"web"}`)

	session, err := CreatePlanningSession(root)
	if err != nil {
		t.Fatalf("CreatePlanningSession() error = %v", err)
	}
	resolution, err := ResolvePlanningStep(session, nil)
	if err != nil {
		t.Fatalf("ResolvePlanningStep() error = %v", err)
	}
	step, ok := resolution.Step.(SelectPackageStep)
	if !ok {
		t.Fatalf("step = %T, want SelectPackageStep", resolution.Step)
	}
	if step.Message != "Select a package to zip:" {
		t.Fatalf("message = %q", step.Message)
	}
	want := []PlanningChoice{
		{Value: root, Label: ".", Hint: "root package: root"},
		{Value: filepath.Join(root, "apps", "web"), Label: "apps/web", Hint: "package: web"},
	}
	if !reflect.DeepEqual(step.Options, want) {
		t.Fatalf("options = %#v, want %#v", step.Options, want)
	}
}

func TestPlanningSessionAppliesAnswersInFrozenOrder(t *testing.T) {
	root := t.TempDir()
	writeZipFile(t, filepath.Join(root, "package.json"), `{"name":"package-name","devDependencies":{"vite":"1"}}`)
	writeZipFile(t, filepath.Join(root, "dist", "index.html"), "")

	session, err := CreatePlanningSession(root)
	if err != nil {
		t.Fatalf("CreatePlanningSession() error = %v", err)
	}
	resolution, err := ResolvePlanningStep(session, nil)
	if err != nil {
		t.Fatalf("ResolvePlanningStep() error = %v", err)
	}
	sourceStep, ok := resolution.Step.(SelectSourceStep)
	if !ok {
		t.Fatalf("step = %T, want SelectSourceStep", resolution.Step)
	}
	if sourceStep.Options[0].Label != "dist (recommended)" || sourceStep.Options[0].Hint != "high confidence" {
		t.Fatalf("source options = %#v", sourceStep.Options)
	}

	session, err = ApplyPlanningAnswer(resolution.Session, PlanningAnswer{Type: PlanningAnswerSource, Value: sourceStep.Options[0].Value})
	if err != nil {
		t.Fatalf("ApplyPlanningAnswer(source) error = %v", err)
	}
	resolution, err = ResolvePlanningStep(session, nil)
	if err != nil {
		t.Fatalf("ResolvePlanningStep(glob) error = %v", err)
	}
	globStep, ok := resolution.Step.(SelectGlobStep)
	if !ok {
		t.Fatalf("step = %T, want SelectGlobStep", resolution.Step)
	}
	if globStep.Message != "Select file patterns to include in the zip:" || !reflect.DeepEqual(globStep.InitialValues, []string{defaultGlobPattern}) {
		t.Fatalf("glob step = %#v", globStep)
	}

	session, err = ApplyPlanningAnswer(resolution.Session, PlanningAnswer{Type: PlanningAnswerGlob, Values: []string{"**/*.html", "assets/**/*"}})
	if err != nil {
		t.Fatalf("ApplyPlanningAnswer(glob) error = %v", err)
	}
	resolver := remoteNameResolverFunc(func(path string) (string, error) {
		if path != root {
			t.Fatalf("remote root = %q, want %q", path, root)
		}
		return "owner-repo", nil
	})
	resolution, err = ResolvePlanningStep(session, resolver)
	if err != nil {
		t.Fatalf("ResolvePlanningStep(output) error = %v", err)
	}
	outputStep, ok := resolution.Step.(EditOutputFileStep)
	if !ok {
		t.Fatalf("step = %T, want EditOutputFileStep", resolution.Step)
	}
	if outputStep.InitialValue != "owner-repo" {
		t.Fatalf("initial output = %q", outputStep.InitialValue)
	}

	session, err = ApplyPlanningAnswer(resolution.Session, PlanningAnswer{Type: PlanningAnswerOutput, Value: "release bundle"})
	if err != nil {
		t.Fatalf("ApplyPlanningAnswer(output) error = %v", err)
	}
	resolution, err = ResolvePlanningStep(session, resolver)
	if err != nil {
		t.Fatalf("ResolvePlanningStep(complete) error = %v", err)
	}
	complete, ok := resolution.Step.(CompleteStep)
	if !ok {
		t.Fatalf("step = %T, want CompleteStep", resolution.Step)
	}
	wantPlan := ZipPlan{
		Input:       filepath.Join(root, "dist"),
		File:        "release-bundle",
		Glob:        []string{"**/*.html", "assets/**/*"},
		PackageRoot: root,
		PackageName: "package-name",
		Confidence:  RecommendationHigh,
	}
	if !reflect.DeepEqual(complete.Plan, wantPlan) {
		t.Fatalf("plan = %#v, want %#v", complete.Plan, wantPlan)
	}
}

func TestPlanningSessionNormalizesAllAndEmptyGlobChoices(t *testing.T) {
	for _, input := range [][]string{{}, {"**/*.css", defaultGlobPattern}, {defaultGlobPattern, "assets/**/*"}} {
		if got := normalizeSelectedPatterns(input); !reflect.DeepEqual(got, []string{defaultGlobPattern}) {
			t.Fatalf("normalizeSelectedPatterns(%#v) = %#v", input, got)
		}
	}
	custom := []string{"**/*.css", "assets/**/*"}
	if got := normalizeSelectedPatterns(custom); !reflect.DeepEqual(got, custom) {
		t.Fatalf("custom patterns = %#v, want %#v", got, custom)
	}
}

func TestPlanningPackageAnswerResetsDependentSelections(t *testing.T) {
	root := t.TempDir()
	other := filepath.Join(root, "apps", "other")
	session := PlanningSession{
		RootDir: root,
		Workspace: WorkspaceInspection{
			DefaultPackage: PackageSelection{Root: root, PackageName: "root"},
			Packages:       []PackageSelection{{Root: other, PackageName: "other"}},
		},
		SourceSelection: &SourceSelectionModel{},
		SelectedSource:  filepath.Join(root, "dist"),
		GlobPatterns:    []string{"**/*.css"},
		GlobSelected:    true,
		OutputFileName:  "existing",
	}
	updated, err := ApplyPlanningAnswer(session, PlanningAnswer{Type: PlanningAnswerPackage, Value: other})
	if err != nil {
		t.Fatalf("ApplyPlanningAnswer() error = %v", err)
	}
	if updated.PackageRoot != other || updated.PackageName != "other" || updated.SourceSelection != nil || updated.SelectedSource != "" || updated.GlobSelected || updated.OutputFileName != "" || updated.GlobPatterns != nil {
		t.Fatalf("updated session = %#v", updated)
	}
}

func TestPlanningSessionFallsBackWhenRemoteLookupFails(t *testing.T) {
	root := t.TempDir()
	writeZipFile(t, filepath.Join(root, "package.json"), `{"name":"package-name"}`)
	writeZipFile(t, filepath.Join(root, "index.html"), "")
	session, err := CreatePlanningSession(root)
	if err != nil {
		t.Fatalf("CreatePlanningSession() error = %v", err)
	}
	resolution, err := ResolvePlanningStep(session, nil)
	if err != nil {
		t.Fatalf("ResolvePlanningStep(source) error = %v", err)
	}
	source := resolution.Step.(SelectSourceStep).Options[0]
	session, err = ApplyPlanningAnswer(resolution.Session, PlanningAnswer{Type: PlanningAnswerSource, Value: source.Value})
	if err != nil {
		t.Fatalf("ApplyPlanningAnswer(source) error = %v", err)
	}
	session, err = ApplyPlanningAnswer(session, PlanningAnswer{Type: PlanningAnswerGlob, Values: nil})
	if err != nil {
		t.Fatalf("ApplyPlanningAnswer(glob) error = %v", err)
	}
	resolution, err = ResolvePlanningStep(session, remoteNameResolverFunc(func(string) (string, error) {
		return "", errors.New("git unavailable")
	}))
	if err != nil {
		t.Fatalf("ResolvePlanningStep(output) error = %v", err)
	}
	output := resolution.Step.(EditOutputFileStep)
	if output.InitialValue != "package-name" || !resolution.Session.RemoteNameFetched {
		t.Fatalf("output step = %#v; session = %#v", output, resolution.Session)
	}
}

type remoteNameResolverFunc func(string) (string, error)

func (function remoteNameResolverFunc) ResolveRemoteName(path string) (string, error) {
	return function(path)
}
