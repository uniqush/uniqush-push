// Package testutil contains reusable utilities for uniqush-push's unit tests (such as ExpectEquals).
package testutil

import (
	"encoding/json"
	"reflect"
	"testing"
)

// ExpectEquals will report a test error if reflect.DeepEqual(expected, actual) is false.
func ExpectEquals(t *testing.T, expected interface{}, actual interface{}, msg string) {
	t.Helper()
	if !reflect.DeepEqual(expected, actual) {
		t.Errorf("ExpectEquals failed: %s: %#v != %#v", msg, expected, actual)
	}
}

// ExpectStringEquals will report a test error if the strings expected and actual differ.
func ExpectStringEquals(t *testing.T, expected string, actual string, msg string) {
	t.Helper()
	if expected != actual {
		t.Errorf("ExpectStringEquals failed: %s: %q != %q", msg, expected, actual)
	}
}

// ExpectJSONIsEquivalent asserts that two JSON strings represent the same JSON object, possibly in different order. golang json serialization has an unpredictable order.
// Uses reflect.DeepEqual.
func ExpectJSONIsEquivalent(t *testing.T, expected []byte, actual []byte) {
	t.Helper()
	var expectedObj map[string]interface{}
	var actualObj map[string]interface{}
	if err := json.Unmarshal(expected, &expectedObj); err != nil {
		t.Fatalf("Invalid test expectation of JSON %s: %v", string(expected), err.Error())
	}
	if err := json.Unmarshal(actual, &actualObj); err != nil {
		t.Fatalf("Invalid JSON %s: %v", string(actual), err.Error())
	}
	if !reflect.DeepEqual(actualObj, expectedObj) {
		t.Errorf("%s is not equivalent to %s", actual, expected)
	}
}
