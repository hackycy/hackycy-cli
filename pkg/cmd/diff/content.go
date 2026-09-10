package diff

import (
	"encoding/binary"
	"errors"
	"io"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

const (
	maxConfirmedTextBytes int64 = 20 * 1024 * 1024
	maxAutoTextBytes      int64 = 2 * 1024 * 1024
	maxAutoTextLines            = 50_000
	maxConfirmedTextLines       = 200_000
	maxTextLineLength           = 1024 * 1024
)

var (
	errComparisonEntryNotFound = errors.New("Comparison Entry not found")
	errComparisonIssueContent  = errors.New("Comparison Issue has no text content")
	errInvalidComparisonSide   = errors.New("Comparison side must be baseline or target")
)

func (snapshot *Snapshot) Detail(entryID int) (EntryDetail, error) {
	entry := snapshot.entry(entryID)
	if entry == nil {
		return EntryDetail{}, errComparisonEntryNotFound
	}

	detail := EntryDetail{Entry: cloneEntry(entry.Entry)}
	if entry.Status == StatusIssue {
		detail.Presentation = PresentationIssue
		return detail, nil
	}
	detail.Presentation = snapshot.presentation(entry)
	return detail, nil
}

func (snapshot *Snapshot) Content(entryID int, side ComparisonSide, force bool) (TextContent, error) {
	entry := snapshot.entry(entryID)
	if entry == nil {
		return TextContent{}, errComparisonEntryNotFound
	}
	if entry.Status == StatusIssue {
		return TextContent{}, errComparisonIssueContent
	}

	source, root := snapshot.sourceForSide(entry, side)
	if root == "" {
		return TextContent{}, errInvalidComparisonSide
	}
	return loadTextContent(source, root, entry.Path, force), nil
}

func (snapshot *Snapshot) sourceForSide(entry *snapshotEntry, side ComparisonSide) (*sourceEntry, string) {
	switch side {
	case SideBaseline:
		return entry.baseline, snapshot.summary.BaselineDirectory
	case SideTarget:
		return entry.target, snapshot.summary.TargetDirectory
	default:
		return nil, ""
	}
}

func (snapshot *Snapshot) presentation(entry *snapshotEntry) EntryPresentation {
	if sourceIsSymlink(entry.baseline) || sourceIsSymlink(entry.target) {
		return PresentationSymlink
	}
	if isImagePath(entry.Path) {
		return PresentationImage
	}
	if sourceSize(entry.baseline) > maxConfirmedTextBytes || sourceSize(entry.target) > maxConfirmedTextBytes {
		return PresentationOversized
	}
	contents := make([][]byte, 0, 2)
	for _, sourceRoot := range []struct {
		source *sourceEntry
		root   string
	}{
		{source: entry.baseline, root: snapshot.summary.BaselineDirectory},
		{source: entry.target, root: snapshot.summary.TargetDirectory},
	} {
		if sourceRoot.source == nil {
			continue
		}
		bytes, stable := readStableSource(sourceRoot.source, sourceRoot.root, entry.Path)
		if !stable {
			return PresentationStale
		}
		contents = append(contents, bytes)
	}
	for _, bytes := range contents {
		if _, ok := decodeText(bytes); !ok {
			return PresentationBinary
		}
	}
	return PresentationText
}

func loadTextContent(source *sourceEntry, root, comparisonPath string, force bool) TextContent {
	if source == nil {
		return TextContent{Status: ContentMissing}
	}
	if source.state.Kind != EntryKindFile {
		return TextContent{Status: ContentBinary}
	}
	if source.state.Size > maxConfirmedTextBytes {
		return TextContent{Status: ContentBlocked, Size: source.state.Size}
	}

	bytes, stable := readStableSource(source, root, comparisonPath)
	if !stable {
		return TextContent{Status: ContentStale}
	}
	decoded, ok := decodeText(bytes)
	if !ok {
		return TextContent{Status: ContentBinary}
	}

	lineCount, oversizedLine := textLineMetrics(decoded.text)
	if lineCount > maxConfirmedTextLines || oversizedLine {
		return TextContent{Status: ContentBlocked, Size: source.state.Size, LineCount: lineCount}
	}
	if !force && (source.state.Size > maxAutoTextBytes || lineCount > maxAutoTextLines) {
		return TextContent{Status: ContentGuarded, Size: source.state.Size, LineCount: lineCount}
	}
	return TextContent{
		Status:    ContentReady,
		Text:      decoded.text,
		Encoding:  decoded.encoding,
		Size:      source.state.Size,
		LineCount: lineCount,
	}
}

func readStableSource(source *sourceEntry, root, comparisonPath string) ([]byte, bool) {
	absolutePath := absoluteComparisonPath(root, comparisonPath)
	file, err := openComparisonFile(absolutePath)
	if err != nil {
		return nil, false
	}
	defer file.Close()

	if !pathResolvesTo(absolutePath) {
		return nil, false
	}
	before, err := file.Stat()
	if err != nil || !sameSourceInfo(source.info, before) {
		return nil, false
	}
	bytes, err := io.ReadAll(file)
	if err != nil {
		return nil, false
	}
	after, err := file.Stat()
	if err != nil || !sameSourceInfo(source.info, after) || !pathResolvesTo(absolutePath) {
		return nil, false
	}
	return bytes, true
}

func pathResolvesTo(absolutePath string) bool {
	resolved, err := filepath.EvalSymlinks(absolutePath)
	if err != nil {
		return false
	}
	absolutePath = filepath.Clean(absolutePath)
	resolved = filepath.Clean(resolved)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(absolutePath, resolved)
	}
	return absolutePath == resolved
}

type decodedText struct {
	text     string
	encoding TextEncoding
}

func decodeText(bytes []byte) (decodedText, bool) {
	if len(bytes) >= 2 && bytes[0] == 0xff && bytes[1] == 0xfe {
		text, ok := decodeUTF16(bytes[2:], binary.LittleEndian)
		return decodedText{text: text, encoding: EncodingUTF16LE}, ok
	}
	if len(bytes) >= 2 && bytes[0] == 0xfe && bytes[1] == 0xff {
		text, ok := decodeUTF16(bytes[2:], binary.BigEndian)
		return decodedText{text: text, encoding: EncodingUTF16BE}, ok
	}
	if !utf8.Valid(bytes) {
		return decodedText{}, false
	}
	if len(bytes) >= 3 && bytes[0] == 0xef && bytes[1] == 0xbb && bytes[2] == 0xbf {
		bytes = bytes[3:]
	}
	return decodedText{text: string(bytes), encoding: EncodingUTF8}, true
}

func decodeUTF16(bytes []byte, order binary.ByteOrder) (string, bool) {
	if len(bytes)%2 != 0 {
		return "", false
	}
	runes := make([]rune, 0, len(bytes)/2)
	for index := 0; index < len(bytes); index += 2 {
		unit := order.Uint16(bytes[index:])
		switch {
		case 0xd800 <= unit && unit <= 0xdbff:
			if index+3 >= len(bytes) {
				return "", false
			}
			next := order.Uint16(bytes[index+2:])
			if next < 0xdc00 || next > 0xdfff {
				return "", false
			}
			runes = append(runes, utf16.DecodeRune(rune(unit), rune(next)))
			index += 2
		case 0xdc00 <= unit && unit <= 0xdfff:
			return "", false
		default:
			runes = append(runes, rune(unit))
		}
	}
	return string(runes), true
}

func textLineMetrics(text string) (int, bool) {
	if text == "" {
		return 0, false
	}

	lineCount := 1
	lineLength := 0
	oversizedLine := false
	units := utf16.Encode([]rune(text))
	for index := 0; index < len(units); index++ {
		switch units[index] {
		case '\n', '\r':
			lineCount++
			lineLength = 0
			if units[index] == '\r' && index+1 < len(units) && units[index+1] == '\n' {
				index++
			}
		default:
			lineLength++
			if lineLength > maxTextLineLength {
				oversizedLine = true
			}
		}
	}
	return lineCount, oversizedLine
}

func sourceIsSymlink(source *sourceEntry) bool {
	return source != nil && source.state.Kind == EntryKindSymlink
}

func sourceSize(source *sourceEntry) int64 {
	if source == nil {
		return 0
	}
	return source.state.Size
}

func isImagePath(comparisonPath string) bool {
	_, image := imageMIMEType(comparisonPath)
	return image
}
