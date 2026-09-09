package webpush

import (
	"net/http"
	"testing"
	"time"

	"github.com/uniqush/uniqush-push/conf"
	"github.com/uniqush/uniqush-push/push"
)

// configureTimeout applies a [webpush] section holding request_timeout, or one
// holding nothing when present is false.
//
// The whole section is rebuilt each time, because that is what a
// reconfiguration does: SetPushServiceConfig is handed the new file, not a
// patch against the old one.
func configureTimeout(t *testing.T, service *pushService, value string, present bool) {
	t.Helper()
	file := conf.NewConfigFile()
	if present {
		file.AddOption("webpush", "request_timeout", value)
	}
	service.SetPushServiceConfig(push.NewPushServiceConfig(file, "webpush"))
	// SetPushServiceConfig republishes the endpoint policy, which closes the
	// one this test's fake private endpoint needs.
	allowPrivate(service, true)
}

// deadlineOfNextPush sends one push and reports how long the request it made
// had left to run.
func deadlineOfNextPush(t *testing.T, service *pushService) time.Duration {
	t.Helper()
	var remaining time.Duration
	var seen bool
	service.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Body != nil {
			defer r.Body.Close()
		}
		if deadline, ok := r.Context().Deadline(); ok {
			remaining = time.Until(deadline)
			seen = true
		}
		return newResponse(201, nil), nil
	})
	psp := newTestPSP(t, service)
	dp := newTestDP(t, service)
	if result := pushOnce(t, service, psp, dp, &push.Notification{Data: map[string]string{"msg": "x"}}); result.Err != nil {
		t.Fatalf("Unexpected error: %v", result.Err)
	}
	if !seen {
		t.Fatal("The request carried no deadline, so nothing bounds a push to a server that never answers")
	}
	return remaining
}

func expectAbout(t *testing.T, got, want time.Duration) {
	t.Helper()
	if got > want || got < want-5*time.Second {
		t.Errorf("Expected a deadline of about %v, got %v", want, got)
	}
}

// TestRequestTimeoutIsConfigurable matters more here than for the vendor
// backends.
//
// A Web Push endpoint is chosen by whoever called /subscribe, so uniqush is
// pushing to a host it knows nothing about -- a self-hosted ntfy on a slow
// link, or Mozilla's autopush. One default cannot be right for both.
func TestRequestTimeoutIsConfigurable(t *testing.T) {
	service := newTestService(t, func(*http.Request) (*http.Response, error) {
		return newResponse(201, nil), nil
	})

	configureTimeout(t, service, "5", true)
	expectAbout(t, deadlineOfNextPush(t, service), 5*time.Second)
}

// TestRequestTimeoutFallsBackToTheDefault covers the reconfiguration that
// removes the option, alongside the values that cannot be used.
func TestRequestTimeoutFallsBackToTheDefault(t *testing.T) {
	service := newTestService(t, func(*http.Request) (*http.Response, error) {
		return newResponse(201, nil), nil
	})

	testCases := []struct {
		name    string
		value   string
		present bool
	}{
		{name: "never configured", present: false},
		{name: "removed after being set", present: false},
		{name: "not a number", value: "quickly", present: true},
		{name: "below the minimum", value: "0", present: true},
		{name: "beyond the maximum", value: "3600", present: true},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			configureTimeout(t, service, "5", true)
			configureTimeout(t, service, testCase.value, testCase.present)
			expectAbout(t, deadlineOfNextPush(t, service), defaultRequestTimeout)
		})
	}
}
