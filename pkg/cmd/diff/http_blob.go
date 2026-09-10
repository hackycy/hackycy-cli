package diff

import (
	"net/http"
	"strings"
)

const svgBlobCSP = "sandbox; default-src 'none'; style-src 'unsafe-inline'"

func (handler *diffHTTPHandler) serveEntryBlob(writer http.ResponseWriter, request *http.Request, rawID, rawSide string) {
	if !requireHTTPMethod(writer, request, http.MethodGet) {
		return
	}
	snapshot, ok := handler.requestSnapshot(writer, request)
	if !ok {
		return
	}
	id, ok := parseHTTPEntryID(writer, rawID)
	if !ok {
		return
	}
	side, ok := parseHTTPComparisonSide(writer, rawSide)
	if !ok {
		return
	}
	blob, err := snapshot.Blob(id, side)
	if err != nil {
		writeHTTPError(writer, http.StatusNotFound, "ENTRY_NOT_FOUND", err.Error())
		return
	}
	if blob.Status != BlobReady {
		httpBlobError(writer, blob.Status)
		return
	}

	csp := diffAPICSP
	if blob.MIMEType == "image/svg+xml" {
		csp = svgBlobCSP
	}
	disposition := "attachment"
	if strings.HasPrefix(blob.MIMEType, "image/") {
		disposition = "inline"
	}
	filename := encodeHTTPBlobFilename(blob.Filename)
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Content-Security-Policy", csp)
	writer.Header().Set("Content-Type", blob.MIMEType)
	writer.Header().Set("Content-Disposition", disposition+`; filename="`+filename+`"; filename*=UTF-8''`+filename)
	writer.Header().Set("Referrer-Policy", "no-referrer")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write(blob.Bytes)
}

func httpBlobError(writer http.ResponseWriter, status BlobStatus) {
	switch status {
	case BlobStale:
		writeHTTPError(writer, http.StatusConflict, "STALE", "Blob is stale")
	case BlobMissing:
		writeHTTPError(writer, http.StatusNotFound, "MISSING", "Blob is missing")
	default:
		writeHTTPError(writer, http.StatusUnsupportedMediaType, "UNAVAILABLE", "Blob is unavailable")
	}
}

func encodeHTTPBlobFilename(filename string) string {
	const hexadecimal = "0123456789ABCDEF"
	var encoded strings.Builder
	for _, value := range []byte(filename) {
		if isHTTPFilenameCharacter(value) {
			encoded.WriteByte(value)
			continue
		}
		encoded.WriteByte('%')
		encoded.WriteByte(hexadecimal[value>>4])
		encoded.WriteByte(hexadecimal[value&0x0f])
	}
	return encoded.String()
}

func isHTTPFilenameCharacter(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9' || strings.ContainsRune("-_.!~*()", rune(value))
}
