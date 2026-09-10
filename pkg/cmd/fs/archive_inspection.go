package fs

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/hackycy/hackycy-cli/internal/sevenzipruntime"
)

const (
	maxArchiveSafeInteger = int64(1<<53 - 1)
	maxArchiveErrorOutput = 64 * 1024
)

var archiveProgressPattern = regexp.MustCompile(`(?:^|\r|\n)\s*(\d{1,3})%`)

type ArchiveInspection struct {
	UncompressedBytes int64
	EntryCount        int64
}

type SevenZipArchiveInspector struct {
	executable func() (string, error)
}

func NewSevenZipArchiveInspector() *SevenZipArchiveInspector {
	return newSevenZipArchiveInspector(sevenzipruntime.Ensure)
}

func newSevenZipArchiveInspector(executable func() (string, error)) *SevenZipArchiveInspector {
	return &SevenZipArchiveInspector{executable: executable}
}

func (inspector *SevenZipArchiveInspector) Inspect(ctx context.Context, source string) (ArchiveInspection, error) {
	if err := ctx.Err(); err != nil {
		return ArchiveInspection{}, err
	}
	executable, err := inspector.executable()
	if err != nil {
		return ArchiveInspection{}, err
	}
	command := exec.CommandContext(ctx, executable, "l", "-slt", "-sccUTF-8", "--", source)
	command.Env = archiveCommandEnvironment(os.Environ())
	stdout, err := command.StdoutPipe()
	if err != nil {
		return ArchiveInspection{}, fmt.Errorf("open 7-Zip inspection output: %w", err)
	}
	stderr := &archiveErrorTail{limit: maxArchiveErrorOutput}
	command.Stderr = stderr
	if err := command.Start(); err != nil {
		return ArchiveInspection{}, archiveFailure(-1, err.Error())
	}
	inspection, parseErr := parseArchiveInspection(stdout)
	if parseErr != nil && command.Process != nil {
		_ = command.Process.Kill()
	}
	waitErr := command.Wait()
	if err := ctx.Err(); err != nil {
		return ArchiveInspection{}, err
	}
	if parseErr != nil {
		return ArchiveInspection{}, parseErr
	}
	if waitErr != nil {
		var exitError *exec.ExitError
		if errors.As(waitErr, &exitError) {
			return ArchiveInspection{}, archiveFailure(exitError.ExitCode(), stderr.String())
		}
		return ArchiveInspection{}, archiveFailure(-1, stderr.String())
	}
	return inspection, nil
}

func (inspector *SevenZipArchiveInspector) Extract(ctx context.Context, source, destination string, progress func(int)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	executable, err := inspector.executable()
	if err != nil {
		return err
	}
	command := exec.CommandContext(ctx, executable, "x", "-y", "-sccUTF-8", "-bso0", "-bse2", "-bsp1", "-o"+destination, "--", source)
	command.Env = archiveCommandEnvironment(os.Environ())
	stdout, err := command.StdoutPipe()
	if err != nil {
		return fmt.Errorf("open 7-Zip extraction output: %w", err)
	}
	stderr := &archiveErrorTail{limit: maxArchiveErrorOutput}
	command.Stderr = stderr
	if err := command.Start(); err != nil {
		return archiveFailure(-1, err.Error())
	}
	progressErr := readArchiveProgress(stdout, progress)
	if progressErr != nil && command.Process != nil {
		_ = command.Process.Kill()
	}
	waitErr := command.Wait()
	if err := ctx.Err(); err != nil {
		return err
	}
	if progressErr != nil {
		return progressErr
	}
	if waitErr != nil {
		var exitError *exec.ExitError
		if errors.As(waitErr, &exitError) {
			return archiveFailure(exitError.ExitCode(), stderr.String())
		}
		return archiveFailure(-1, stderr.String())
	}
	if progress != nil {
		progress(100)
	}
	return nil
}

func archiveCommandEnvironment(environment []string) []string {
	result := make([]string, 0, len(environment)+2)
	for _, entry := range environment {
		if !strings.HasPrefix(entry, "LANG=") && !strings.HasPrefix(entry, "LC_ALL=") {
			result = append(result, entry)
		}
	}
	return append(result, "LANG=C", "LC_ALL=C")
}

func readArchiveProgress(reader io.Reader, progress func(int)) error {
	buffer := make([]byte, 0, 128)
	chunk := make([]byte, 4096)
	for {
		count, err := reader.Read(chunk)
		if count > 0 {
			buffer = append(buffer, chunk[:count]...)
			matches := archiveProgressPattern.FindAllSubmatch(buffer, -1)
			if len(matches) > 0 && progress != nil {
				value, _ := strconv.Atoi(string(matches[len(matches)-1][1]))
				progress(min(value, 100))
			}
			if len(buffer) > 128 {
				buffer = append(buffer[:0], buffer[len(buffer)-128:]...)
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read 7-Zip extraction output: %w", err)
		}
	}
}

func parseArchiveInspection(reader io.Reader) (ArchiveInspection, error) {
	parser := archiveInspectionParser{}
	buffer := bufio.NewReader(reader)
	for {
		line, err := buffer.ReadString('\n')
		if len(line) > 0 {
			if parseErr := parser.parseLine(strings.TrimSuffix(line, "\n")); parseErr != nil {
				return ArchiveInspection{}, parseErr
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return ArchiveInspection{}, fmt.Errorf("read 7-Zip inspection output: %w", err)
		}
	}
	if err := parser.finishEntry(); err != nil {
		return ArchiveInspection{}, err
	}
	return parser.inspection, nil
}

type archiveInspectionParser struct {
	entriesStarted bool
	current        map[string]string
	inspection     ArchiveInspection
}

func (parser *archiveInspectionParser) parseLine(line string) error {
	line = strings.TrimSuffix(line, "\r")
	if archiveEntryDivider(line) {
		if err := parser.finishEntry(); err != nil {
			return err
		}
		parser.entriesStarted = true
		return nil
	}
	separator := strings.Index(line, " = ")
	if !parser.entriesStarted {
		if separator == -1 {
			return nil
		}
		key, value := line[:separator], line[separator+3:]
		if key == "Type" && value == "Split" || key == "Volumes" && archiveNumber(value) > 1 {
			return &ServiceError{Code: "INVALID_ARCHIVE", Message: "Multi-volume archives are not supported"}
		}
		return nil
	}
	if strings.TrimSpace(line) == "" {
		return parser.finishEntry()
	}
	if separator != -1 {
		if parser.current == nil {
			parser.current = make(map[string]string)
		}
		parser.current[line[:separator]] = line[separator+3:]
	}
	return nil
}

func (parser *archiveInspectionParser) finishEntry() error {
	if parser.current == nil || parser.current["Path"] == "" {
		parser.current = nil
		return nil
	}
	if parser.current["Encrypted"] == "+" {
		return &ServiceError{Code: "ENCRYPTED_ARCHIVE", Message: "Encrypted archives are not supported"}
	}
	size, ok := archiveSafeInteger(parser.current["Size"])
	if !ok || parser.inspection.UncompressedBytes+size > maxArchiveSafeInteger || parser.inspection.EntryCount == maxArchiveSafeInteger {
		return &ServiceError{Code: "INVALID_ARCHIVE", Message: "Archive reports an invalid unpacked size"}
	}
	parser.inspection.UncompressedBytes += size
	parser.inspection.EntryCount++
	parser.current = nil
	return nil
}

func archiveEntryDivider(line string) bool {
	if len(line) < 10 {
		return false
	}
	for _, character := range line {
		if character != '-' {
			return false
		}
	}
	return true
}

func archiveSafeInteger(raw string) (int64, bool) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return 0, true
	}
	if integer, err := strconv.ParseInt(value, 0, 64); err == nil {
		return integer, integer >= 0 && integer <= maxArchiveSafeInteger
	}
	number, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(number) || math.IsInf(number, 0) || math.Trunc(number) != number || number < 0 || number > float64(maxArchiveSafeInteger) {
		return 0, false
	}
	return int64(number), true
}

func archiveNumber(raw string) float64 {
	number, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil {
		return math.NaN()
	}
	return number
}

func archiveFailure(exitCode int, output string) *ServiceError {
	normalized := strings.ToLower(output)
	cause := fmt.Errorf("7-Zip exited with code %d", exitCode)
	if output != "" {
		cause = fmt.Errorf("7-Zip exited with code %d\n%s", exitCode, output)
	}
	if strings.Contains(normalized, "wrong password") || strings.Contains(normalized, "enter password") || strings.Contains(normalized, "encrypted") {
		return &ServiceError{Code: "ENCRYPTED_ARCHIVE", Message: "Encrypted archives are not supported", Cause: cause}
	}
	if strings.Contains(normalized, "no space left") || strings.Contains(normalized, "not enough space on the disk") || strings.Contains(normalized, "disk full") {
		return &ServiceError{Code: "INSUFFICIENT_SPACE", Message: "Archive extraction ran out of disk space", Cause: cause}
	}
	if strings.Contains(normalized, "dangerous link") || strings.Contains(normalized, "incorrect link path") || strings.Contains(normalized, "empty link") {
		return &ServiceError{Code: "UNAVAILABLE", Message: "7-Zip rejected an unsafe symbolic link", Cause: cause}
	}
	if archiveDamagedOutput(normalized) {
		return &ServiceError{Code: "INVALID_ARCHIVE", Message: "Archive is invalid, damaged, or unsupported", Cause: cause}
	}
	if strings.Contains(normalized, "system error") || strings.Contains(normalized, "i/o error") || strings.Contains(normalized, "input/output error") || strings.Contains(normalized, "permission denied") || strings.Contains(normalized, "access is denied") {
		return &ServiceError{Code: "UNAVAILABLE", Message: "7-Zip could not access the archive or destination", Cause: cause}
	}
	switch exitCode {
	case 1:
		return &ServiceError{Code: "UNAVAILABLE", Message: "7-Zip reported warnings; extracted output was not published", Cause: cause}
	case 7:
		return &ServiceError{Code: "UNAVAILABLE", Message: "7-Zip command invocation failed", Cause: cause}
	case 8:
		return &ServiceError{Code: "UNAVAILABLE", Message: "7-Zip ran out of memory", Cause: cause}
	case 255:
		return &ServiceError{Code: "UNAVAILABLE", Message: "7-Zip was interrupted", Cause: cause}
	default:
		return &ServiceError{Code: "UNAVAILABLE", Message: "7-Zip could not process the archive", Cause: cause}
	}
}

func archiveDamagedOutput(output string) bool {
	return strings.Contains(output, "crc failed") || strings.Contains(output, "data error") || strings.Contains(output, "headers error") || strings.Contains(output, "unexpected end") || strings.Contains(output, "is not archive") || strings.Contains(output, "unsupported method") || strings.Contains(output, "can not open ") && strings.Contains(output, " as [") && strings.Contains(output, "] archive")
}

type archiveErrorTail struct {
	bytes []byte
	limit int
}

func (tail *archiveErrorTail) Write(value []byte) (int, error) {
	if len(value) >= tail.limit {
		tail.bytes = append(tail.bytes[:0], value[len(value)-tail.limit:]...)
		return len(value), nil
	}
	if len(tail.bytes)+len(value) > tail.limit {
		copy(tail.bytes, tail.bytes[len(tail.bytes)+len(value)-tail.limit:])
		tail.bytes = tail.bytes[:tail.limit-len(value)]
	}
	tail.bytes = append(tail.bytes, value...)
	return len(value), nil
}

func (tail *archiveErrorTail) String() string {
	return string(tail.bytes)
}
