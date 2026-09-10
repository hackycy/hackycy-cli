//go:build !windows

package server

import "net/url"

func databaseFileURI(path string) string {
	return (&url.URL{Scheme: "file", Path: path}).String()
}
