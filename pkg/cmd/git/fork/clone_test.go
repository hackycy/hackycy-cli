package fork

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
)

func TestCloneFallbackBuildsTheLegacyCloneArgumentsAndCleansMetadata(t *testing.T) {
	runner := &fakeCloneRunner{}
	remover := &fakeDirectoryRemover{}
	repository := Repository{
		Host: "github.example", Scheme: "https", Owner: "group/subgroup", Name: "project", Ref: "release/topic", ProviderType: providerGitHub, Token: "token-for-argv",
	}
	if err := CloneFallback(context.Background(), runner, remover, repository, "/tmp/disposable-destination"); err != nil {
		t.Fatalf("CloneFallback() error = %v", err)
	}
	if got, want := runner.arguments, [][]string{{
		"clone", "--depth=1", "--single-branch", "--branch", "release/topic", "https://token-for-argv@github.example/group/subgroup/project.git", "/tmp/disposable-destination",
	}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("clone arguments = %#v, want %#v", got, want)
	}
	if got, want := remover.paths, []string{filepath.Join("/tmp/disposable-destination", ".git")}; !reflect.DeepEqual(got, want) {
		t.Fatalf("metadata cleanup paths = %#v, want %#v", got, want)
	}
}

func TestCloneFallbackUsesTheRemoteDefaultWhenNoRefWasResolved(t *testing.T) {
	runner := &fakeCloneRunner{}
	remover := &fakeDirectoryRemover{}
	repository := Repository{Host: "gitlab.example", Scheme: "http", Owner: "group", Name: "project", ProviderType: providerGitLab}
	if err := CloneFallback(context.Background(), runner, remover, repository, "/tmp/destination"); err != nil {
		t.Fatalf("CloneFallback() error = %v", err)
	}
	if got, want := runner.arguments, [][]string{{
		"clone", "--depth=1", "--single-branch", "http://gitlab.example/group/project.git", "/tmp/destination",
	}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("clone arguments = %#v, want %#v", got, want)
	}
}

func TestCloneFallbackRetainsFailureAndCleanupOutcomes(t *testing.T) {
	tests := []struct {
		name         string
		runner       *fakeCloneRunner
		remover      *fakeDirectoryRemover
		wantErr      string
		wantCleanups int
	}{
		{
			name: "clone stderr", runner: &fakeCloneRunner{output: CloneOutput{ExitCode: 7, Stderr: []byte(" fatal: refused \n")}}, remover: &fakeDirectoryRemover{}, wantErr: "fatal: refused",
		},
		{
			name: "clone exit without stderr", runner: &fakeCloneRunner{output: CloneOutput{ExitCode: 7}}, remover: &fakeDirectoryRemover{}, wantErr: "git clone failed with exit code 7",
		},
		{
			name: "clone process failure", runner: &fakeCloneRunner{err: errors.New("process start failed")}, remover: &fakeDirectoryRemover{}, wantErr: "process start failed",
		},
		{
			name: "metadata cleanup failure", runner: &fakeCloneRunner{}, remover: &fakeDirectoryRemover{err: errors.New("cleanup failed")}, wantErr: "cleanup failed", wantCleanups: 1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := CloneFallback(context.Background(), test.runner, test.remover, Repository{Host: "github.com", Scheme: "https", Owner: "owner", Name: "project", ProviderType: providerGitHub}, "/tmp/destination")
			if err == nil || err.Error() != test.wantErr {
				t.Fatalf("CloneFallback() error = %v, want %q", err, test.wantErr)
			}
			if got := len(test.remover.paths); got != test.wantCleanups {
				t.Fatalf("metadata cleanups = %d, want %d", got, test.wantCleanups)
			}
		})
	}
}

func TestCloneFallbackRequiresItsBoundaries(t *testing.T) {
	repository := Repository{Host: "github.com", Scheme: "https", Owner: "owner", Name: "project", ProviderType: providerGitHub}
	if err := CloneFallback(context.Background(), nil, &fakeDirectoryRemover{}, repository, "/tmp/destination"); err == nil || err.Error() != "git fork clone runner is required" {
		t.Fatalf("nil runner error = %v", err)
	}
	if err := CloneFallback(context.Background(), &fakeCloneRunner{}, nil, repository, "/tmp/destination"); err == nil || err.Error() != "git fork clone metadata remover is required" {
		t.Fatalf("nil remover error = %v", err)
	}
}

type fakeCloneRunner struct {
	events    *[]string
	arguments [][]string
	output    CloneOutput
	err       error
}

func (runner *fakeCloneRunner) Run(_ context.Context, arguments []string) (CloneOutput, error) {
	recordTestEvent(runner.events, "clone")
	runner.arguments = append(runner.arguments, append([]string(nil), arguments...))
	return runner.output, runner.err
}

type fakeDirectoryRemover struct {
	events *[]string
	paths  []string
	err    error
}

func (remover *fakeDirectoryRemover) RemoveAll(path string) error {
	recordTestEvent(remover.events, "remove:"+path)
	remover.paths = append(remover.paths, path)
	return remover.err
}
