package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var stableVersionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

type stableVersion struct {
	Major uint64
	Minor uint64
	Patch uint64
}

func parseStableVersion(value string) (stableVersion, error) {
	if !stableVersionPattern.MatchString(value) {
		return stableVersion{}, fmt.Errorf("version must be stable X.Y.Z without leading zeroes: %q", value)
	}
	parts := strings.Split(value, ".")
	parsed := stableVersion{}
	values := []*uint64{&parsed.Major, &parsed.Minor, &parsed.Patch}
	for index, part := range parts {
		number, err := strconv.ParseUint(part, 10, 64)
		if err != nil {
			return stableVersion{}, fmt.Errorf("version component %q is too large: %w", part, err)
		}
		*values[index] = number
	}
	return parsed, nil
}

func (version stableVersion) String() string {
	return fmt.Sprintf("%d.%d.%d", version.Major, version.Minor, version.Patch)
}

func compareStableVersions(left, right stableVersion) int {
	for _, pair := range [][2]uint64{{left.Major, right.Major}, {left.Minor, right.Minor}, {left.Patch, right.Patch}} {
		if pair[0] < pair[1] {
			return -1
		}
		if pair[0] > pair[1] {
			return 1
		}
	}
	return 0
}

type bumpKind string

const (
	bumpMajor        bumpKind = "major"
	bumpMinor        bumpKind = "minor"
	bumpPatch        bumpKind = "patch"
	bumpNext         bumpKind = "next"
	bumpConventional bumpKind = "conventional"
)

type versionCandidate struct {
	Kind    bumpKind
	Version string
}

func candidates(current stableVersion, conventional *bumpKind) []versionCandidate {
	major := stableVersion{Major: current.Major + 1}
	minor := stableVersion{Major: current.Major, Minor: current.Minor + 1}
	patch := stableVersion{Major: current.Major, Minor: current.Minor, Patch: current.Patch + 1}
	result := []versionCandidate{
		{Kind: bumpMajor, Version: major.String()},
		{Kind: bumpMinor, Version: minor.String()},
		{Kind: bumpPatch, Version: patch.String()},
		{Kind: bumpNext, Version: patch.String()},
	}
	if conventional != nil {
		var version stableVersion
		switch *conventional {
		case bumpMajor:
			version = major
		case bumpMinor:
			version = minor
		case bumpPatch:
			version = patch
		}
		result = append(result, versionCandidate{Kind: bumpConventional, Version: version.String()})
	}
	return result
}

func conventionalBump(messages string) (bumpKind, bool) {
	var selected bumpKind
	for _, record := range strings.Split(messages, "\x1e") {
		record = strings.TrimSpace(record)
		if record == "" {
			continue
		}
		parts := strings.SplitN(record, "\x1f", 2)
		if len(parts) != 2 {
			continue
		}
		subject, body := strings.TrimSpace(parts[0]), parts[1]
		breakingFooter := strings.Contains(body, "BREAKING CHANGE:") || strings.Contains(body, "BREAKING-CHANGE:")
		kind, breaking, ok := parseConventionalSubject(subject)
		if breakingFooter || (ok && breaking) {
			return bumpMajor, true
		}
		if !ok {
			continue
		}
		candidate := bumpKind("")
		switch kind {
		case "feat":
			candidate = bumpMinor
		case "fix", "perf":
			candidate = bumpPatch
		default:
			continue
		}
		if selected == "" || (selected == bumpPatch && candidate == bumpMinor) {
			selected = candidate
		}
	}
	return selected, selected != ""
}

func parseConventionalSubject(subject string) (kind string, breaking bool, ok bool) {
	colon := strings.IndexByte(subject, ':')
	if colon <= 0 {
		return "", false, false
	}
	header := strings.TrimSpace(subject[:colon])
	if strings.HasSuffix(header, "!") {
		breaking = true
		header = strings.TrimSuffix(header, "!")
	}
	if open := strings.IndexByte(header, '('); open >= 0 {
		if !strings.HasSuffix(header, ")") || open == 0 {
			return "", false, false
		}
		header = header[:open]
	}
	if header == "" || strings.ContainsAny(header, " !@#$%^&*[]{}") {
		return "", false, false
	}
	return header, breaking, true
}
