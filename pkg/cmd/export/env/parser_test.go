package env

import (
	"reflect"
	"testing"
)

func TestParseUsesDotenvSyntaxAndMergesLaterContents(t *testing.T) {
	got := Parse(
		"# comment\r\n"+
			"EMPTY=\r\n"+
			"export EXPORTED=visible\r\n"+
			"UNICODE=\u4f60\u597d\r\n"+
			"INLINE=before # ignored\r\n"+
			"HASH=before#ignored\r\n"+
			"QUOTED=\"value # preserved\"\r\n"+
			"SINGLE='literal \\n'\r\n"+
			"BACKTICK=`literal # preserved`\r\n"+
			"ESCAPED=\"first\\nsecond\\rend\"\r\n"+
			"MULTILINE=\"first\r\nsecond\"\r\n"+
			"LITERAL=$EXPORTED\r\n"+
			"SHARED=base\r\n"+
			"not a dotenv assignment\r\n"+
			"!INVALID=ignored\r\n",
		"SHARED=selected\nNEW=value\n",
	)

	want := map[string]string{
		"BACKTICK":  "literal # preserved",
		"EMPTY":     "",
		"ESCAPED":   "first\nsecond\rend",
		"EXPORTED":  "visible",
		"HASH":      "before",
		"INLINE":    "before",
		"LITERAL":   "$EXPORTED",
		"MULTILINE": "first\nsecond",
		"NEW":       "value",
		"QUOTED":    "value # preserved",
		"SHARED":    "selected",
		"SINGLE":    `literal \n`,
		"UNICODE":   "\u4f60\u597d",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Parse() = %#v, want %#v", got, want)
	}
}
