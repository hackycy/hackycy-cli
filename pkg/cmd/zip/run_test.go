package zip

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestModuleRunsTheCompletePlanAndTreatsRevealFailureAsNonfatal(t *testing.T) {
	root := t.TempDir()
	writeZipFile(t, filepath.Join(root, "package.json"), `{"name":"project"}`)
	writeZipFile(t, filepath.Join(root, "index.html"), "<main />")
	module := newZipModule(t, Dependencies{
		Prompter: selectFirstZipPrompter{output: "release"},
		Revealer: revealFunc(func(string) error { return errors.New("headless") }),
	})

	result, err := module.Run(Input{Directory: root, Open: true, WithDir: "bundle"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Kind != ResultCompleted || !result.RevealFailed || result.IncludedCount != 2 || result.CollectedCount != 2 {
		t.Fatalf("result = %#v", result)
	}
	if result.OutputPath != filepath.Join(root, "release.zip") {
		t.Fatalf("output = %q", result.OutputPath)
	}
	if _, err := os.Stat(result.OutputPath); err != nil {
		t.Fatalf("published archive missing: %v", err)
	}
}

func TestModuleMapsPromptCancellationToNormalResult(t *testing.T) {
	root := t.TempDir()
	writeZipFile(t, filepath.Join(root, "package.json"), `{"name":"project"}`)
	module := newZipModule(t, Dependencies{Prompter: cancelledZipPrompter{}})

	result, err := module.Run(Input{Directory: root, Open: true})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Kind != ResultCancelled || result.Plan != nil {
		t.Fatalf("result = %#v", result)
	}
}

func TestModuleStopsBeforeArchiveWorkWhenPrompterFails(t *testing.T) {
	root := t.TempDir()
	writeZipFile(t, filepath.Join(root, "package.json"), `{"name":"project"}`)
	writeZipFile(t, filepath.Join(root, "index.html"), "<main />")
	promptFailure := errors.New("interaction unavailable")
	revealCalls := 0
	module := newZipModule(t, Dependencies{
		Prompter: failingZipPrompter{err: promptFailure},
		Collector: archiveCollectorFunc(func(string, []string) ([]ArchiveEntry, error) {
			t.Fatal("collector called after prompt failure")
			return nil, nil
		}),
		Builder: archiveBuilderFunc(func([]ArchiveEntry, string, string) ([]byte, int, error) {
			t.Fatal("builder called after prompt failure")
			return nil, 0, nil
		}),
		Writer: archiveWriterFunc(func(string, []byte) error {
			t.Fatal("writer called after prompt failure")
			return nil
		}),
		Revealer: revealFunc(func(string) error {
			revealCalls++
			return nil
		}),
	})

	result, err := module.Run(Input{Directory: root, Open: true})
	if !errors.Is(err, promptFailure) || result != (Result{}) || revealCalls != 0 {
		t.Fatalf("Run() = (%#v, %v), reveal calls = %d", result, err, revealCalls)
	}
}

func TestModuleValidatesTheSelectedSourceAfterPlanning(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing")
	module := newZipModule(t, Dependencies{Prompter: selectFirstZipPrompter{output: "archive"}})

	result, err := module.Run(Input{Directory: root, Open: false})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Kind != ResultDirectoryNotFound || result.Plan == nil {
		t.Fatalf("result = %#v", result)
	}
}

func TestModuleMapsAFileSourceToTheLegacyPathNotDirectoryResult(t *testing.T) {
	input := filepath.Join(t.TempDir(), "not-a-directory")
	writeZipFile(t, input, "file")
	module := newZipModule(t, Dependencies{Prompter: selectFirstZipPrompter{output: "archive"}})

	result, err := module.Run(Input{Directory: input, Open: false})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Kind != ResultPathNotDirectory || result.Plan == nil {
		t.Fatalf("result = %#v", result)
	}
}

func TestModuleMapsArchiveFailureBranchesToNormalResults(t *testing.T) {
	root := t.TempDir()
	writeZipFile(t, filepath.Join(root, "package.json"), `{"name":"project"}`)
	writeZipFile(t, filepath.Join(root, "index.html"), "<main />")
	entry := ArchiveEntry{Relative: "index.html", Absolute: filepath.Join(root, "index.html")}
	collectionFailure := errors.New("read directory")
	compressionFailure := errors.New("compress")
	writeFailure := errors.New("write")
	testCases := []struct {
		name         string
		dependencies Dependencies
		want         ResultKind
	}{
		{
			name: "collection failure",
			dependencies: Dependencies{
				Collector: archiveCollectorFunc(func(string, []string) ([]ArchiveEntry, error) { return nil, collectionFailure }),
			},
			want: ResultCollectionFailed,
		},
		{
			name: "no matches",
			dependencies: Dependencies{
				Collector: archiveCollectorFunc(func(string, []string) ([]ArchiveEntry, error) { return []ArchiveEntry{}, nil }),
			},
			want: ResultNoFiles,
		},
		{
			name: "compression failure",
			dependencies: Dependencies{
				Collector: archiveCollectorFunc(func(string, []string) ([]ArchiveEntry, error) { return []ArchiveEntry{entry}, nil }),
				Builder:   archiveBuilderFunc(func([]ArchiveEntry, string, string) ([]byte, int, error) { return nil, 0, compressionFailure }),
			},
			want: ResultCompressionFailed,
		},
		{
			name: "only output remains after filtering",
			dependencies: Dependencies{
				Collector: archiveCollectorFunc(func(string, []string) ([]ArchiveEntry, error) { return []ArchiveEntry{entry}, nil }),
				Builder:   archiveBuilderFunc(func([]ArchiveEntry, string, string) ([]byte, int, error) { return nil, 0, errNoValidArchiveFiles }),
			},
			want: ResultNoValidFiles,
		},
		{
			name: "write failure",
			dependencies: Dependencies{
				Collector: archiveCollectorFunc(func(string, []string) ([]ArchiveEntry, error) { return []ArchiveEntry{entry}, nil }),
				Builder:   archiveBuilderFunc(func([]ArchiveEntry, string, string) ([]byte, int, error) { return []byte("zip"), 1, nil }),
				Writer:    archiveWriterFunc(func(string, []byte) error { return writeFailure }),
			},
			want: ResultWriteFailed,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			dependencies := testCase.dependencies
			dependencies.Prompter = selectFirstZipPrompter{output: "archive"}
			module := newZipModule(t, dependencies)
			result, err := module.Run(Input{Directory: root, Open: false})
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if result.Kind != testCase.want || result.Cause == nil && testCase.want != ResultNoFiles {
				t.Fatalf("result = %#v, want %q", result, testCase.want)
			}
		})
	}
}

func TestModuleMapsCancellationAtEachPlanningPromptToNormalResult(t *testing.T) {
	root := t.TempDir()
	writeZipFile(t, filepath.Join(root, "package.json"), `{"name":"root","workspaces":["apps/*"]}`)
	writeZipFile(t, filepath.Join(root, "apps", "web", "package.json"), `{"name":"web"}`)
	writeZipFile(t, filepath.Join(root, "index.html"), "")

	for _, stage := range []string{"package", "source", "glob", "output"} {
		t.Run(stage, func(t *testing.T) {
			module := newZipModule(t, Dependencies{Prompter: cancelAtZipPrompter{stage: stage}})
			result, err := module.Run(Input{Directory: root, Open: false})
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if result.Kind != ResultCancelled || result.Plan != nil {
				t.Fatalf("result = %#v", result)
			}
		})
	}
}

func TestModuleSkipsRevealWhenOpenIsDisabled(t *testing.T) {
	root := t.TempDir()
	writeZipFile(t, filepath.Join(root, "package.json"), `{"name":"project"}`)
	writeZipFile(t, filepath.Join(root, "index.html"), "")
	revealCalls := 0
	module := newZipModule(t, Dependencies{
		Prompter: selectFirstZipPrompter{output: "archive"},
		Revealer: revealFunc(func(string) error {
			revealCalls++
			return nil
		}),
	})

	result, err := module.Run(Input{Directory: root, Open: false})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Kind != ResultCompleted || result.RevealFailed || revealCalls != 0 {
		t.Fatalf("result = %#v; reveal calls = %d", result, revealCalls)
	}
}

func TestModuleUsesTheSelectedSourceAndPassesWithDirToTheBuilder(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	writeZipFile(t, filepath.Join(root, "package.json"), `{"name":"project"}`)
	writeZipFile(t, filepath.Join(source, "index.html"), "<main />")
	entry := ArchiveEntry{Relative: "index.html", Absolute: filepath.Join(source, "index.html")}
	var collectedDirectory string
	var builtOutput string
	var builtWithDir string
	module := newZipModule(t, Dependencies{
		Prompter: selectSourceZipPrompter{source: source, output: "archive"},
		Collector: archiveCollectorFunc(func(directory string, patterns []string) ([]ArchiveEntry, error) {
			collectedDirectory = directory
			return []ArchiveEntry{entry}, nil
		}),
		Builder: archiveBuilderFunc(func(entries []ArchiveEntry, outputPath, withDir string) ([]byte, int, error) {
			builtOutput = outputPath
			builtWithDir = withDir
			return []byte("zip"), len(entries), nil
		}),
		Writer: archiveWriterFunc(func(string, []byte) error { return nil }),
	})

	result, err := module.Run(Input{Directory: root, Open: false, WithDir: "../unsafe"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Kind != ResultCompleted || collectedDirectory != source || builtOutput != filepath.Join(source, "archive.zip") || builtWithDir != "../unsafe" {
		t.Fatalf("result = %#v, collected = %q, output = %q, withDir = %q", result, collectedDirectory, builtOutput, builtWithDir)
	}
}

func TestNewRequiresAPrompter(t *testing.T) {
	module, err := New(Dependencies{})
	if module != nil || err == nil || err.Error() != "zip prompter is required" {
		t.Fatalf("New() = %#v, %v", module, err)
	}
}

func newZipModule(t *testing.T, dependencies Dependencies) *Module {
	t.Helper()
	module, err := New(dependencies)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return module
}

type selectFirstZipPrompter struct {
	output string
}

func (prompter selectFirstZipPrompter) SelectPackage(step SelectPackageStep) (string, bool, error) {
	return step.Options[0].Value, false, nil
}

func (prompter selectFirstZipPrompter) SelectSource(step SelectSourceStep) (string, bool, error) {
	return step.Options[0].Value, false, nil
}

func (prompter selectFirstZipPrompter) SelectGlob(SelectGlobStep) ([]string, bool, error) {
	return []string{defaultGlobPattern}, false, nil
}

func (prompter selectFirstZipPrompter) EditOutputFile(EditOutputFileStep) (string, bool, error) {
	return prompter.output, false, nil
}

type selectSourceZipPrompter struct {
	source string
	output string
}

func (prompter selectSourceZipPrompter) SelectPackage(step SelectPackageStep) (string, bool, error) {
	return step.Options[0].Value, false, nil
}

func (prompter selectSourceZipPrompter) SelectSource(SelectSourceStep) (string, bool, error) {
	return prompter.source, false, nil
}

func (prompter selectSourceZipPrompter) SelectGlob(SelectGlobStep) ([]string, bool, error) {
	return []string{defaultGlobPattern}, false, nil
}

func (prompter selectSourceZipPrompter) EditOutputFile(EditOutputFileStep) (string, bool, error) {
	return prompter.output, false, nil
}

type cancelledZipPrompter struct{}

func (cancelledZipPrompter) SelectPackage(SelectPackageStep) (string, bool, error) {
	return "", true, nil
}

func (cancelledZipPrompter) SelectSource(SelectSourceStep) (string, bool, error) {
	return "", true, nil
}

func (cancelledZipPrompter) SelectGlob(SelectGlobStep) ([]string, bool, error) {
	return nil, true, nil
}

func (cancelledZipPrompter) EditOutputFile(EditOutputFileStep) (string, bool, error) {
	return "", true, nil
}

type failingZipPrompter struct {
	err error
}

func (prompter failingZipPrompter) SelectPackage(SelectPackageStep) (string, bool, error) {
	return "", false, prompter.err
}

func (prompter failingZipPrompter) SelectSource(SelectSourceStep) (string, bool, error) {
	return "", false, prompter.err
}

func (prompter failingZipPrompter) SelectGlob(SelectGlobStep) ([]string, bool, error) {
	return nil, false, prompter.err
}

func (prompter failingZipPrompter) EditOutputFile(EditOutputFileStep) (string, bool, error) {
	return "", false, prompter.err
}

type cancelAtZipPrompter struct {
	stage string
}

func (prompter cancelAtZipPrompter) SelectPackage(step SelectPackageStep) (string, bool, error) {
	return step.Options[0].Value, prompter.stage == "package", nil
}

func (prompter cancelAtZipPrompter) SelectSource(step SelectSourceStep) (string, bool, error) {
	return step.Options[0].Value, prompter.stage == "source", nil
}

func (prompter cancelAtZipPrompter) SelectGlob(SelectGlobStep) ([]string, bool, error) {
	return []string{defaultGlobPattern}, prompter.stage == "glob", nil
}

func (prompter cancelAtZipPrompter) EditOutputFile(EditOutputFileStep) (string, bool, error) {
	return "archive", prompter.stage == "output", nil
}

type revealFunc func(string) error

func (function revealFunc) Reveal(path string) error {
	return function(path)
}

type pathStaterFunc func(string) (fs.FileInfo, error)

func (function pathStaterFunc) Stat(path string) (fs.FileInfo, error) {
	return function(path)
}
