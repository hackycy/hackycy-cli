package heat

import (
	"fmt"
	"strings"
)

const defaultLimit = 20

// Target controls whether the report lists files or immediate directories.
type Target string

const (
	TargetFiles       Target = "files"
	TargetDirectories Target = "directories"
)

// Sort controls report row order.
type Sort string

const (
	SortCount Sort = "count"
	SortPath  Sort = "path"
)

// Input is the typed request for git heat before defaults are applied.
type Input struct {
	Limit        *int
	Days         *int
	Target       Target
	Sort         Sort
	RelativeTime bool
	Query        string
}

// Range describes the selected Git log range. Days is nonzero for a day range.
type Range struct {
	Limit int
	Days  int
}

// IsDayRange reports whether the range is expressed in days rather than commits.
func (rangeValue Range) IsDayRange() bool {
	return rangeValue.Days != 0
}

// NormalizedInput is the validated command request used by the command module.
type NormalizedInput struct {
	Range        Range
	Target       Target
	Sort         Sort
	RelativeTime bool
	Query        string
}

// NormalizeInput applies the observed defaults and validates command options.
func NormalizeInput(input Input) (NormalizedInput, error) {
	if input.Limit != nil && input.Days != nil {
		return NormalizedInput{}, fmt.Errorf("Please use either -n/--limit or -d/--days, not both.")
	}

	rangeValue := Range{Limit: defaultLimit}
	if input.Limit != nil {
		if err := validatePositiveInteger(*input.Limit, "-n/--limit"); err != nil {
			return NormalizedInput{}, err
		}
		rangeValue.Limit = *input.Limit
	}
	if input.Days != nil {
		if err := validatePositiveInteger(*input.Days, "-d/--days"); err != nil {
			return NormalizedInput{}, err
		}
		rangeValue = Range{Days: *input.Days}
	}

	target := input.Target
	if target == "" {
		target = TargetDirectories
	}
	if target != TargetFiles && target != TargetDirectories {
		return NormalizedInput{}, fmt.Errorf("'%s' is not a valid report type. Use files or directories.", target)
	}

	sort := input.Sort
	if sort == "" {
		sort = SortPath
	}
	if sort != SortCount && sort != SortPath {
		return NormalizedInput{}, fmt.Errorf("'%s' is not a valid sort. Use count or path.", sort)
	}

	return NormalizedInput{
		Range:        rangeValue,
		Target:       target,
		Sort:         sort,
		RelativeTime: input.RelativeTime,
		Query:        strings.TrimSpace(input.Query),
	}, nil
}

func validatePositiveInteger(value int, option string) error {
	if value <= 0 {
		return fmt.Errorf("%s must be a positive integer.", option)
	}
	return nil
}
