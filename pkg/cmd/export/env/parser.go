package env

import "strings"

// Parse reads dotenv content and lets later content override earlier keys.
func Parse(contents ...string) map[string]string {
	values := make(map[string]string)
	for _, content := range contents {
		parseDotenv(values, strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(content))
	}
	return values
}

func parseDotenv(values map[string]string, content string) {
	for offset := 0; offset < len(content); {
		offset = skipLineWhitespace(content, offset)
		if offset >= len(content) {
			return
		}
		if content[offset] == '\n' {
			offset++
			continue
		}

		lineStart := offset
		if strings.HasPrefix(content[offset:], "export") {
			candidate := offset + len("export")
			if candidate < len(content) && isLineWhitespace(content[candidate]) {
				offset = skipLineWhitespace(content, candidate)
			}
		}

		keyStart := offset
		for offset < len(content) && isDotenvKeyCharacter(content[offset]) {
			offset++
		}
		if keyStart == offset {
			offset = skipLine(content, lineStart)
			continue
		}
		key := content[keyStart:offset]
		offset = skipLineWhitespace(content, offset)
		if offset >= len(content) {
			return
		}
		if content[offset] == '=' {
			offset++
		} else if content[offset] == ':' && offset+1 < len(content) && isLineWhitespace(content[offset+1]) {
			offset = skipLineWhitespace(content, offset+1)
		} else {
			offset = skipLine(content, lineStart)
			continue
		}

		offset = skipLineWhitespace(content, offset)
		value, next := dotenvValue(content, offset)
		values[key] = value
		offset = next
	}
}

func dotenvValue(content string, offset int) (string, int) {
	if offset >= len(content) || content[offset] == '\n' || content[offset] == '#' {
		return "", skipLine(content, offset)
	}

	quote := content[offset]
	if quote == '\'' || quote == '"' || quote == '`' {
		for index := offset + 1; index < len(content); index++ {
			if content[index] != quote || content[index-1] == '\\' {
				continue
			}
			value := content[offset+1 : index]
			if quote == '"' {
				value = strings.ReplaceAll(value, `\n`, "\n")
				value = strings.ReplaceAll(value, `\r`, "\r")
			}
			return value, skipLine(content, index+1)
		}
	}

	end := offset
	for end < len(content) && content[end] != '\n' && content[end] != '#' {
		end++
	}
	return strings.TrimSpace(content[offset:end]), skipLine(content, end)
}

func skipLineWhitespace(content string, offset int) int {
	for offset < len(content) && isLineWhitespace(content[offset]) {
		offset++
	}
	return offset
}

func isLineWhitespace(character byte) bool {
	switch character {
	case ' ', '\t', '\v', '\f':
		return true
	default:
		return false
	}
}

func isDotenvKeyCharacter(character byte) bool {
	return character >= 'a' && character <= 'z' ||
		character >= 'A' && character <= 'Z' ||
		character >= '0' && character <= '9' ||
		character == '_' || character == '.' || character == '-'
}

func skipLine(content string, offset int) int {
	for offset < len(content) && content[offset] != '\n' {
		offset++
	}
	if offset < len(content) {
		return offset + 1
	}
	return offset
}
