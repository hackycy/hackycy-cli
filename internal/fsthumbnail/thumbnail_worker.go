package fsthumbnail

import (
	"encoding/binary"
	"fmt"
	"io"
	"strings"
)

const (
	thumbnailWorkerArgument              = "--internal-thumbnail-worker"
	maxThumbnailWorkerMIMEBytes          = 128
	maxThumbnailWorkerRequestFrameBytes  = maxThumbnailSourceBytes + maxThumbnailWorkerMIMEBytes + 14
	maxThumbnailWorkerResponseFrameBytes = maxThumbnailSourceBytes + 13
	maxThumbnailWorkerErrorMessageBytes  = 4096
	thumbnailWorkerRequestHeaderBytes    = 14
	thumbnailWorkerResponseHeaderBytes   = 13
)

type thumbnailWorkerRequest struct {
	id       uint64
	mimeType string
	source   []byte
}

type thumbnailWorkerResponse struct {
	id      uint64
	ok      bool
	payload []byte
}

// IsThumbnailWorkerInvocation reports whether args select the private worker mode.
func IsThumbnailWorkerInvocation(args []string) bool {
	return len(args) == 1 && args[0] == thumbnailWorkerArgument
}

// RunThumbnailWorker serves sequential thumbnail conversions over a private length-framed pipe.
func RunThumbnailWorker(input io.Reader, output io.Writer) error {
	for {
		request, err := readThumbnailWorkerRequest(input)
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		thumbnail, err := convertThumbnail(request.mimeType, request.source)
		response := thumbnailWorkerResponse{id: request.id, ok: err == nil, payload: thumbnail}
		if err != nil {
			response.payload = []byte(limitThumbnailWorkerError(err.Error()))
		}
		if err := writeThumbnailWorkerResponse(output, response); err != nil {
			return err
		}
	}
}

func readThumbnailWorkerRequest(input io.Reader) (thumbnailWorkerRequest, error) {
	frame, err := readThumbnailWorkerFrame(input, maxThumbnailWorkerRequestFrameBytes)
	if err != nil {
		return thumbnailWorkerRequest{}, err
	}
	if len(frame) < thumbnailWorkerRequestHeaderBytes {
		return thumbnailWorkerRequest{}, fmt.Errorf("thumbnail worker: malformed request frame")
	}
	request := thumbnailWorkerRequest{id: binary.BigEndian.Uint64(frame[:8])}
	mimeLength := int(binary.BigEndian.Uint16(frame[8:10]))
	sourceLength := int(binary.BigEndian.Uint32(frame[10:14]))
	if mimeLength > maxThumbnailWorkerMIMEBytes || sourceLength > maxThumbnailSourceBytes || len(frame) != thumbnailWorkerRequestHeaderBytes+mimeLength+sourceLength {
		return thumbnailWorkerRequest{}, fmt.Errorf("thumbnail worker: malformed request frame")
	}
	request.mimeType = string(frame[thumbnailWorkerRequestHeaderBytes : thumbnailWorkerRequestHeaderBytes+mimeLength])
	request.source = frame[thumbnailWorkerRequestHeaderBytes+mimeLength:]
	return request, nil
}

func writeThumbnailWorkerRequest(output io.Writer, request thumbnailWorkerRequest) error {
	if len(request.mimeType) > maxThumbnailWorkerMIMEBytes || len(request.source) > maxThumbnailSourceBytes {
		return fmt.Errorf("thumbnail worker: request exceeds frame limits")
	}
	frame := make([]byte, thumbnailWorkerRequestHeaderBytes+len(request.mimeType)+len(request.source))
	binary.BigEndian.PutUint64(frame[:8], request.id)
	binary.BigEndian.PutUint16(frame[8:10], uint16(len(request.mimeType)))
	binary.BigEndian.PutUint32(frame[10:14], uint32(len(request.source)))
	copy(frame[thumbnailWorkerRequestHeaderBytes:], request.mimeType)
	copy(frame[thumbnailWorkerRequestHeaderBytes+len(request.mimeType):], request.source)
	return writeThumbnailWorkerFrame(output, frame)
}

func readThumbnailWorkerResponse(input io.Reader) (thumbnailWorkerResponse, error) {
	frame, err := readThumbnailWorkerFrame(input, maxThumbnailWorkerResponseFrameBytes)
	if err != nil {
		return thumbnailWorkerResponse{}, err
	}
	if len(frame) < thumbnailWorkerResponseHeaderBytes {
		return thumbnailWorkerResponse{}, fmt.Errorf("thumbnail worker: malformed response frame")
	}
	response := thumbnailWorkerResponse{id: binary.BigEndian.Uint64(frame[:8]), ok: frame[8] == 1}
	payloadLength := int(binary.BigEndian.Uint32(frame[9:13]))
	if (frame[8] != 0 && frame[8] != 1) || len(frame) != thumbnailWorkerResponseHeaderBytes+payloadLength {
		return thumbnailWorkerResponse{}, fmt.Errorf("thumbnail worker: malformed response frame")
	}
	response.payload = frame[thumbnailWorkerResponseHeaderBytes:]
	return response, nil
}

func writeThumbnailWorkerResponse(output io.Writer, response thumbnailWorkerResponse) error {
	if len(response.payload) > maxThumbnailWorkerResponseFrameBytes-thumbnailWorkerResponseHeaderBytes {
		return fmt.Errorf("thumbnail worker: response exceeds frame limits")
	}
	frame := make([]byte, thumbnailWorkerResponseHeaderBytes+len(response.payload))
	binary.BigEndian.PutUint64(frame[:8], response.id)
	if response.ok {
		frame[8] = 1
	}
	binary.BigEndian.PutUint32(frame[9:13], uint32(len(response.payload)))
	copy(frame[thumbnailWorkerResponseHeaderBytes:], response.payload)
	return writeThumbnailWorkerFrame(output, frame)
}

func readThumbnailWorkerFrame(input io.Reader, maximum int) ([]byte, error) {
	var header [4]byte
	n, err := io.ReadFull(input, header[:])
	if err == io.EOF && n == 0 {
		return nil, io.EOF
	}
	if err != nil {
		return nil, fmt.Errorf("thumbnail worker: malformed frame length: %w", err)
	}
	length := int(binary.BigEndian.Uint32(header[:]))
	if length > maximum {
		return nil, fmt.Errorf("thumbnail worker: frame exceeds limit")
	}
	frame := make([]byte, length)
	if _, err := io.ReadFull(input, frame); err != nil {
		return nil, fmt.Errorf("thumbnail worker: malformed frame body: %w", err)
	}
	return frame, nil
}

func writeThumbnailWorkerFrame(output io.Writer, frame []byte) error {
	if len(frame) > int(^uint32(0)) {
		return fmt.Errorf("thumbnail worker: frame exceeds uint32 length")
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(frame)))
	if err := writeThumbnailWorkerBytes(output, header[:]); err != nil {
		return fmt.Errorf("thumbnail worker: write frame length: %w", err)
	}
	if err := writeThumbnailWorkerBytes(output, frame); err != nil {
		return fmt.Errorf("thumbnail worker: write frame body: %w", err)
	}
	return nil
}

func writeThumbnailWorkerBytes(output io.Writer, data []byte) error {
	for len(data) > 0 {
		written, err := output.Write(data)
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
		data = data[written:]
	}
	return nil
}

func limitThumbnailWorkerError(message string) string {
	if len(message) <= maxThumbnailWorkerErrorMessageBytes {
		return message
	}
	return strings.ToValidUTF8(message[:maxThumbnailWorkerErrorMessageBytes], "?")
}
