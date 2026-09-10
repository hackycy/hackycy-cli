package heat

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestNewRequiresEveryCommandBoundary(t *testing.T) {
	runner := &scriptedGitRunner{}
	now := func() time.Time { return time.Time{} }
	testCases := []struct {
		name string
		deps Dependencies
		want string
	}{
		{name: "runner", deps: Dependencies{Now: now}, want: "git heat runner is required"},
		{name: "clock", deps: Dependencies{Git: runner}, want: "git heat clock is required"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := New(testCase.deps)
			if err == nil || err.Error() != testCase.want {
				t.Fatalf("New() error = %v, want %q", err, testCase.want)
			}
		})
	}
}

func TestModuleRunComposesInputGitReportAndResultTime(t *testing.T) {
	now := time.Date(2024, time.March, 10, 12, 0, 0, 0, time.UTC)
	runner := &scriptedGitRunner{outputs: []GitOutput{
		{Stdout: []byte("/tmp/repo\n")},
		{Stdout: []byte("\x00" + heatCommitMarker + "abc\x1f1710000000\x1f2024-03-09 12:00:00 +0000\x00M\x00src/main.go\x00")},
	}}
	module, err := New(Dependencies{Git: runner, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	result, err := module.Run(context.Background(), Input{Target: TargetFiles, Sort: SortPath, Query: "main"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Report.RepositoryName != "repo" || result.Report.CommitCount != 1 || result.Now != now {
		t.Fatalf("result = %#v", result)
	}
	if got, want := runner.arguments, [][]string{
		{"rev-parse", "--show-toplevel"},
		{"-C", "/tmp/repo", "log", "-n", "20", "--no-color", "--name-status", "-z", "--pretty=format:%x00" + heatCommitMarker + "%H%x1f%ct%x1f%ci%x00"},
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("arguments = %#v, want %#v", got, want)
	}
}

func TestModuleRunStopsAtErrorsWithoutAResult(t *testing.T) {
	runnerFailure := errors.New("git unavailable")
	module, err := New(Dependencies{
		Git: &scriptedGitRunner{err: runnerFailure},
		Now: time.Now,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, err = module.Run(context.Background(), Input{})
	if !errors.Is(err, runnerFailure) {
		t.Fatalf("Run() error = %v, want %v", err, runnerFailure)
	}
}

func TestModuleRunDoesNoWorkWhenContextIsCancelledOrInputIsInvalid(t *testing.T) {
	runner := &scriptedGitRunner{}
	module, err := New(Dependencies{Git: runner, Now: time.Now})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := module.Run(cancelled, Input{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled Run() error = %v", err)
	}
	limit := 1
	days := 1
	if _, err := module.Run(context.Background(), Input{Limit: &limit, Days: &days}); err == nil {
		t.Fatal("invalid Run() error = nil")
	}
	if len(runner.arguments) != 0 {
		t.Fatalf("unexpected work: arguments=%#v", runner.arguments)
	}
}
