package zip

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestInspectWorkspaceRootDiscoversPackageJSONPnpmAndLayoutPackages(t *testing.T) {
	root := t.TempDir()
	writeZipFile(t, filepath.Join(root, "package.json"), `{"name":"root-package","workspaces":["apps/*"]}`)
	writeZipFile(t, filepath.Join(root, "pnpm-workspace.yaml"), "packages:\n  - 'services/**'\n")
	writeZipFile(t, filepath.Join(root, "turbo.json"), "{}")
	writeZipFile(t, filepath.Join(root, "nx.json"), "{}")
	writeZipFile(t, filepath.Join(root, "apps", "web", "package.json"), `{"name":"@scope/web"}`)
	writeZipFile(t, filepath.Join(root, "services", "api", "package.json"), `{"name":"api"}`)
	writeZipFile(t, filepath.Join(root, "services", "tools", "nested", "package.json"), `{"name":"nested"}`)
	writeZipFile(t, filepath.Join(root, "packages", "utility", "package.json"), `{}`)
	writeZipFile(t, filepath.Join(root, "ignored", "package.json"), `{"name":"ignored"}`)

	inspection, err := InspectWorkspaceRoot(root)
	if err != nil {
		t.Fatalf("InspectWorkspaceRoot() error = %v", err)
	}
	if inspection.Root != root {
		t.Fatalf("root = %q, want %q", inspection.Root, root)
	}
	if inspection.DefaultPackage != (PackageSelection{Root: root, PackageName: "root-package"}) {
		t.Fatalf("default package = %#v", inspection.DefaultPackage)
	}
	wantReasons := []string{"package.json workspaces", "pnpm-workspace.yaml", "turbo.json", "nx.json", "packages/* layout"}
	if !reflect.DeepEqual(inspection.Reasons, wantReasons) {
		t.Fatalf("reasons = %#v, want %#v", inspection.Reasons, wantReasons)
	}
	wantPackages := []PackageSelection{
		{Root: filepath.Join(root, "apps", "web"), PackageName: "@scope/web"},
		{Root: filepath.Join(root, "services", "api"), PackageName: "api"},
		{Root: filepath.Join(root, "services", "tools", "nested"), PackageName: "nested"},
		{Root: filepath.Join(root, "packages", "utility")},
	}
	if !reflect.DeepEqual(inspection.Packages, wantPackages) {
		t.Fatalf("packages = %#v, want %#v", inspection.Packages, wantPackages)
	}
}

func TestInspectWorkspaceRootSupportsWorkspaceObjectAndRootPattern(t *testing.T) {
	root := t.TempDir()
	writeZipFile(t, filepath.Join(root, "package.json"), `{"name":"root","workspaces":{"packages":[".","modules/*"]}}`)
	writeZipFile(t, filepath.Join(root, "modules", "alpha", "package.json"), `{"name":"alpha"}`)

	inspection, err := InspectWorkspaceRoot(root)
	if err != nil {
		t.Fatalf("InspectWorkspaceRoot() error = %v", err)
	}
	want := []PackageSelection{
		{Root: filepath.Join(root, "modules", "alpha"), PackageName: "alpha"},
		{Root: root, PackageName: "root"},
	}
	if !reflect.DeepEqual(inspection.Packages, want) {
		t.Fatalf("packages = %#v, want %#v", inspection.Packages, want)
	}
}

func TestInspectWorkspaceRootDoesNotSelectPackagesFromSignalsAlone(t *testing.T) {
	root := t.TempDir()
	writeZipFile(t, filepath.Join(root, "package.json"), `{"name":"root"}`)
	writeZipFile(t, filepath.Join(root, "turbo.json"), "{}")
	writeZipFile(t, filepath.Join(root, "nx.json"), "{}")
	writeZipFile(t, filepath.Join(root, "apps", "hidden", "package.json"), `{"name":"hidden"}`)

	inspection, err := InspectWorkspaceRoot(root)
	if err != nil {
		t.Fatalf("InspectWorkspaceRoot() error = %v", err)
	}
	if len(inspection.Packages) != 0 {
		t.Fatalf("packages = %#v, want none", inspection.Packages)
	}
}

func TestInspectWorkspaceRootIgnoresMalformedWorkspaceMetadata(t *testing.T) {
	root := t.TempDir()
	writeZipFile(t, filepath.Join(root, "package.json"), `{"name":"root","workspaces":{"packages":"not-an-array"}}`)
	writeZipFile(t, filepath.Join(root, "pnpm-workspace.yaml"), "packages:\n- apps/*\n")
	writeZipFile(t, filepath.Join(root, "apps", "hidden", "package.json"), `{"name":"hidden"}`)

	inspection, err := InspectWorkspaceRoot(root)
	if err != nil {
		t.Fatalf("InspectWorkspaceRoot() error = %v", err)
	}
	if len(inspection.Packages) != 0 {
		t.Fatalf("packages = %#v, want none", inspection.Packages)
	}
}

func TestDetectProjectSignalsUsesFrozenPriority(t *testing.T) {
	testCases := []struct {
		name        string
		packageJSON string
		files       []string
		want        ProjectKind
	}{
		{
			name:        "uniapp wins over vite and webpack",
			packageJSON: `{"dependencies":{"vite":"1","webpack":"1","@dcloudio/uni-app":"1"}}`,
			files:       []string{"vite.config.ts", "webpack.config.js", "index.html"},
			want:        ProjectKindUniappH5,
		},
		{
			name:        "vite configuration wins over webpack",
			packageJSON: `{"scripts":{"bundle":"webpack"}}`,
			files:       []string{"vite.config.mts"},
			want:        ProjectKindVite,
		},
		{
			name:        "webpack dependency",
			packageJSON: `{"peerDependencies":{"webpack":"1"}}`,
			want:        ProjectKindWebpack,
		},
		{
			name:        "frontend index fallback",
			packageJSON: `{}`,
			files:       []string{"index.html"},
			want:        ProjectKindFrontend,
		},
		{
			name:        "generic fallback",
			packageJSON: `{"scripts":{"build":"echo build"}}`,
			want:        ProjectKindGeneric,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			root := t.TempDir()
			writeZipFile(t, filepath.Join(root, "package.json"), testCase.packageJSON)
			for _, file := range testCase.files {
				writeZipFile(t, filepath.Join(root, file), "")
			}

			signals, err := DetectProjectSignals(root)
			if err != nil {
				t.Fatalf("DetectProjectSignals() error = %v", err)
			}
			if signals.Kind != testCase.want {
				t.Fatalf("kind = %q, want %q; signals = %#v", signals.Kind, testCase.want, signals)
			}
		})
	}
}

func TestBuildSourceSelectionModelRanksViteOutput(t *testing.T) {
	root := t.TempDir()
	writeZipFile(t, filepath.Join(root, "package.json"), `{"name":"web","devDependencies":{"vite":"1"}}`)
	writeZipFile(t, filepath.Join(root, "dist", "index.html"), "<main />")
	writeZipFile(t, filepath.Join(root, "build", "placeholder"), "")
	writeZipFile(t, filepath.Join(root, "out", "placeholder"), "")

	model, err := BuildSourceSelectionModel(root)
	if err != nil {
		t.Fatalf("BuildSourceSelectionModel() error = %v", err)
	}
	if model.Project.Kind != ProjectKindVite {
		t.Fatalf("project kind = %q, want %q", model.Project.Kind, ProjectKindVite)
	}
	if model.Recommended.Relative != "dist" || model.Recommended.Score != 160 {
		t.Fatalf("recommended = %#v, want dist score 160", model.Recommended)
	}
	if model.Confidence != RecommendationHigh {
		t.Fatalf("confidence = %q, want %q", model.Confidence, RecommendationHigh)
	}
}

func TestBuildDirectoryCandidatesScoresEveryKnownOutputConvention(t *testing.T) {
	testCases := []struct {
		name  string
		kind  ProjectKind
		paths map[string]int
	}{
		{
			name: "uniapp h5 conventions",
			kind: ProjectKindUniappH5,
			paths: map[string]int{
				"dist/build/h5":           216,
				"unpackage/dist/build/h5": 232,
				"dist/dev/h5":             166,
				"unpackage/dist/dev/h5":   182,
			},
		},
		{
			name: "vite dist convention",
			kind: ProjectKindVite,
			paths: map[string]int{
				"dist": 136,
			},
		},
		{
			name: "webpack conventions",
			kind: ProjectKindWebpack,
			paths: map[string]int{
				"dist":  134,
				"build": 124,
			},
		},
		{
			name: "generic conventions",
			kind: ProjectKindGeneric,
			paths: map[string]int{
				"dist":    118,
				"build":   108,
				"out":     102,
				"release": 94,
				"public":  54,
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			root := t.TempDir()
			for relative := range testCase.paths {
				if err := os.MkdirAll(filepath.Join(root, relative), 0o700); err != nil {
					t.Fatalf("MkdirAll(%q): %v", relative, err)
				}
			}
			candidates := buildDirectoryCandidates(root, ProjectSignals{Kind: testCase.kind})
			for relative, wantScore := range testCase.paths {
				candidate, ok := candidateByRelative(candidates, relative)
				if !ok {
					t.Fatalf("candidate %q missing from %#v", relative, candidates)
				}
				if candidate.Score != wantScore {
					t.Fatalf("candidate %q score = %d, want %d; candidate = %#v", relative, candidate.Score, wantScore, candidate)
				}
			}
		})
	}
}

func TestBuildDirectoryCandidatesSortsTiesAndAddsFallbackChoices(t *testing.T) {
	root := t.TempDir()
	writeZipFile(t, filepath.Join(root, "alpha", "index.html"), "")
	writeZipFile(t, filepath.Join(root, "beta", "index.html"), "")
	writeZipFile(t, filepath.Join(root, "manual", "placeholder"), "")
	writeZipFile(t, filepath.Join(root, "node_modules", "ignored", "index.html"), "")
	writeZipFile(t, filepath.Join(root, ".git", "ignored", "index.html"), "")

	candidates := buildDirectoryCandidates(root, ProjectSignals{Kind: ProjectKindGeneric})
	if candidates[0].Relative != "alpha" || candidates[1].Relative != "beta" {
		t.Fatalf("tie order = %#v, want alpha then beta", candidates)
	}
	if candidates[0].Score != candidates[1].Score {
		t.Fatalf("tie scores = %d and %d", candidates[0].Score, candidates[1].Score)
	}
	if _, ok := candidateByRelative(candidates, "manual"); ok {
		t.Fatalf("manual directory unexpectedly added when index candidates exist: %#v", candidates)
	}
	if _, ok := candidateByRelative(candidates, "node_modules/ignored"); ok {
		t.Fatalf("ignored node_modules directory scanned: %#v", candidates)
	}
	if _, ok := candidateByRelative(candidates, ".git/ignored"); ok {
		t.Fatalf("ignored VCS directory scanned: %#v", candidates)
	}

	fallbackRoot := t.TempDir()
	writeZipFile(t, filepath.Join(fallbackRoot, "alpha", "placeholder"), "")
	writeZipFile(t, filepath.Join(fallbackRoot, "beta", "placeholder"), "")
	writeZipFile(t, filepath.Join(fallbackRoot, "manual", "nested", "placeholder"), "")
	writeZipFile(t, filepath.Join(fallbackRoot, "coverage", "ignored", "placeholder"), "")
	fallback := buildDirectoryCandidates(fallbackRoot, ProjectSignals{Kind: ProjectKindGeneric})
	for _, relative := range []string{"alpha", "beta", "manual", "."} {
		if _, ok := candidateByRelative(fallback, relative); !ok {
			t.Fatalf("fallback candidate %q missing from %#v", relative, fallback)
		}
	}
	if _, ok := candidateByRelative(fallback, "manual/nested"); ok {
		t.Fatalf("depth-two fallback candidate was included: %#v", fallback)
	}
	if _, ok := candidateByRelative(fallback, "coverage"); ok {
		t.Fatalf("ignored coverage directory was included: %#v", fallback)
	}
}

func TestDeriveRecommendationConfidenceThresholds(t *testing.T) {
	testCases := []struct {
		name       string
		candidates []CandidateDirectory
		want       RecommendationConfidence
	}{
		{name: "high exact threshold", candidates: []CandidateDirectory{{Score: 92}, {Score: 74}}, want: RecommendationHigh},
		{name: "medium exact threshold", candidates: []CandidateDirectory{{Score: 78}, {Score: 70}}, want: RecommendationMedium},
		{name: "medium after high cutoff", candidates: []CandidateDirectory{{Score: 92}, {Score: 75}}, want: RecommendationMedium},
		{name: "low insufficient score", candidates: []CandidateDirectory{{Score: 77}, {Score: 0}}, want: RecommendationLow},
		{name: "no candidates", want: RecommendationLow},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := deriveRecommendationConfidence(testCase.candidates); got != testCase.want {
				t.Fatalf("deriveRecommendationConfidence() = %q, want %q", got, testCase.want)
			}
		})
	}
}

func TestArchiveNameDerivationPreservesLegacyPriorityAndSanitization(t *testing.T) {
	testCases := []struct {
		name  string
		input string
		want  string
	}{
		{name: "final slash leaf", input: "owner/project", want: "project"},
		{name: "illegal whitespace and repeated hyphens", input: " bad<>:\\|?*  name--x ", want: "bad-name-x"},
		{name: "leading and trailing dots", input: "...archive...", want: "archive"},
		{name: "empty after normalization", input: "...", want: "archive"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := SanitizeFileName(testCase.input); got != testCase.want {
				t.Fatalf("SanitizeFileName(%q) = %q, want %q", testCase.input, got, testCase.want)
			}
		})
	}

	for remote, want := range map[string]string{
		"git@github.com:owner/repo.git":       "owner-repo",
		"https://github.com/owner/repo.git":   "owner-repo",
		"ssh://git@github.com/owner/repo.git": "owner-repo",
		"not a remote":                        "",
	} {
		if got := ArchiveNameFromRemoteURL(remote); got != want {
			t.Fatalf("ArchiveNameFromRemoteURL(%q) = %q, want %q", remote, got, want)
		}
	}
	if got := defaultArchiveName("owner-repo", "package-name", filepath.Join("root", "project")); got != "owner-repo" {
		t.Fatalf("remote default = %q", got)
	}
	if got := defaultArchiveName("", "package/name", filepath.Join("root", "project")); got != "name" {
		t.Fatalf("package default = %q", got)
	}
	if got := defaultArchiveName("", "", filepath.Join("root", "project")); got != "project" {
		t.Fatalf("root default = %q", got)
	}
}

func candidateByRelative(candidates []CandidateDirectory, relative string) (CandidateDirectory, bool) {
	for _, candidate := range candidates {
		if candidate.Relative == relative {
			return candidate, true
		}
	}
	return CandidateDirectory{}, false
}

func writeZipFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll(%q): %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
}
