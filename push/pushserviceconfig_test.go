package push

import (
	"testing"
	"time"

	"github.com/uniqush/uniqush-push/conf"
)

// TestGetSecondsFallsBackRatherThanFailing covers the accessor every
// reconfigurable duration option goes through.
//
// It has no error return on purpose. Its callers store the result
// unconditionally, because SetPushServiceConfig runs again on every
// reconfiguration and a setting written only when it parsed would leave an
// earlier config's value in force after the line was deleted. That only works
// if every way of being wrong produces the default.
func TestGetSecondsFallsBackRatherThanFailing(t *testing.T) {
	const (
		fallback = 30 * time.Second
		minimum  = 1 * time.Second
		maximum  = 5 * time.Minute
	)

	testCases := []struct {
		name     string
		value    string
		present  bool
		expected time.Duration
	}{
		{name: "a plain number of seconds", value: "45", present: true, expected: 45 * time.Second},
		{name: "the minimum", value: "1", present: true, expected: 1 * time.Second},
		{name: "the maximum", value: "300", present: true, expected: 5 * time.Minute},
		{name: "absent", present: false, expected: fallback},
		{name: "empty", value: "", present: true, expected: fallback},
		{name: "not a number", value: "thirty", present: true, expected: fallback},
		{name: "a duration string, which this does not take", value: "30s", present: true, expected: fallback},
		// Out of range falls back rather than clamping: a server running on a
		// number nobody wrote is worse than one running on the documented
		// default.
		{name: "below the minimum", value: "0", present: true, expected: fallback},
		{name: "negative", value: "-5", present: true, expected: fallback},
		{name: "above the maximum", value: "301", present: true, expected: fallback},
		// Bounded as a count of seconds rather than as a duration, so that a
		// value large enough to overflow the multiplication cannot wrap into
		// something that looks acceptable.
		{name: "large enough to overflow a duration", value: "99999999999999", present: true, expected: fallback},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			file := conf.NewConfigFile()
			if testCase.present {
				file.AddOption("apns", "request_timeout", testCase.value)
			}
			config := NewPushServiceConfig(file, "apns")
			if got := config.GetSeconds("request_timeout", fallback, minimum, maximum); got != testCase.expected {
				t.Errorf("Expected %v, got %v", testCase.expected, got)
			}
		})
	}
}

// TestGetSecondsWithoutAConfigFile covers uniqush started with no config at
// all, which every accessor here has to survive.
func TestGetSecondsWithoutAConfigFile(t *testing.T) {
	config := NewPushServiceConfig(nil, "apns")
	if got := config.GetSeconds("request_timeout", 20*time.Second, time.Second, time.Minute); got != 20*time.Second {
		t.Errorf("Expected the fallback with no config file, got %v", got)
	}
}
