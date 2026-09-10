package diff

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestHTTPHandlerServesEntryDetailsAndTextContent(t *testing.T) {
	workspace, snapshot, target := httpDetailContentFixture(t)
	handler := NewHTTPHandler(workspace)
	snapshotID := snapshot.Summary().ID

	changedID := snapshotEntryID(t, snapshot, "changed.txt")
	detail := httpResponse(handler, http.MethodGet, "/api/entries/"+strconv.Itoa(changedID)+"?snapshot="+snapshotID)
	assertHTTPAPIHeaders(t, detail)
	detailBody := decodeHTTPDetail(t, detail)
	if detail.Code != http.StatusOK || detailBody.Path != "changed.txt" || detailBody.Status != string(StatusModified) || detailBody.Kind != string(ItemKindFile) || detailBody.Presentation != string(PresentationText) || detailBody.BaselineSize == nil || *detailBody.BaselineSize != int64(len("before")) || detailBody.TargetSize == nil || *detailBody.TargetSize != int64(len("after!")) || detailBody.BaselineLinkTarget != nil || detailBody.TargetLinkTarget != nil {
		t.Fatalf("changed detail = %#v", detailBody)
	}

	linkID := snapshotEntryID(t, snapshot, "link")
	link := httpResponse(handler, http.MethodGet, "/api/entries/"+strconv.Itoa(linkID)+"?snapshot="+snapshotID)
	linkBody := decodeHTTPDetail(t, link)
	if link.Code != http.StatusOK || linkBody.Kind != string(ItemKindSymlink) || linkBody.Presentation != string(PresentationSymlink) || linkBody.BaselineLinkTarget == nil || *linkBody.BaselineLinkTarget != "baseline-target" || linkBody.TargetLinkTarget == nil || *linkBody.TargetLinkTarget != "target-target" {
		t.Fatalf("link detail = %#v", linkBody)
	}
	imageID := snapshotEntryID(t, snapshot, "preview.PNG")
	if image := decodeHTTPDetail(t, httpResponse(handler, http.MethodGet, "/api/entries/"+strconv.Itoa(imageID)+"?snapshot="+snapshotID)); image.Presentation != string(PresentationImage) {
		t.Fatalf("image detail = %#v", image)
	}

	content := httpResponse(handler, http.MethodGet, "/api/entries/"+strconv.Itoa(changedID)+"/content/target?snapshot="+snapshotID)
	assertHTTPAPIHeaders(t, content)
	contentBody := decodeHTTPContent(t, content)
	if content.Code != http.StatusOK || contentBody.Status != string(ContentReady) || contentBody.Text == nil || *contentBody.Text != "after!" || contentBody.Encoding == nil || *contentBody.Encoding != string(EncodingUTF8) || contentBody.Size == nil || *contentBody.Size != int64(len("after!")) || contentBody.LineCount == nil || *contentBody.LineCount != 1 {
		t.Fatalf("ready content = %#v", contentBody)
	}

	guardedID := snapshotEntryID(t, snapshot, "guarded.txt")
	guarded := httpResponse(handler, http.MethodGet, "/api/entries/"+strconv.Itoa(guardedID)+"/content/target?snapshot="+snapshotID)
	guardedBody := decodeHTTPContent(t, guarded)
	if guarded.Code != http.StatusOK || guardedBody.Status != string(ContentGuarded) || guardedBody.Text != nil || guardedBody.Encoding != nil || guardedBody.Size == nil || *guardedBody.Size != 100_000 || guardedBody.LineCount == nil || *guardedBody.LineCount != 50_001 {
		t.Fatalf("guarded content = %#v", guardedBody)
	}
	forced := decodeHTTPContent(t, httpResponse(handler, http.MethodGet, "/api/entries/"+strconv.Itoa(guardedID)+"/content/target?snapshot="+snapshotID+"&force=true"))
	if forced.Status != string(ContentReady) || forced.Text == nil {
		t.Fatalf("forced content = %#v", forced)
	}

	deletedID := snapshotEntryID(t, snapshot, "deleted.txt")
	if missing := decodeHTTPContent(t, httpResponse(handler, http.MethodGet, "/api/entries/"+strconv.Itoa(deletedID)+"/content/target?snapshot="+snapshotID)); missing.Status != string(ContentMissing) || missing.Text != nil || missing.Size != nil {
		t.Fatalf("missing content = %#v", missing)
	}

	if err := os.WriteFile(filepath.Join(target, "changed.txt"), []byte("changed-size"), 0o644); err != nil {
		t.Fatal(err)
	}
	stale := decodeHTTPContent(t, httpResponse(handler, http.MethodGet, "/api/entries/"+strconv.Itoa(changedID)+"/content/target?snapshot="+snapshotID))
	if stale.Status != string(ContentStale) || stale.Text != nil || stale.Size != nil {
		t.Fatalf("stale content = %#v", stale)
	}
}

func TestHTTPHandlerRejectsInvalidDetailAndContentRequests(t *testing.T) {
	workspace, snapshot, _ := httpDetailContentFixture(t)
	handler := NewHTTPHandler(workspace)
	snapshotID := snapshot.Summary().ID

	for _, path := range []string{
		"/api/entries/none?snapshot=" + snapshotID,
		"/api/entries/0?snapshot=" + snapshotID,
		"/api/entries/99999?snapshot=" + snapshotID,
		"/api/entries/1/content/other?snapshot=" + snapshotID,
	} {
		response := httpResponse(handler, http.MethodGet, path)
		assertHTTPAPIHeaders(t, response)
		if response.Code != http.StatusBadRequest && response.Code != http.StatusNotFound {
			t.Fatalf("invalid detail/content request %q code = %d, body = %s", path, response.Code, response.Body.String())
		}
	}
	for _, path := range []string{
		"/api/entries/1?snapshot=" + snapshotID,
		"/api/entries/1/content/target?snapshot=" + snapshotID,
	} {
		response := httpResponse(handler, http.MethodPost, path)
		assertHTTPAPIHeaders(t, response)
		assertHTTPAPIError(t, response, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Use GET")
	}
	conflict := httpResponse(handler, http.MethodGet, "/api/entries/1?snapshot=old")
	assertHTTPAPIHeaders(t, conflict)
	assertHTTPAPIError(t, conflict, http.StatusConflict, "SNAPSHOT_CHANGED", "The requested Comparison Snapshot is no longer available")
}

type httpDetailResponse struct {
	ID                 int     `json:"id"`
	Path               string  `json:"path"`
	Status             string  `json:"status"`
	Kind               string  `json:"kind"`
	Presentation       string  `json:"presentation"`
	BaselineSize       *int64  `json:"baselineSize"`
	TargetSize         *int64  `json:"targetSize"`
	BaselineLinkTarget *string `json:"baselineLinkTarget"`
	TargetLinkTarget   *string `json:"targetLinkTarget"`
	Message            string  `json:"message"`
}

type httpContentResponse struct {
	Status    string  `json:"status"`
	Text      *string `json:"text"`
	Encoding  *string `json:"encoding"`
	Size      *int64  `json:"size"`
	LineCount *int    `json:"lineCount"`
}

func httpDetailContentFixture(t *testing.T) (*Workspace, *Snapshot, string) {
	t.Helper()
	baseline, target := comparisonRoots(t)
	writeComparisonFile(t, baseline, "changed.txt", "before")
	writeComparisonFile(t, target, "changed.txt", "after!")
	writeComparisonFile(t, baseline, "deleted.txt", "gone")
	writeComparisonFile(t, target, "guarded.txt", strings.Repeat("x\n", 50_000))
	writeComparisonBytes(t, target, "preview.PNG", []byte{0xff, 0x00})
	if err := os.Symlink("baseline-target", filepath.Join(baseline, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target-target", filepath.Join(target, "link")); err != nil {
		t.Fatal(err)
	}
	workspace, err := NewWorkspace(WorkspaceOptions{BaselineDirectory: baseline, TargetDirectory: target})
	if err != nil {
		t.Fatalf("NewWorkspace() error = %v", err)
	}
	return workspace, refreshWorkspace(t, workspace), target
}

func decodeHTTPDetail(t *testing.T, response *httptest.ResponseRecorder) httpDetailResponse {
	t.Helper()
	var body httpDetailResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode detail response: %v", err)
	}
	return body
}

func decodeHTTPContent(t *testing.T, response *httptest.ResponseRecorder) httpContentResponse {
	t.Helper()
	var body httpContentResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode content response: %v", err)
	}
	return body
}
