package logging

import (
	"bytes"
	"encoding/json"
	"math"
	"reflect"
	"regexp"
	"strings"
	"unicode"

	"github.com/charmbracelet/x/ansi"
)

const (
	redactedValue    = "[REDACTED]"
	unencodableValue = "[UNENCODABLE]"
)

var (
	credentialAssignment = regexp.MustCompile(`(?i)(["']?\b(?:authorization|cookies?|passwords?|secrets?|tokens?|api[\s._-]*keys?|credentials?|private[\s._-]*keys?|request[\s._-]*bod(?:y|ies)|key)\b["']?)\s*([:=])\s*(?:"[^"]*"|'[^']*'|[^\s,;}\]]+)`)
	bearerSecret         = regexp.MustCompile(`(?i)\bbearer\s+(?:"[^"]*"|'[^']*'|[^\s,;}\]]+)`)
)

// Redact removes credential-shaped values from diagnostic text while retaining a single line.
func Redact(value string) string {
	value = bearerSecret.ReplaceAllString(value, "Bearer "+redactedValue)
	value = credentialAssignment.ReplaceAllString(value, "${1}${2}"+redactedValue)
	return strings.ReplaceAll(value, "\n", `\n`)
}

// RedactDiagnostic projects arbitrary diagnostic text as one control-free line.
func RedactDiagnostic(value string) string {
	value = StripANSI(value)
	value = strings.Map(func(character rune) rune {
		if character == '\r' || character == '\n' || unicode.IsControl(character) {
			return ' '
		}
		return character
	}, value)
	return Redact(strings.Join(strings.Fields(value), " "))
}

// StripANSI removes terminal styling sequences while keeping caller-specific
// control-character and whitespace projection behind its existing policy.
func StripANSI(value string) string {
	return ansi.Strip(value)
}

func redactContext(context map[string]any) map[string]any {
	redactor := redactor{visiting: make(map[redactionVisit]struct{})}
	redacted := make(map[string]any, len(context))
	for key, value := range context {
		redacted[key] = redactor.redactField(key, reflect.ValueOf(value))
	}
	return redacted
}

type redactor struct {
	visiting map[redactionVisit]struct{}
}

type redactionVisit struct {
	typeOf  reflect.Type
	pointer uintptr
}

func (redactor *redactor) redactField(key string, value reflect.Value) any {
	if credentialField(key) {
		return redactedValue
	}
	return redactor.redactValue(value)
}

func (redactor *redactor) redactValue(value reflect.Value) any {
	if !value.IsValid() {
		return nil
	}
	if value.Kind() == reflect.Interface {
		if value.IsNil() {
			return nil
		}
		return redactor.redactValue(value.Elem())
	}
	if value.CanInterface() {
		switch typed := value.Interface().(type) {
		case error:
			return Redact(typed.Error())
		case string:
			return Redact(typed)
		case json.Number:
			return typed
		}
	}

	switch value.Kind() {
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return value.Interface()
	case reflect.Float32, reflect.Float64:
		if math.IsInf(value.Float(), 0) || math.IsNaN(value.Float()) {
			return unencodableValue
		}
		return value.Interface()
	case reflect.String:
		return Redact(value.String())
	case reflect.Map:
		return redactor.redactMap(value)
	case reflect.Slice:
		if value.Type().Elem().Kind() == reflect.Uint8 {
			return redactedValue
		}
		return redactor.redactSlice(value)
	case reflect.Array:
		return redactor.redactArray(value)
	case reflect.Pointer:
		if value.IsNil() {
			return nil
		}
		if !redactor.enter(value) {
			return unencodableValue
		}
		defer redactor.leave(value)
		return redactor.redactValue(value.Elem())
	case reflect.Chan, reflect.Complex64, reflect.Complex128, reflect.Func, reflect.UnsafePointer, reflect.Uintptr:
		return unencodableValue
	default:
		return redactor.redactEncoded(value)
	}
}

func (redactor *redactor) redactMap(value reflect.Value) any {
	if value.IsNil() {
		return nil
	}
	if value.Type().Key().Kind() != reflect.String || !redactor.enter(value) {
		return unencodableValue
	}
	defer redactor.leave(value)

	redacted := make(map[string]any, value.Len())
	for _, key := range value.MapKeys() {
		redacted[key.String()] = redactor.redactField(key.String(), value.MapIndex(key))
	}
	return redacted
}

func (redactor *redactor) redactSlice(value reflect.Value) any {
	if value.IsNil() {
		return nil
	}
	if !redactor.enter(value) {
		return unencodableValue
	}
	defer redactor.leave(value)

	redacted := make([]any, value.Len())
	for index := range redacted {
		redacted[index] = redactor.redactValue(value.Index(index))
	}
	return redacted
}

func (redactor *redactor) redactArray(value reflect.Value) any {
	redacted := make([]any, value.Len())
	for index := range redacted {
		redacted[index] = redactor.redactValue(value.Index(index))
	}
	return redacted
}

func (redactor *redactor) redactEncoded(value reflect.Value) any {
	if !value.CanInterface() {
		return unencodableValue
	}
	encoded, err := json.Marshal(value.Interface())
	if err != nil {
		return unencodableValue
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return unencodableValue
	}
	return redactor.redactDecoded(decoded)
}

func (redactor *redactor) redactDecoded(value any) any {
	switch typed := value.(type) {
	case string:
		return Redact(typed)
	case map[string]any:
		redacted := make(map[string]any, len(typed))
		for key, nested := range typed {
			redacted[key] = redactor.redactDecodedField(key, nested)
		}
		return redacted
	case []any:
		redacted := make([]any, len(typed))
		for index, nested := range typed {
			redacted[index] = redactor.redactDecoded(nested)
		}
		return redacted
	default:
		return typed
	}
}

func (redactor *redactor) redactDecodedField(key string, value any) any {
	if credentialField(key) {
		return redactedValue
	}
	return redactor.redactDecoded(value)
}

func (redactor *redactor) enter(value reflect.Value) bool {
	visit := redactionVisit{typeOf: value.Type(), pointer: value.Pointer()}
	if _, exists := redactor.visiting[visit]; exists {
		return false
	}
	redactor.visiting[visit] = struct{}{}
	return true
}

func (redactor *redactor) leave(value reflect.Value) {
	delete(redactor.visiting, redactionVisit{typeOf: value.Type(), pointer: value.Pointer()})
}

func credentialField(key string) bool {
	normalized := strings.Map(func(character rune) rune {
		if unicode.IsLetter(character) || unicode.IsDigit(character) {
			return unicode.ToLower(character)
		}
		return -1
	}, key)
	for _, credential := range []string{
		"authorization",
		"cookie",
		"password",
		"secret",
		"token",
		"apikey",
		"credential",
		"privatekey",
		"requestbody",
	} {
		if strings.Contains(normalized, credential) {
			return true
		}
	}
	return false
}
