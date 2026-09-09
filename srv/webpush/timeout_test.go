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

// deadlineSkew is how much of a configured timeout may have elapsed between the
// deadline being set and a test reading it.
//
// Both happen inside one synchronous request against a mocked transport, so the
// real figure is microseconds and a second is three orders of magnitude of slack
// for a loaded CI runner. It was five seconds, which for a five-second timeout
// put the lower bound at zero: the assertion said a deadline existed, not that
// it was the one configured, and a push running on the one-second minimum would
// have passed it.
const deadlineSkew = time.Second

func expectAbout(t *testing.T, got, want time.Duration) {
	t.Helper()
	if got > want || got < want-deadlineSkew {
		t.Errorf("Expected a deadline of about %v (within %v), got %v", want, deadlineSkew, got)
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
	testCases := []struct {
		name string
		// preconfigure applies request_timeout=5 first, so the case has a
		// value to undo rather than merely never setting one.
		preconfigure bool
		// apply says whether the service is handed a configuration at all.
		// Without one it has never seen SetPushServiceConfig, which is its own
		// path: an embedder that registers nothing, or any reader of the
		// timeout before the first configuration arrives.
		apply   bool
		value   string
		present bool
	}{
		{name: "never configured"},
		{name: "configured without the option", apply: true},
		{name: "removed after being set", preconfigure: true, apply: true},
		{name: "not a number", preconfigure: true, apply: true, value: "quickly", present: true},
		{name: "below the minimum", preconfigure: true, apply: true, value: "0", present: true},
		{name: "beyond the maximum", preconfigure: true, apply: true, value: "3600", present: true},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			// A service per case, so that "never configured" means a service
			// that has never been configured rather than one the previous case
			// left behind.
			service := newTestService(t, func(*http.Request) (*http.Response, error) {
				return newResponse(201, nil), nil
			})

			if testCase.preconfigure {
				configureTimeout(t, service, "5", true)
			}
			if testCase.apply {
				configureTimeout(t, service, testCase.value, testCase.present)
			}
			expectAbout(t, deadlineOfNextPush(t, service), defaultRequestTimeout)
		})
	}
}
