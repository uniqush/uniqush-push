package util

import (
	"bytes"
	"encoding/json"
	"testing"
)

// TestMarshalJSONUnescaped tests that HTML encoding is properly removed from json.Marshal, so that APNS receives a payload it supports.
func TestMarshalJSONUnescaped(t *testing.T) {
	testValues := []string{
		`null`,              // Null
		`{"a":"\\u003c"}`,   // Double backslashes, not an escape sequence in json
		`{"a":"\\\\u003c"}`, // Quadruple backslashes, not an escape sequence in json
		`{"a":"\u0019"}`,    // An ASCII control code. Keep it escaped.
		`{"<a":"<&>"}`,
		`"<&>\""`,
		`{"a":">\""}`, // A quotation mark. Should use backslashes to escape instead of unicode escape sequence
		`{"a":"\u0000\u0001\u0002\u0003\u0004\u0005\u0006\u0007\u0008\t\n\u000b\u000c\r\u000e\u000f\u0010\u0011\u0012\u0013\u0014\u0015\u0016\u0017\u0018\u0019\u001a\u001b\u001c\u001d\u001e\u001f !\"#$%&'()*+,-.\\/0123456789:;<=>?@ABCDEFGHIJKLMNOPQRSTUVWXYZ[]^_abcdefghijklmnopqrstuvwxyz{|}~"}`,
		`{"a":"한국어/조선말"}`, // unicode should continue to work.
	}

	for _, testValue := range testValues {
		var data interface{}
		originalBytes := []byte(testValue)
		err := json.Unmarshal(originalBytes, &data)
		if err != nil {
			t.Fatalf("Invalid test value %q: %v", testValue, err)
		}
		reencoded, err := MarshalJSONUnescaped(data)
		if !bytes.Equal(reencoded, originalBytes) {
			t.Errorf("Expected %v(%s), got %v(%s)", originalBytes, testValue, reencoded, string(reencoded))
		}
	}
}
