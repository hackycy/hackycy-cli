package heat

import (
	"strings"
	"testing"
)

func TestNormalizeInputAppliesLegacyDefaults(t *testing.T) {
	options, err := NormalizeInput(Input{})
	if err != nil {
		t.Fatalf("NormalizeInput() error = %v", err)
	}
	if options.Range != (Range{Limit: defaultLimit}) {
		t.Fatalf("range = %#v, want default limit %d", options.Range, defaultLimit)
	}
	if options.Target != TargetDirectories {
		t.Fatalf("target = %q, want %q", options.Target, TargetDirectories)
	}
	if options.Sort != SortPath {
		t.Fatalf("sort = %q, want %q", options.Sort, SortPath)
	}
}

func TestNormalizeInputSelectsExplicitLimitOrDayRange(t *testing.T) {
	limit := 3
	days := 2
	testCases := []struct {
		name  string
		input Input
		want  Range
	}{
		{name: "limit", input: Input{Limit: &limit}, want: Range{Limit: 3}},
		{name: "days", input: Input{Days: &days}, want: Range{Days: 2}},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			options, err := NormalizeInput(testCase.input)
			if err != nil {
				t.Fatalf("NormalizeInput() error = %v", err)
			}
			if options.Range != testCase.want {
				t.Fatalf("range = %#v, want %#v", options.Range, testCase.want)
			}
			if options.Range.IsDayRange() != (testCase.want.Days != 0) {
				t.Fatalf("IsDayRange() = %t", options.Range.IsDayRange())
			}
		})
	}
}

func TestNormalizeInputRejectsMutuallyExclusiveRanges(t *testing.T) {
	limit := 1
	days := 1
	_, err := NormalizeInput(Input{Limit: &limit, Days: &days})
	if err == nil || err.Error() != "Please use either -n/--limit or -d/--days, not both." {
		t.Fatalf("NormalizeInput() error = %v", err)
	}
}

func TestNormalizeInputRejectsNonPositiveRanges(t *testing.T) {
	testCases := []struct {
		name  string
		input Input
		want  string
	}{
		{name: "zero limit", input: Input{Limit: integerPointer(0)}, want: "-n/--limit must be a positive integer."},
		{name: "negative limit", input: Input{Limit: integerPointer(-1)}, want: "-n/--limit must be a positive integer."},
		{name: "zero days", input: Input{Days: integerPointer(0)}, want: "-d/--days must be a positive integer."},
		{name: "negative days", input: Input{Days: integerPointer(-1)}, want: "-d/--days must be a positive integer."},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := NormalizeInput(testCase.input)
			if err == nil || err.Error() != testCase.want {
				t.Fatalf("NormalizeInput() error = %v, want %q", err, testCase.want)
			}
		})
	}
}

func TestNormalizeInputValidatesTargetAndSort(t *testing.T) {
	testCases := []struct {
		name  string
		input Input
		want  string
	}{
		{name: "target", input: Input{Target: "other"}, want: "'other' is not a valid report type. Use files or directories."},
		{name: "sort", input: Input{Sort: "other"}, want: "'other' is not a valid sort. Use count or path."},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := NormalizeInput(testCase.input)
			if err == nil || err.Error() != testCase.want {
				t.Fatalf("NormalizeInput() error = %v, want %q", err, testCase.want)
			}
		})
	}
}

func TestNormalizeInputTrimsQueryWithoutFilteringIt(t *testing.T) {
	options, err := NormalizeInput(Input{Query: "  Fix Me  ", RelativeTime: true})
	if err != nil {
		t.Fatalf("NormalizeInput() error = %v", err)
	}
	if options.Query != "Fix Me" {
		t.Fatalf("query = %q", options.Query)
	}
	if !options.RelativeTime {
		t.Fatal("relative time = false, want true")
	}
	if strings.Contains(options.Query, "  ") {
		t.Fatalf("query retained surrounding whitespace: %q", options.Query)
	}
}

func integerPointer(value int) *int {
	return &value
}
