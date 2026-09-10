package heat

import (
	"bytes"
	"fmt"
	"strconv"
)

// ChangeKind identifies a Git name-status change preserved by heat.
type ChangeKind byte

const (
	ChangeModified ChangeKind = 'M'
	ChangeAdded    ChangeKind = 'A'
	ChangeDeleted  ChangeKind = 'D'
	ChangeRenamed  ChangeKind = 'R'
	ChangeCopied   ChangeKind = 'C'
)

// Change is one supported path occurrence in a Git log range.
type Change struct {
	Kind           ChangeKind
	Path           string
	ChangedAt      string
	ChangedAtEpoch int64
}

// Log is the path-safe parsed representation of one Git log range.
type Log struct {
	CommitCount int
	Changes     []Change
}

// ParseLog decodes the command's NUL-delimited Git log output.
func ParseLog(output []byte) (Log, error) {
	var log Log
	var current commitTime
	hasCommit := false
	tokens := bytes.Split(output, []byte{0})

	for index := 0; index < len(tokens); index++ {
		token := tokens[index]
		headerToken := bytes.TrimLeft(token, "\n")
		if bytes.HasPrefix(headerToken, []byte(heatCommitMarker)) {
			parsed, err := parseCommitTime(headerToken)
			if err != nil {
				return Log{}, err
			}
			current = parsed
			hasCommit = true
			log.CommitCount++
			continue
		}
		if len(token) == 0 || bytes.Equal(token, []byte("\n")) {
			continue
		}
		if !hasCommit {
			return Log{}, fmt.Errorf("invalid git heat log record before commit header")
		}

		status := bytes.TrimLeft(token, "\n")
		if len(status) == 0 {
			continue
		}
		if index+1 >= len(tokens) {
			return Log{}, fmt.Errorf("missing path for git heat status %q", status)
		}
		kind := ChangeKind(status[0])
		index++
		path := tokens[index]
		if len(path) == 0 {
			return Log{}, fmt.Errorf("missing path for git heat status %q", status)
		}
		if kind == ChangeRenamed || kind == ChangeCopied {
			if index+1 >= len(tokens) {
				return Log{}, fmt.Errorf("missing destination path for git heat status %q", status)
			}
			index++
			path = tokens[index]
			if len(path) == 0 {
				return Log{}, fmt.Errorf("missing destination path for git heat status %q", status)
			}
		}
		if !isSupportedKind(kind) {
			continue
		}
		log.Changes = append(log.Changes, Change{
			Kind:           kind,
			Path:           string(path),
			ChangedAt:      current.label,
			ChangedAtEpoch: current.epoch,
		})
	}

	return log, nil
}

type commitTime struct {
	label string
	epoch int64
}

func parseCommitTime(token []byte) (commitTime, error) {
	fields := bytes.SplitN(bytes.TrimPrefix(token, []byte(heatCommitMarker)), []byte{0x1f}, 3)
	if len(fields) != 3 || len(fields[0]) == 0 {
		return commitTime{}, fmt.Errorf("invalid git heat commit header")
	}
	epoch, err := strconv.ParseInt(string(fields[1]), 10, 64)
	if err != nil {
		return commitTime{}, fmt.Errorf("invalid git heat commit epoch: %w", err)
	}
	label := string(fields[2])
	if len(label) > 19 {
		label = label[:19]
	}
	return commitTime{label: label, epoch: epoch}, nil
}

func isSupportedKind(kind ChangeKind) bool {
	return kind == ChangeModified || kind == ChangeAdded || kind == ChangeDeleted || kind == ChangeRenamed || kind == ChangeCopied
}
