package diff

import (
	"errors"
	"unicode/utf8"
)

var errInvalidUTF8Filename = errors.New("invalid UTF-8 filename")

func comparisonPathForChild(parent, name string) (string, error) {
	if !utf8.ValidString(name) {
		return "", errInvalidUTF8Filename
	}
	return joinComparisonPath(parent, name), nil
}
