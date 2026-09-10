package env

import "testing"

func TestEncodeMatchesDeterministicJSON(t *testing.T) {
	got, err := Encode(map[string]string{
		"separator":     "\u2028",
		"mixed":         "\\\u2028",
		"literalEscape": `\u2028`,
		"multiline":     "first\nsecond",
		"html":          "<>&",
		"alpha":         "first",
	})

	if err != nil {
		t.Fatalf("Encode returned an error: %v", err)
	}
	want := "{\n" +
		"  \"alpha\": \"first\",\n" +
		"  \"html\": \"<>&\",\n" +
		"  \"literalEscape\": \"\\\\u2028\",\n" +
		"  \"mixed\": \"\\\\\u2028\",\n" +
		"  \"multiline\": \"first\\nsecond\",\n" +
		"  \"separator\": \"\u2028\"\n" +
		"}"
	if got != want {
		t.Fatalf("Encode() = %q, want %q", got, want)
	}
}
