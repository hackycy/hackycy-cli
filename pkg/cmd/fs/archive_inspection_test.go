package fs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSevenZipArchiveInspectorPassesTheFrozenCommandAndParsesEntries(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test helper is a POSIX shell script")
	}
	source := filepath.Join(t.TempDir(), "archive with spaces.zip")
	executable := fakeSevenZip(t, fmt.Sprintf(`
if [ "$1" != l ] || [ "$2" != -slt ] || [ "$3" != -sccUTF-8 ] || [ "$4" != -- ] || [ "$5" != '%s' ]; then
  echo "unexpected arguments: $*" >&2
  exit 7
fi
if [ "$LANG" != C ] || [ "$LC_ALL" != C ]; then
  echo "unexpected locale: $LANG/$LC_ALL" >&2
  exit 7
fi
printf 'Path = archive.zip\nType = zip\n\n----------\nPath = first.txt\nSize = 3\nEncrypted = -\n\nPath = empty\nSize = 0\n\n----------\nPath = second.txt\nSize = 5\n'
`, shellQuote(source)), "", 0)

	inspection, err := newSevenZipArchiveInspector(func() (string, error) { return executable, nil }).Inspect(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	if inspection != (ArchiveInspection{UncompressedBytes: 8, EntryCount: 3}) {
		t.Fatalf("Inspect() = %#v", inspection)
	}
}

func TestSevenZipArchiveInspectorRejectsUnsafeInspectionMetadata(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test helper is a POSIX shell script")
	}
	for _, test := range []struct {
		name   string
		output string
		code   string
	}{
		{name: "encrypted entry", output: "----------\nPath = secret.txt\nSize = 1\nEncrypted = +\n", code: "ENCRYPTED_ARCHIVE"},
		{name: "split type", output: "Type = Split\n----------\n", code: "INVALID_ARCHIVE"},
		{name: "multiple volumes", output: "Volumes = 2\n----------\n", code: "INVALID_ARCHIVE"},
		{name: "unsafe unpacked size", output: "----------\nPath = huge.bin\nSize = 9007199254740992\n", code: "INVALID_ARCHIVE"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := parseArchiveInspection(strings.NewReader(test.output))
			if !serviceErrorIs(err, test.code) {
				t.Fatalf("parseArchiveInspection() error = %v, want %s", err, test.code)
			}
		})
	}
}

func TestArchiveFailureUsesFrozenNormalizedClasses(t *testing.T) {
	for _, test := range []struct {
		name    string
		exit    int
		output  string
		code    string
		message string
	}{
		{name: "password", exit: 2, output: "Wrong password", code: "ENCRYPTED_ARCHIVE", message: "Encrypted archives are not supported"},
		{name: "space", exit: 2, output: "No space left on device", code: "INSUFFICIENT_SPACE", message: "Archive extraction ran out of disk space"},
		{name: "link", exit: 2, output: "Dangerous link path", code: "UNAVAILABLE", message: "7-Zip rejected an unsafe symbolic link"},
		{name: "damaged", exit: 2, output: "CRC failed", code: "INVALID_ARCHIVE", message: "Archive is invalid, damaged, or unsupported"},
		{name: "access", exit: 2, output: "Permission denied", code: "UNAVAILABLE", message: "7-Zip could not access the archive or destination"},
		{name: "warning", exit: 1, code: "UNAVAILABLE", message: "7-Zip reported warnings; extracted output was not published"},
		{name: "command", exit: 7, code: "UNAVAILABLE", message: "7-Zip command invocation failed"},
		{name: "memory", exit: 8, code: "UNAVAILABLE", message: "7-Zip ran out of memory"},
		{name: "interrupted", exit: 255, code: "UNAVAILABLE", message: "7-Zip was interrupted"},
		{name: "fallback", exit: 2, code: "UNAVAILABLE", message: "7-Zip could not process the archive"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := archiveFailure(test.exit, test.output)
			if err.Code != test.code || err.Message != test.message {
				t.Fatalf("archiveFailure() = %#v", err)
			}
			if err.Cause == nil || !strings.Contains(err.Cause.Error(), test.output) {
				t.Fatalf("archiveFailure() did not retain stderr cause: %#v", err)
			}
		})
	}
}

func TestSevenZipArchiveInspectorNormalizesProcessFailures(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test helper is a POSIX shell script")
	}
	executable := fakeSevenZip(t, ":", "CRC failed", 2)
	_, err := newSevenZipArchiveInspector(func() (string, error) { return executable, nil }).Inspect(context.Background(), "broken.zip")
	if !serviceErrorIs(err, "INVALID_ARCHIVE") {
		t.Fatalf("Inspect() error = %v, want INVALID_ARCHIVE", err)
	}
}

func TestSevenZipArchiveInspectorExtractsWithFrozenArgumentsAndProgress(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test helper is a POSIX shell script")
	}
	source := filepath.Join(t.TempDir(), "archive with spaces.zip")
	destination := filepath.Join(t.TempDir(), ".extract-550e8400-e29b-41d4-a716-446655440000.tmp")
	executable := fakeSevenZip(t, fmt.Sprintf(`
if [ "$1" != x ] || [ "$2" != -y ] || [ "$3" != -sccUTF-8 ] || [ "$4" != -bso0 ] || [ "$5" != -bse2 ] || [ "$6" != -bsp1 ] || [ "$7" != '-o%s' ] || [ "$8" != -- ] || [ "$9" != '%s' ]; then
  echo "unexpected arguments: $*" >&2
  exit 7
fi
if [ "$LANG" != C ] || [ "$LC_ALL" != C ]; then
  echo "unexpected locale: $LANG/$LC_ALL" >&2
  exit 7
fi
printf ' 2%%\r 42%%\r 101%%\r'
`, shellQuote(destination), shellQuote(source)), "", 0)
	var progress []int
	err := newSevenZipArchiveInspector(func() (string, error) { return executable, nil }).Extract(context.Background(), source, destination, func(value int) {
		progress = append(progress, value)
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(progress) == 0 || progress[len(progress)-1] != 100 {
		t.Fatalf("progress = %v, want a final 100", progress)
	}
}

func TestArchiveProgressReaderRetainsCarriageReturnBoundaries(t *testing.T) {
	var progress []int
	if err := readArchiveProgress(io.MultiReader(strings.NewReader("99%\r"), strings.NewReader(" 3%\r")), func(value int) { progress = append(progress, value) }); err != nil {
		t.Fatal(err)
	}
	if got, want := fmt.Sprint(progress), "[99 3]"; got != want {
		t.Fatalf("progress = %s, want %s", got, want)
	}
}

func TestArchiveErrorTailKeepsOnlyTheLast64KiB(t *testing.T) {
	tail := &archiveErrorTail{limit: 4}
	if _, err := tail.Write([]byte("abcdef")); err != nil || tail.String() != "cdef" {
		t.Fatalf("tail after first write = %q, %v", tail.String(), err)
	}
	if _, err := tail.Write([]byte("gh")); err != nil || tail.String() != "efgh" {
		t.Fatalf("tail after second write = %q, %v", tail.String(), err)
	}
}

func TestSevenZipArchiveInspectorReturnsContextCancellation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test helper is a POSIX shell script")
	}
	executable := fakeSevenZip(t, "sleep 10", "", 0)
	testContext, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := newSevenZipArchiveInspector(func() (string, error) { return executable, nil }).Inspect(testContext, "archive.zip")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Inspect() error = %v, want context cancellation", err)
	}
}

func fakeSevenZip(t *testing.T, body, stderr string, exitCode int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "7zz")
	script := "#!/bin/sh\n" + body + "\n"
	if stderr != "" {
		script += "printf '" + shellQuote(stderr) + "' >&2\n"
	}
	script += fmt.Sprintf("exit %d\n", exitCode)
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func shellQuote(value string) string {
	return strings.ReplaceAll(value, "'", "'\\''")
}
