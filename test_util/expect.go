package test_util

import (
	"reflect"
	"testing"
)

func ExpectEquals(t *testing.T, expected interface{}, actual interface{}, msg string) {
	t.Helper()
	if !reflect.DeepEqual(expected, actual) {
		t.Errorf("ExpectEquals failed: %s: %#v != %#v", msg, expected, actual)
	}
}

func ExpectStringEquals(t *testing.T, expected string, actual string, msg string) {
	t.Helper()
	if expected != actual {
		t.Errorf("ExpectStringEquals failed: %s: %q != %q", msg, expected, actual)
	}
}
