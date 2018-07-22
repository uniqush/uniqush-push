// Package util contains miscellaneous utilities that are used throughout uniqush-push.
package util

import (
	"bytes"
	"encoding/json"
)

// MarshalJSONUnescaped uses encoding/json to return a JSON string without escapes for unicode and special characters in HTML.
func MarshalJSONUnescaped(v interface{}) ([]byte, error) {
	// Get the HTML escaped
	writer := bytes.Buffer{}
	encoder := json.NewEncoder(&writer)
	encoder.SetEscapeHTML(false)
	err := encoder.Encode(v)
	if err != nil {
		return nil, err
	}

	bytes := writer.Bytes()
	return bytes[:len(bytes)-1], nil
}
