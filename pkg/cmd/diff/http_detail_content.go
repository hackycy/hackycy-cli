package diff

import (
	"net/http"
	"strconv"
	"strings"
)

func (handler *diffHTTPHandler) serveEntryResource(writer http.ResponseWriter, request *http.Request) {
	parts := strings.Split(strings.TrimPrefix(request.URL.Path, "/api/entries/"), "/")
	switch {
	case len(parts) == 1:
		handler.serveEntryDetail(writer, request, parts[0])
	case len(parts) == 3 && parts[1] == "content":
		handler.serveEntryContent(writer, request, parts[0], parts[2])
	case len(parts) == 3 && parts[1] == "blob":
		handler.serveEntryBlob(writer, request, parts[0], parts[2])
	default:
		writeHTTPError(writer, http.StatusNotFound, "NOT_FOUND", "API route not found")
	}
}

func (handler *diffHTTPHandler) serveEntryDetail(writer http.ResponseWriter, request *http.Request, rawID string) {
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
	detail, err := snapshot.Detail(id)
	if err != nil {
		writeHTTPError(writer, http.StatusNotFound, "ENTRY_NOT_FOUND", err.Error())
		return
	}
	writeHTTPJSON(writer, http.StatusOK, makeHTTPEntryDetail(detail))
}

func (handler *diffHTTPHandler) serveEntryContent(writer http.ResponseWriter, request *http.Request, rawID, rawSide string) {
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
	content, err := snapshot.Content(id, side, request.URL.Query().Get("force") == "true")
	if err != nil {
		writeHTTPError(writer, http.StatusNotFound, "ENTRY_NOT_FOUND", err.Error())
		return
	}
	writeHTTPJSON(writer, http.StatusOK, makeHTTPTextContent(content))
}

func parseHTTPEntryID(writer http.ResponseWriter, raw string) (int, bool) {
	if raw == "" {
		writeHTTPError(writer, http.StatusBadRequest, "INVALID_REQUEST", "Entry ID must be a positive integer")
		return 0, false
	}
	for _, character := range raw {
		if character < '0' || character > '9' {
			writeHTTPError(writer, http.StatusBadRequest, "INVALID_REQUEST", "Entry ID must be a positive integer")
			return 0, false
		}
	}
	id, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || id == 0 || id > maxJavaScriptSafeInteger || (strconv.IntSize == 32 && id > uint64(^uint(0)>>1)) {
		writeHTTPError(writer, http.StatusBadRequest, "INVALID_REQUEST", "Entry ID must be a positive integer")
		return 0, false
	}
	return int(id), true
}

func parseHTTPComparisonSide(writer http.ResponseWriter, raw string) (ComparisonSide, bool) {
	switch ComparisonSide(raw) {
	case SideBaseline:
		return SideBaseline, true
	case SideTarget:
		return SideTarget, true
	default:
		writeHTTPError(writer, http.StatusBadRequest, "INVALID_REQUEST", "Comparison side must be baseline or target")
		return "", false
	}
}

type httpEntryDetail struct {
	ID                 int               `json:"id"`
	Path               string            `json:"path"`
	Status             ComparisonStatus  `json:"status"`
	Kind               EntryItemKind     `json:"kind"`
	BaselineSize       *int64            `json:"baselineSize,omitempty"`
	TargetSize         *int64            `json:"targetSize,omitempty"`
	Message            string            `json:"message,omitempty"`
	Presentation       EntryPresentation `json:"presentation"`
	BaselineLinkTarget *string           `json:"baselineLinkTarget,omitempty"`
	TargetLinkTarget   *string           `json:"targetLinkTarget,omitempty"`
}

func makeHTTPEntryDetail(detail EntryDetail) httpEntryDetail {
	entry := makeHTTPListItem(detail.Entry)
	result := httpEntryDetail{
		ID:           entry.ID,
		Path:         entry.Path,
		Status:       entry.Status,
		Kind:         entry.Kind,
		BaselineSize: entry.BaselineSize,
		TargetSize:   entry.TargetSize,
		Message:      entry.Message,
		Presentation: detail.Presentation,
	}
	if detail.Baseline != nil && detail.Baseline.Kind == EntryKindSymlink {
		linkTarget := detail.Baseline.LinkTarget
		result.BaselineLinkTarget = &linkTarget
	}
	if detail.Target != nil && detail.Target.Kind == EntryKindSymlink {
		linkTarget := detail.Target.LinkTarget
		result.TargetLinkTarget = &linkTarget
	}
	return result
}

type httpTextContent struct {
	Status    ContentStatus `json:"status"`
	Text      *string       `json:"text,omitempty"`
	Encoding  *TextEncoding `json:"encoding,omitempty"`
	Size      *int64        `json:"size,omitempty"`
	LineCount *int          `json:"lineCount,omitempty"`
}

func makeHTTPTextContent(content TextContent) httpTextContent {
	result := httpTextContent{Status: content.Status}
	switch content.Status {
	case ContentReady:
		text := content.Text
		encoding := content.Encoding
		size := content.Size
		lineCount := content.LineCount
		result.Text = &text
		result.Encoding = &encoding
		result.Size = &size
		result.LineCount = &lineCount
	case ContentGuarded:
		size := content.Size
		lineCount := content.LineCount
		result.Size = &size
		result.LineCount = &lineCount
	case ContentBlocked:
		size := content.Size
		result.Size = &size
		if content.LineCount > 0 {
			lineCount := content.LineCount
			result.LineCount = &lineCount
		}
	}
	return result
}
