package fsthumbnail

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"

	"github.com/gen2brain/vpx/webp"
)

func TestRunThumbnailWorkerServesSequentialRequests(t *testing.T) {
	source := thumbnailWorkerPNG(t)
	var input bytes.Buffer
	for _, request := range []thumbnailWorkerRequest{
		{id: 7, mimeType: "image/png", source: source},
		{id: 8, mimeType: "image/png", source: source},
	} {
		if err := writeThumbnailWorkerRequest(&input, request); err != nil {
			t.Fatal(err)
		}
	}
	var output bytes.Buffer
	if err := RunThumbnailWorker(&input, &output); err != nil {
		t.Fatal(err)
	}
	for _, identifier := range []uint64{7, 8} {
		response, err := readThumbnailWorkerResponse(&output)
		if err != nil {
			t.Fatal(err)
		}
		if !response.ok || response.id != identifier {
			t.Fatalf("response = %#v", response)
		}
		decoded, err := webp.Decode(bytes.NewReader(response.payload))
		if err != nil || decoded.Bounds() != image.Rect(0, 0, thumbnailDimension, thumbnailDimension) {
			t.Fatalf("thumbnail = %#v, %v", decoded, err)
		}
	}
}

func TestRunThumbnailWorkerReturnsConversionError(t *testing.T) {
	var input bytes.Buffer
	if err := writeThumbnailWorkerRequest(&input, thumbnailWorkerRequest{id: 42, mimeType: "image/svg+xml", source: []byte("<svg/>")}); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := RunThumbnailWorker(&input, &output); err != nil {
		t.Fatal(err)
	}
	response, err := readThumbnailWorkerResponse(&output)
	if err != nil {
		t.Fatal(err)
	}
	if response.id != 42 || response.ok || string(response.payload) != "Thumbnail format is not supported" {
		t.Fatalf("response = %#v", response)
	}
}

func TestRunThumbnailWorkerRejectsMalformedAndOversizedFrames(t *testing.T) {
	for name, input := range map[string][]byte{
		"truncated": {0, 0, 0, 1},
		"oversized": thumbnailWorkerOversizedFrame(),
		"malformed": thumbnailWorkerMalformedRequestFrame(),
	} {
		t.Run(name, func(t *testing.T) {
			err := RunThumbnailWorker(bytes.NewReader(input), &bytes.Buffer{})
			if err == nil || !strings.Contains(err.Error(), "thumbnail worker:") {
				t.Fatalf("RunThumbnailWorker() error = %v", err)
			}
		})
	}
}

func TestIsThumbnailWorkerInvocationRequiresExactPrivateArgument(t *testing.T) {
	for _, test := range []struct {
		args []string
		want bool
	}{
		{args: []string{thumbnailWorkerArgument}, want: true},
		{args: nil, want: false},
		{args: []string{thumbnailWorkerArgument, "extra"}, want: false},
		{args: []string{"fs", thumbnailWorkerArgument}, want: false},
	} {
		if got := IsThumbnailWorkerInvocation(test.args); got != test.want {
			t.Fatalf("IsThumbnailWorkerInvocation(%q) = %t, want %t", test.args, got, test.want)
		}
	}
}

func thumbnailWorkerPNG(t *testing.T) []byte {
	t.Helper()
	image := image.NewNRGBA(image.Rect(0, 0, 4, 2))
	image.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 255})
	var output bytes.Buffer
	if err := png.Encode(&output, image); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func thumbnailWorkerOversizedFrame() []byte {
	var frame [4]byte
	binary.BigEndian.PutUint32(frame[:], uint32(maxThumbnailWorkerRequestFrameBytes+1))
	return frame[:]
}

func thumbnailWorkerMalformedRequestFrame() []byte {
	var frame bytes.Buffer
	if err := writeThumbnailWorkerFrame(&frame, make([]byte, thumbnailWorkerRequestHeaderBytes-1)); err != nil {
		panic(err)
	}
	return frame.Bytes()
}
