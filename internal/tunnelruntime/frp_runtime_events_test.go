package tunnelruntime

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestFRPDownloadProgressReaderThrottlesByPercentAndTime(t *testing.T) {
	current := time.Unix(1_700_000_000, 0)
	var events []FRPRuntimeEvent
	progress := newFRPDownloadProgressReader(bytes.NewReader(make([]byte, 100)), 100, func(event FRPRuntimeEvent) {
		events = append(events, event)
	}, func() time.Time { return current })
	progress.start()
	readFRPProgressBytes(t, progress, 4)
	readFRPProgressBytes(t, progress, 1)
	current = current.Add(frpDownloadProgressTimeInterval)
	readFRPProgressBytes(t, progress, 1)
	readFRPProgressBytes(t, progress, 94)
	progress.finish()

	if len(events) != 4 {
		t.Fatalf("progress events = %#v", events)
	}
	wantBytes := []int64{0, 5, 6, 100}
	wantPercent := []int{0, 5, 6, 100}
	for index, event := range events {
		if event.ReceivedBytes != wantBytes[index] || event.TotalBytes == nil || *event.TotalBytes != 100 || event.Percent == nil || *event.Percent != wantPercent[index] {
			t.Fatalf("progress event %d = %#v", index, event)
		}
	}
}

func TestFRPDownloadProgressReaderOmitsUnknownLengthAndForcesFinalSample(t *testing.T) {
	current := time.Unix(1_700_000_000, 0)
	var events []FRPRuntimeEvent
	progress := newFRPDownloadProgressReader(bytes.NewReader(make([]byte, 25)), -1, func(event FRPRuntimeEvent) {
		events = append(events, event)
	}, func() time.Time { return current })
	progress.start()
	readFRPProgressBytes(t, progress, 10)
	current = current.Add(frpDownloadProgressTimeInterval)
	readFRPProgressBytes(t, progress, 10)
	readFRPProgressBytes(t, progress, 5)
	progress.finish()

	wantBytes := []int64{0, 20, 25}
	if len(events) != len(wantBytes) {
		t.Fatalf("unknown-length events = %#v", events)
	}
	for index, event := range events {
		if event.ReceivedBytes != wantBytes[index] || event.TotalBytes != nil || event.Percent != nil {
			t.Fatalf("unknown-length event %d = %#v", index, event)
		}
	}
}

func TestFRPDownloadProgressReaderReportsZeroLengthWithoutPercent(t *testing.T) {
	var events []FRPRuntimeEvent
	progress := newFRPDownloadProgressReader(bytes.NewReader(nil), 0, func(event FRPRuntimeEvent) {
		events = append(events, event)
	}, time.Now)
	progress.start()
	progress.finish()
	if len(events) != 2 {
		t.Fatalf("zero-length events = %#v", events)
	}
	for _, event := range events {
		if event.TotalBytes == nil || *event.TotalBytes != 0 || event.Percent != nil || event.ReceivedBytes != 0 {
			t.Fatalf("zero-length event = %#v", event)
		}
	}
}

func TestEnsureFRPRuntimeAtUsesRedirectedResponseContentLength(t *testing.T) {
	archive, artifact := frpTarFixture(t, map[string][]byte{"frpc": []byte("frpc bytes"), "frps": []byte("frps bytes")})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/archive" {
			http.Redirect(writer, request, "/download", http.StatusFound)
			return
		}
		writer.Header().Set("Content-Length", strconv.Itoa(len(archive)))
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write(archive)
	}))
	defer server.Close()
	artifact.Description.URL = server.URL + "/archive"

	var events []FRPRuntimeEvent
	_, err := ensureFRPRuntimeAt(context.Background(), filepath.Join(t.TempDir(), "frp", FRPVersion), artifact, server.Client(), func(context.Context, string) error { return nil }, func(event FRPRuntimeEvent) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatalf("ensure redirected runtime error = %v", err)
	}
	progress := frpRuntimeEventsOfType(events, FRPRuntimeEventDownloadProgress)
	if len(progress) < 2 || progress[0].TotalBytes == nil || *progress[0].TotalBytes != int64(len(archive)) {
		t.Fatalf("redirected response progress = %#v", progress)
	}
	if last := events[len(events)-1]; last.Type != FRPRuntimeEventReady || !last.Downloaded {
		t.Fatalf("redirected response final event = %#v", last)
	}
}

func TestEnsureFRPRuntimeAtReportsFinalProgressBeforeReadFailure(t *testing.T) {
	archive, artifact := frpTarFixture(t, map[string][]byte{"frpc": []byte("frpc bytes"), "frps": []byte("frps bytes")})
	body := &frpFailingReadCloser{contents: archive[:min(8, len(archive))]}
	var events []FRPRuntimeEvent
	_, err := ensureFRPRuntimeAt(context.Background(), filepath.Join(t.TempDir(), "frp", FRPVersion), artifact, &http.Client{Transport: frpRoundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: body, ContentLength: -1, Header: make(http.Header)}, nil
	})}, func(context.Context, string) error { return nil }, func(event FRPRuntimeEvent) {
		events = append(events, event)
	})
	if err == nil || !errors.Is(err, ErrFRPInstall) {
		t.Fatalf("read failure error = %v", err)
	}
	progress := frpRuntimeEventsOfType(events, FRPRuntimeEventDownloadProgress)
	if len(progress) != 2 || progress[1].ReceivedBytes != int64(len(body.contents)) || progress[1].TotalBytes != nil || progress[1].Percent != nil {
		t.Fatalf("read failure progress = %#v", progress)
	}
	if last := events[len(events)-1]; last.Type != FRPRuntimeEventFailed || last.FailureStage != FRPRuntimeFailureDownload {
		t.Fatalf("read failure final event = %#v", last)
	}
}

func readFRPProgressBytes(t *testing.T, reader io.Reader, size int) {
	t.Helper()
	buffer := make([]byte, size)
	if count, err := io.ReadFull(reader, buffer); err != nil || count != size {
		t.Fatalf("read progress bytes = (%d, %v), want %d", count, err, size)
	}
}

type frpFailingReadCloser struct {
	contents []byte
	read     bool
}

func (reader *frpFailingReadCloser) Read(buffer []byte) (int, error) {
	if reader.read {
		return 0, errors.New("fixture read failure")
	}
	reader.read = true
	return copy(buffer, reader.contents), nil
}

func (*frpFailingReadCloser) Close() error { return nil }
