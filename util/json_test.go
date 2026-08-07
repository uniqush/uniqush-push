package util

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// htmlEscapes are the escape sequences encoding/json emits by default and that
// MarshalJSONUnescaped exists to suppress. APNs rejects payloads containing them.
var htmlEscapes = []string{`\u003c`, `\u003e`, `\u0026`}

// TestMarshalJSONUnescaped tests that HTML escaping is removed from
// encoding/json's output, so that APNs receives a payload it supports.
//
// This deliberately does not assert byte-for-byte equality with the input.
// encoding/json makes no promise about which escape form it uses for a given
// control character, and the answer has changed: Go now emits the two-character
// escapes \b and \f where it once emitted \u0008 and \u000c. Both are
// valid JSON and both are fine for APNs. Pinning the exact bytes made this test
// fail on a stdlib change that was not a regression.
//
// What we actually care about is that the round trip is lossless and that <, >
// and & come through literally rather than as \u003c, \u003e and
// \u0026.
func TestMarshalJSONUnescaped(t *testing.T) {
	testValues := []string{
		`null`,              // Null
		`{"a":"\\u003c"}`,   // Double backslashes, not an escape sequence in json
		`{"a":"\\\\u003c"}`, // Quadruple backslashes, not an escape sequence in json
		`{"a":"\u0019"}`,    // An ASCII control code. Keep it escaped.
		`{"<a":"<&>"}`,
		`"<&>\""`,
		`{"a":">\""}`, // A quotation mark. Should use backslashes to escape instead of unicode escape sequence
		`{"a":"\u0000\u0001\u0007\u0008\t\n\u000b\u000c\r\u001f !\"#$%&'()*+,-.\\/0123456789:;<=>?@ABCDEFGHIJKLMNOPQRSTUVWXYZ[]^_abcdefghijklmnopqrstuvwxyz{|}~"}`,
		`{"a":"한국어/조선말"}`, // unicode should continue to work.
	}

	for _, testValue := range testValues {
		var original interface{}
		if err := json.Unmarshal([]byte(testValue), &original); err != nil {
			t.Fatalf("Invalid test value %q: %v", testValue, err)
		}

		reencoded, err := MarshalJSONUnescaped(original)
		if err != nil {
			t.Errorf("MarshalJSONUnescaped(%s) returned an error: %v", testValue, err)
			continue
		}

		// (a) The round trip must be lossless.
		var roundTripped interface{}
		if err := json.Unmarshal(reencoded, &roundTripped); err != nil {
			t.Errorf("MarshalJSONUnescaped(%s) produced invalid JSON %q: %v", testValue, reencoded, err)
			continue
		}
		if !reflect.DeepEqual(original, roundTripped) {
			t.Errorf("Round trip of %s changed the data: got %q", testValue, reencoded)
		}

		// (b) HTML escaping must be suppressed. Note that the two backslash test
		// cases contain a literal backslash followed by "u003c", which is not an
		// escape sequence; they survive because the backslash is itself escaped,
		// so the encoder emits a doubled backslash. Collapse those first so they
		// are not mistaken for the encoder having HTML-escaped anything.
		out := strings.ReplaceAll(string(reencoded), `\\`, ``)
		for _, escape := range htmlEscapes {
			if strings.Contains(out, escape) {
				t.Errorf("MarshalJSONUnescaped(%s) left an HTML escape %s in %q", testValue, escape, reencoded)
			}
		}
	}
}

// TestMarshalJSONUnescapedKeepsControlCharactersEscaped guards the other half of
// the contract: control characters must not be emitted raw, since raw C0 bytes
// are not legal inside a JSON string. It does not care which escape form is used.
func TestMarshalJSONUnescapedKeepsControlCharactersEscaped(t *testing.T) {
	encoded, err := MarshalJSONUnescaped(map[string]string{"a": "\x00\x08\x0c\x19\x1f"})
	if err != nil {
		t.Fatalf("MarshalJSONUnescaped returned an error: %v", err)
	}
	for _, b := range encoded {
		if b < 0x20 {
			t.Errorf("Found raw control byte %#x in output %q", b, encoded)
		}
	}
}
