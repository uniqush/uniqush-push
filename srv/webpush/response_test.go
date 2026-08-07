package webpush

import (
	"net/http"
	"testing"
	"time"
)

func TestClassifyStatus(t *testing.T) {
	testCases := []struct {
		statusCode int
		expected   outcome
	}{
		// The spec mandates 201 but tells application servers to accept the
		// whole 2xx range.
		{200, outcomeSuccess},
		{201, outcomeSuccess},
		{202, outcomeSuccess},
		{299, outcomeSuccess},

		// Permanently dead subscriptions.
		{404, outcomeUnsubscribe},
		{410, outcomeUnsubscribe},

		// Our fault. Retrying the same bytes cannot help, and unsubscribing
		// would throw away a working subscription.
		{400, outcomeBadNotification},
		{413, outcomeBadNotification},

		// Transient.
		{429, outcomeRetry},
		{500, outcomeRetry},
		{502, outcomeRetry},
		{503, outcomeRetry},

		// Unexpected. A 3xx should never reach here, because redirects are not
		// followed; treat it as retryable rather than silently dropping.
		{301, outcomeRetry},
		{307, outcomeRetry},
		{401, outcomeRetry},
		{403, outcomeRetry},
	}

	for _, testCase := range testCases {
		if got := classifyStatus(testCase.statusCode); got != testCase.expected {
			t.Errorf("classifyStatus(%d) = %v, expected %v", testCase.statusCode, got, testCase.expected)
		}
	}
}

func TestRetryAfter(t *testing.T) {
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)

	testCases := []struct {
		name     string
		value    string
		expected time.Duration
	}{
		{name: "absent", value: "", expected: 0},
		{name: "delay seconds", value: "120", expected: 120 * time.Second},
		{name: "zero seconds", value: "0", expected: 0},
		{name: "negative seconds", value: "-5", expected: 0},
		{name: "unparseable", value: "soon please", expected: 0},
		{
			name:     "http date in the future",
			value:    "Fri, 07 Aug 2026 12:02:00 GMT",
			expected: 2 * time.Minute,
		},
		{
			// A date already in the past means retry now, not never.
			name:     "http date in the past",
			value:    "Fri, 07 Aug 2026 11:00:00 GMT",
			expected: 0,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			header := http.Header{}
			if testCase.value != "" {
				header.Set("Retry-After", testCase.value)
			}
			if got := retryAfter(header, now); got != testCase.expected {
				t.Errorf("retryAfter(%q) = %v, expected %v", testCase.value, got, testCase.expected)
			}
		})
	}
}
