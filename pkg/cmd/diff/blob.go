package diff

import (
	"path"
	"strings"
)

func (snapshot *Snapshot) Blob(entryID int, side ComparisonSide) (BlobContent, error) {
	entry := snapshot.entry(entryID)
	if entry == nil {
		return BlobContent{}, errComparisonEntryNotFound
	}
	if entry.Status == StatusIssue {
		return BlobContent{Status: BlobUnavailable}, nil
	}
	source, root := snapshot.sourceForSide(entry, side)
	if root == "" {
		return BlobContent{}, errInvalidComparisonSide
	}
	if source == nil {
		return BlobContent{Status: BlobMissing}, nil
	}
	if source.state.Kind != EntryKindFile {
		return BlobContent{Status: BlobUnavailable}, nil
	}
	bytes, stable := readStableSource(source, root, entry.Path)
	if !stable {
		return BlobContent{Status: BlobStale}, nil
	}
	mimeType, image := imageMIMEType(entry.Path)
	if !image {
		mimeType = "application/octet-stream"
	}
	return BlobContent{
		Status:   BlobReady,
		Bytes:    bytes,
		MIMEType: mimeType,
		Filename: comparisonBaseName(entry.Path),
	}, nil
}

func imageMIMEType(comparisonPath string) (string, bool) {
	switch strings.ToLower(path.Ext(comparisonPath)) {
	case ".avif":
		return "image/avif", true
	case ".gif":
		return "image/gif", true
	case ".jpeg", ".jpg":
		return "image/jpeg", true
	case ".png":
		return "image/png", true
	case ".svg":
		return "image/svg+xml", true
	case ".webp":
		return "image/webp", true
	default:
		return "", false
	}
}
