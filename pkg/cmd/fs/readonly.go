package fs

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"path"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

const (
	MaxTextPreviewBytes int64 = 10 * 1024 * 1024
	MaxUploadBytes      int64 = 1024 * 1024 * 1024
)

type PreviewKind string

const (
	PreviewImage PreviewKind = "image"
	PreviewVideo PreviewKind = "video"
	PreviewAudio PreviewKind = "audio"
	PreviewPDF   PreviewKind = "pdf"
	PreviewText  PreviewKind = "text"
	PreviewNone  PreviewKind = "none"
)

type DirectoryEntry struct {
	Name           string      `json:"name"`
	Path           string      `json:"path"`
	Kind           EntryKind   `json:"kind"`
	IsSymlink      bool        `json:"isSymlink"`
	Size           *int64      `json:"size,omitempty"`
	ModifiedAt     string      `json:"modifiedAt,omitempty"`
	MIMEType       string      `json:"mimeType,omitempty"`
	PreviewKind    PreviewKind `json:"previewKind"`
	SyntaxLanguage string      `json:"syntaxLanguage,omitempty"`
	BrowseURL      string      `json:"browseUrl,omitempty"`
	FileURL        string      `json:"fileUrl,omitempty"`
	ThumbnailURL   string      `json:"thumbnailUrl,omitempty"`
	DownloadURL    string      `json:"downloadUrl,omitempty"`
	Extractable    bool        `json:"extractable"`
}

type DirectoryListing struct {
	RootName          string                   `json:"rootName"`
	Path              string                   `json:"path"`
	ParentPath        string                   `json:"parentPath,omitempty"`
	ManagementEnabled bool                     `json:"managementEnabled"`
	MaxUploadBytes    int64                    `json:"maxUploadBytes"`
	ChunkedUpload     *ChunkedUploadCapability `json:"chunkedUpload,omitempty"`
	Entries           []DirectoryEntry         `json:"entries"`
}

type ChunkedUploadCapability struct {
	ThresholdBytes int64 `json:"thresholdBytes"`
	ChunkSizeBytes int64 `json:"chunkSizeBytes"`
}

type TextPreview struct {
	Status   string `json:"status"`
	Text     string `json:"text,omitempty"`
	Encoding string `json:"encoding,omitempty"`
	Size     int64  `json:"size"`
	MaxBytes int64  `json:"maxBytes,omitempty"`
	Revision string `json:"revision,omitempty"`
}

func (workspace *Workspace) ReadDirectory(path WorkspacePath, managementEnabled bool, chunkedUpload *ChunkedUploadManager) (DirectoryListing, error) {
	entries, err := workspace.List(path)
	if err != nil {
		return DirectoryListing{}, err
	}
	result := DirectoryListing{
		RootName:          workspace.RootName(),
		Path:              path.String(),
		ManagementEnabled: managementEnabled,
		MaxUploadBytes:    MaxUploadBytes,
		Entries:           make([]DirectoryEntry, 0, len(entries)),
	}
	if managementEnabled && chunkedUpload != nil {
		result.ChunkedUpload = &ChunkedUploadCapability{ThresholdBytes: chunkedUploadThreshold, ChunkSizeBytes: chunkedUpload.chunkSize}
	}
	if path.String() != "" {
		parts := strings.Split(path.String(), "/")
		result.ParentPath = strings.Join(parts[:len(parts)-1], "/")
	}
	for _, entry := range entries {
		result.Entries = append(result.Entries, makeDirectoryEntry(entry))
	}
	return result, nil
}

func makeDirectoryEntry(entry Entry) DirectoryEntry {
	result := DirectoryEntry{
		Name:        entry.Name,
		Path:        entry.Path.String(),
		Kind:        entry.Kind,
		IsSymlink:   entry.IsSymlink,
		PreviewKind: PreviewNone,
	}
	if !entry.ModifiedAt.IsZero() {
		result.ModifiedAt = entry.ModifiedAt.UTC().Format("2006-01-02T15:04:05.000Z")
	}
	if entry.Kind == EntryKindDirectory {
		result.BrowseURL = "/browse/" + encodeWorkspacePath(entry.Path.String())
		return result
	}
	if entry.Kind != EntryKindFile {
		return result
	}
	size := entry.Size
	result.Size = &size
	result.MIMEType = workspaceMIMEType(entry.Name)
	result.SyntaxLanguage = workspaceSyntaxLanguage(entry.Name)
	result.PreviewKind = workspacePreviewKind(result.MIMEType, result.SyntaxLanguage)
	fileURL := "/files/" + encodeWorkspacePath(entry.Path.String())
	result.FileURL = fileURL
	result.DownloadURL = fileURL + "?download=1"
	if thumbnailSupported(entry.Name) {
		result.ThumbnailURL = "/thumbnails/" + encodeWorkspacePath(entry.Path.String())
	}
	result.Extractable = extractableArchiveName(entry.Name)
	return result
}

func (workspace *Workspace) ReadText(path WorkspacePath) (TextPreview, error) {
	file, err := workspace.OpenFile(path)
	if err != nil {
		return TextPreview{}, err
	}
	defer file.Close()
	identity := file.Identity()
	if identity.Size > MaxTextPreviewBytes {
		return TextPreview{Status: "too_large", Size: identity.Size, MaxBytes: MaxTextPreviewBytes}, nil
	}
	bytes, err := io.ReadAll(io.LimitReader(file, MaxTextPreviewBytes+1))
	if err != nil {
		return TextPreview{}, errors.Join(ErrWorkspaceUnavailable, err)
	}
	info, err := file.file.Stat()
	if err != nil {
		return TextPreview{}, errors.Join(ErrWorkspaceUnavailable, err)
	}
	if info.Size() > MaxTextPreviewBytes || int64(len(bytes)) > MaxTextPreviewBytes {
		return TextPreview{Status: "too_large", Size: info.Size(), MaxBytes: MaxTextPreviewBytes}, nil
	}
	text, encoding, ok := decodeWorkspaceText(bytes)
	if !ok {
		return TextPreview{Status: "binary", Size: info.Size()}, nil
	}
	digest := sha256.Sum256(bytes)
	return TextPreview{
		Status:   "ready",
		Text:     text,
		Encoding: encoding,
		Size:     info.Size(),
		Revision: hex.EncodeToString(digest[:]),
	}, nil
}

func decodeWorkspaceText(bytes []byte) (string, string, bool) {
	if len(bytes) >= 2 && bytes[0] == 0xff && bytes[1] == 0xfe {
		text, ok := decodeWorkspaceUTF16(bytes[2:], binary.LittleEndian)
		return text, "utf-16le", ok
	}
	if len(bytes) >= 2 && bytes[0] == 0xfe && bytes[1] == 0xff {
		text, ok := decodeWorkspaceUTF16(bytes[2:], binary.BigEndian)
		return text, "utf-16be", ok
	}
	if !utf8.Valid(bytes) {
		return "", "", false
	}
	if len(bytes) >= 3 && string(bytes[:3]) == "\xef\xbb\xbf" {
		bytes = bytes[3:]
	}
	return string(bytes), "utf-8", true
}

func decodeWorkspaceUTF16(bytes []byte, order binary.ByteOrder) (string, bool) {
	if len(bytes)%2 != 0 {
		return "", false
	}
	runes := make([]rune, 0, len(bytes)/2)
	for offset := 0; offset < len(bytes); offset += 2 {
		unit := order.Uint16(bytes[offset:])
		switch {
		case 0xd800 <= unit && unit <= 0xdbff:
			if offset+3 >= len(bytes) {
				return "", false
			}
			next := order.Uint16(bytes[offset+2:])
			if next < 0xdc00 || next > 0xdfff {
				return "", false
			}
			runes = append(runes, utf16.DecodeRune(rune(unit), rune(next)))
			offset += 2
		case 0xdc00 <= unit && unit <= 0xdfff:
			return "", false
		default:
			runes = append(runes, rune(unit))
		}
	}
	return string(runes), true
}

func workspaceMIMEType(name string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".avif":
		return "image/avif"
	case ".gif":
		return "image/gif"
	case ".jpeg", ".jpg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".svg":
		return "image/svg+xml"
	case ".webp":
		return "image/webp"
	case ".html", ".htm":
		return "text/html;charset=utf-8"
	case ".xhtml":
		return "application/xhtml+xml"
	case ".xml":
		return "application/xml"
	case ".json", ".map":
		return "application/json;charset=utf-8"
	case ".js", ".mjs", ".cjs":
		return "application/javascript;charset=utf-8"
	case ".pdf":
		return "application/pdf"
	case ".mp3":
		return "audio/mpeg"
	case ".mp4":
		return "video/mp4"
	case ".txt", ".md", ".csv", ".log", ".yaml", ".yml", ".toml", ".go", ".ts", ".tsx", ".jsx", ".css", ".scss", ".sh":
		return "text/plain;charset=utf-8"
	default:
		return "application/octet-stream"
	}
}

func workspaceSyntaxLanguage(name string) string {
	lower := strings.ToLower(name)
	if strings.HasPrefix(lower, ".env") && (lower == ".env" || strings.HasPrefix(lower, ".env.")) {
		return "dotenv"
	}
	switch strings.ToLower(path.Ext(name)) {
	case ".go":
		return "go"
	case ".ts", ".tsx":
		return "typescript"
	case ".js", ".mjs", ".cjs", ".jsx":
		return "javascript"
	case ".json":
		return "json"
	case ".html", ".htm", ".xhtml":
		return "html"
	case ".css", ".scss":
		return "css"
	case ".md":
		return "markdown"
	case ".yaml", ".yml":
		return "yaml"
	case ".xml":
		return "xml"
	case ".sh":
		return "shell"
	default:
		return ""
	}
}

func workspacePreviewKind(mimeType, language string) PreviewKind {
	base := strings.ToLower(strings.TrimSpace(strings.Split(mimeType, ";")[0]))
	switch {
	case strings.HasPrefix(base, "image/"):
		return PreviewImage
	case language != "":
		return PreviewText
	case strings.HasPrefix(base, "video/"):
		return PreviewVideo
	case strings.HasPrefix(base, "audio/"):
		return PreviewAudio
	case base == "application/pdf":
		return PreviewPDF
	case strings.HasPrefix(base, "text/"), base == "application/json", base == "application/javascript", base == "application/ld+json", base == "application/xml", base == "application/xhtml+xml":
		return PreviewText
	default:
		return PreviewNone
	}
}

func thumbnailSupported(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".avif", ".gif", ".jpeg", ".jpg", ".png", ".webp":
		return true
	default:
		return false
	}
}
