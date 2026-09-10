package env

import (
	"bytes"
	"encoding/json"
	"strings"
)

// Encode serializes dotenv values as deterministic JSON.stringify-compatible text.
func Encode(values map[string]string) (string, error) {
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(values); err != nil {
		return "", err
	}
	return restoreJavaScriptLineSeparators(strings.TrimSuffix(output.String(), "\n")), nil
}

func restoreJavaScriptLineSeparators(encoded string) string {
	var output strings.Builder
	output.Grow(len(encoded))
	for index := 0; index < len(encoded); {
		if encoded[index] != '\\' || index+1 >= len(encoded) {
			output.WriteByte(encoded[index])
			index++
			continue
		}
		if strings.HasPrefix(encoded[index:], `\u2028`) {
			output.WriteRune('\u2028')
			index += len(`\u2028`)
			continue
		}
		if strings.HasPrefix(encoded[index:], `\u2029`) {
			output.WriteRune('\u2029')
			index += len(`\u2029`)
			continue
		}
		output.WriteByte(encoded[index])
		output.WriteByte(encoded[index+1])
		index += 2
	}
	return output.String()
}
