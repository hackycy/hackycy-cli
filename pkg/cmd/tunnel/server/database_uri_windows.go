//go:build windows

package server

import (
	"net/url"
	"path/filepath"
)

// databaseFileURI uses the no-host form required by the pinned SQLite driver
// for native Windows drive-letter paths.
func databaseFileURI(path string) string {
	normalized := filepath.ToSlash(path)
	escaped := (&url.URL{Path: normalized}).EscapedPath()
	return "file:" + escaped
}
