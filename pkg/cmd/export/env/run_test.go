package env

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestModuleObservedExportPhasesAndSafeResult(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("SECRET=do-not-project\nBASE=base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".env.production"), []byte("VALUE=production\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	module, err := New(Dependencies{
		WorkingDirectory: func() (string, error) { return root, nil },
		Selector:         &recordingSelector{},
		Reader:           osReader{},
		Writer:           osWriter{},
		Presenter:        &recordingPresenter{},
	})
	if err != nil {
		t.Fatal(err)
	}
	var phases []string
	var selectedSource string
	observer := &runObserver{
		phase: func(id, _ string, state terminalPhaseState, _ string) {
			phases = append(phases, id+":"+fmt.Sprint(state))
		},
		selected: func(_ Selection, source string, _ bool) { selectedSource = source },
	}
	result, err := module.run(context.Background(), Input{Merge: true}, observer)
	if err != nil || result.Cancelled {
		t.Fatalf("run = (%#v, %v)", result, err)
	}
	want := []string{
		"resolve-directory:0", "resolve-directory:1",
		"discover-environment-files:0", "discover-environment-files:1",
		"read-selected-files:0", "read-selected-files:1",
		"parse-and-merge-values:0", "parse-and-merge-values:1",
		"encode-json:0", "encode-json:1",
	}
	// The final write phase is absent because this invocation targets stdout.
	if !reflect.DeepEqual(phases, want) {
		t.Fatalf("phases = %#v, want %#v", phases, want)
	}
	if observer.output == "" || selectedSource != "unique candidate" {
		t.Fatalf("observer output/source = (%q, %q)", observer.output, selectedSource)
	}
}

func TestModuleExportsNamedEnvironmentToStdout(t *testing.T) {
	workingDirectory := t.TempDir()
	directory := filepath.Join(workingDirectory, "project")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatalf("make input directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, ".env.production"), []byte("VALUE=production\n"), 0o600); err != nil {
		t.Fatalf("write environment file: %v", err)
	}
	presenter := &recordingPresenter{}
	module, err := New(Dependencies{
		WorkingDirectory: func() (string, error) { return workingDirectory, nil },
		Selector:         &recordingSelector{},
		Reader:           osReader{},
		Writer:           osWriter{},
		Presenter:        presenter,
	})
	if err != nil {
		t.Fatalf("New returned an error: %v", err)
	}

	result, err := module.Run(context.Background(), Input{Directory: "project", Environment: "production"})

	if err != nil {
		t.Fatalf("Run returned an error: %v", err)
	}
	if result.Cancelled {
		t.Fatal("Run reported cancellation")
	}
	if len(presenter.outros) != 1 || presenter.outros[0] != "Exported variables:" {
		t.Fatalf("outros = %#v", presenter.outros)
	}
	if len(presenter.printed) != 1 || presenter.printed[0] != "{\n  \"VALUE\": \"production\"\n}" {
		t.Fatalf("printed = %#v", presenter.printed)
	}
}

func TestModuleWritesOutputFromWorkingDirectoryAndOverwrites(t *testing.T) {
	workingDirectory := t.TempDir()
	directory := filepath.Join(workingDirectory, "input")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatalf("make input directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, ".env"), []byte("BASE=base\nSHARED=base\n"), 0o600); err != nil {
		t.Fatalf("write base file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, ".env.production"), []byte("PROD=production\nSHARED=production\n"), 0o600); err != nil {
		t.Fatalf("write environment file: %v", err)
	}
	outputPath := filepath.Join(workingDirectory, "output.json")
	if err := os.WriteFile(outputPath, []byte("obsolete"), 0o600); err != nil {
		t.Fatalf("write old output: %v", err)
	}
	presenter := &recordingPresenter{}
	module, err := New(Dependencies{
		WorkingDirectory: func() (string, error) { return workingDirectory, nil },
		Selector:         &recordingSelector{},
		Reader:           osReader{},
		Writer:           osWriter{},
		Presenter:        presenter,
	})
	if err != nil {
		t.Fatalf("New returned an error: %v", err)
	}

	_, err = module.Run(context.Background(), Input{
		Directory:   "input",
		Environment: "production",
		Merge:       true,
		Output:      "output.json",
	})

	if err != nil {
		t.Fatalf("Run returned an error: %v", err)
	}
	content, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	want := "{\n  \"BASE\": \"base\",\n  \"PROD\": \"production\",\n  \"SHARED\": \"production\"\n}"
	if string(content) != want {
		t.Fatalf("output = %q, want %q", content, want)
	}
	if len(presenter.outros) != 1 || presenter.outros[0] != "Writing output to output.json" {
		t.Fatalf("outros = %#v", presenter.outros)
	}
	if len(presenter.printed) != 0 {
		t.Fatalf("printed = %#v", presenter.printed)
	}
}

func TestModuleReportsCancellationWithoutReadingOrWriting(t *testing.T) {
	workingDirectory := t.TempDir()
	if err := os.WriteFile(filepath.Join(workingDirectory, ".env.production"), []byte("VALUE=production\n"), 0o600); err != nil {
		t.Fatalf("write environment file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workingDirectory, ".env.local"), []byte("VALUE=local\n"), 0o600); err != nil {
		t.Fatalf("write environment file: %v", err)
	}
	selector := &recordingSelector{cancel: true}
	reader := &countingFailureReader{}
	writer := &countingFailureWriter{}
	presenter := &recordingPresenter{}
	module, err := New(Dependencies{
		WorkingDirectory: func() (string, error) { return workingDirectory, nil },
		Selector:         selector,
		Reader:           reader,
		Writer:           writer,
		Presenter:        presenter,
	})
	if err != nil {
		t.Fatalf("New returned an error: %v", err)
	}

	result, err := module.Run(context.Background(), Input{})

	if err != nil {
		t.Fatalf("Run returned an error: %v", err)
	}
	if !result.Cancelled {
		t.Fatal("Run did not report cancellation")
	}
	if reader.calls != 0 || writer.calls != 0 {
		t.Fatalf("reader calls = %d, writer calls = %d", reader.calls, writer.calls)
	}
	if len(presenter.alerts) != 1 || presenter.alerts[0] != "Cancelled" {
		t.Fatalf("alerts = %#v", presenter.alerts)
	}
}

func TestModuleReturnsSelectorFailureWithoutReadingWritingOrPresenting(t *testing.T) {
	workingDirectory := t.TempDir()
	if err := os.WriteFile(filepath.Join(workingDirectory, ".env"), []byte("BASE=base\n"), 0o600); err != nil {
		t.Fatalf("write base environment file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workingDirectory, ".env.production"), []byte("PROD=production\n"), 0o600); err != nil {
		t.Fatalf("write selected environment file: %v", err)
	}
	failure := errors.New("interactive terminal unavailable")
	selector := &recordingSelector{err: failure}
	reader := &countingFailureReader{}
	writer := &countingFailureWriter{}
	presenter := &recordingPresenter{}
	module, err := New(Dependencies{
		WorkingDirectory: func() (string, error) { return workingDirectory, nil },
		Selector:         selector,
		Reader:           reader,
		Writer:           writer,
		Presenter:        presenter,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := module.Run(context.Background(), Input{Output: "output.json"})

	if result != (Result{}) || !errors.Is(err, failure) {
		t.Fatalf("Run() = (%#v, %v), want selector failure", result, err)
	}
	if reader.calls != 0 || writer.calls != 0 || len(presenter.outros) != 0 || len(presenter.printed) != 0 || len(presenter.alerts) != 0 {
		t.Fatalf("selector failure performed work: reader=%d writer=%d presenter=%#v", reader.calls, writer.calls, presenter)
	}
}

func TestModuleReportsOutputTargetBeforeMissingParentFailure(t *testing.T) {
	workingDirectory := t.TempDir()
	if err := os.WriteFile(filepath.Join(workingDirectory, ".env"), []byte("VALUE=value\n"), 0o600); err != nil {
		t.Fatalf("write environment file: %v", err)
	}
	presenter := &recordingPresenter{}
	module, err := New(Dependencies{
		WorkingDirectory: func() (string, error) { return workingDirectory, nil },
		Selector:         &recordingSelector{},
		Reader:           osReader{},
		Writer:           osWriter{},
		Presenter:        presenter,
	})
	if err != nil {
		t.Fatalf("New returned an error: %v", err)
	}

	_, err = module.Run(context.Background(), Input{Output: "missing/output.json"})

	if err == nil {
		t.Fatal("Run returned nil error")
	}
	if len(presenter.outros) != 1 || presenter.outros[0] != "Writing output to missing/output.json" {
		t.Fatalf("outros = %#v", presenter.outros)
	}
}

func TestModuleReturnsPermissionFailureFromWriter(t *testing.T) {
	workingDirectory := t.TempDir()
	if err := os.WriteFile(filepath.Join(workingDirectory, ".env"), []byte("VALUE=value\n"), 0o600); err != nil {
		t.Fatalf("write environment file: %v", err)
	}
	module, err := New(Dependencies{
		WorkingDirectory: func() (string, error) { return workingDirectory, nil },
		Selector:         &recordingSelector{},
		Reader:           osReader{},
		Writer:           permissionWriter{},
		Presenter:        &recordingPresenter{},
	})
	if err != nil {
		t.Fatalf("New returned an error: %v", err)
	}

	_, err = module.Run(context.Background(), Input{Output: "output.json"})

	if !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("Run error = %v, want permission error", err)
	}
}

type osReader struct{}

func (osReader) ReadFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

type osWriter struct{}

func (osWriter) WriteFile(path string, content []byte) error {
	return os.WriteFile(path, content, 0o666)
}

type countingFailureReader struct {
	calls int
}

func (reader *countingFailureReader) ReadFile(string) ([]byte, error) {
	reader.calls++
	return nil, errors.New("reader must not run")
}

type countingFailureWriter struct {
	calls int
}

func (writer *countingFailureWriter) WriteFile(string, []byte) error {
	writer.calls++
	return errors.New("writer must not run")
}

type permissionWriter struct{}

func (permissionWriter) WriteFile(string, []byte) error {
	return fs.ErrPermission
}
