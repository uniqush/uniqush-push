package common

import (
	"bytes"
	"encoding/json"
	"strings"
)

// extractToken takes the remainder of a JSON-encoded string. It removes a token, converting html escaped tokens to the equivalent non-escaped token.
func extractToken(s string) (remainder string, token string) {
	// convert instances of \u00xy to a single character.
	n := len(s)
	if n < 6 {
		return "", s
	}
	if s[0] != '\\' {
		return s[1:], s[0:1]
	}
	// two backslashes, `\t`, etc. should be a single token. Don't care about "\\x.."
	if s[1] != 'u' {
		return s[2:], s[0:2]
	}

	// s begins with the 4 bytes `\u00`, the remainder is a unicode escape code.
	remainder = s[6:]
	if s[2] == '0' && s[3] == '0' {
		switch s[4:6] {
		case "22":
			token = "\\\""
		case "26":
			token = "&"
		case "3c":
			token = "<"
		case "3e":
			token = ">"
		default:
			token = s[:6]
		}
	} else {
		token = s[:6]
	}
	return remainder, token
}

// MarshalJSONUnescaped undoes the HTML escaping done by encoding/json.
func MarshalJSONUnescaped(v interface{}) ([]byte, error) {
	// Get the HTML escaped
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	s := string(b)
	if !strings.Contains(s, "\\u00") {
		return b, nil
	}
	buf := &bytes.Buffer{}
	for len(s) > 0 {
		var token string
		s, token = extractToken(s)
		buf.WriteString(token)
	}
	return buf.Bytes(), nil
}
