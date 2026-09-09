package fcm

import (
	"net/http"
	"testing"
	"time"

	"github.com/uniqush/uniqush-push/conf"
	"github.com/uniqush/uniqush-push/push"
)

// configureTimeout applies a [fcm] section holding request_timeout, or one
// holding nothing when present is false.
func configureTimeout(t *testing.T, service *pushService, value string, present bool) {
	t.Helper()
	file := conf.NewConfigFile()
	if present {
		file.AddOption("fcm", "request_timeout", value)
	}
	service.SetPushServiceConfig(push.NewPushServiceConfig(file, "fcm"))
}

// deadlineOfNextPush sends one push and reports how long the request it made
// had left to run.
func deadlineOfNextPush(t *testing.T, service *pushService) time.Duration {
	t.Helper()
	var remaining time.Duration
	var seen bool
	service.OverrideClientFactory(func(*push.PushServiceProvider) (HTTPClient, error) {
		return &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if deadline, ok := r.Context().Deadline(); ok {
				remaining = time.Until(deadline)
				seen = true
			}
			return newResponse(200, `{"name":"n"}`, nil), nil
		})}, nil
	})
	psp := newTestPSP(t, service)
	dp := newTestDP(t, service, "token-1")
	if result := pushOnce(t, service, psp, dp, &push.Notification{Data: map[string]string{"msg": "x"}}); result.Err != nil {
		t.Fatalf("Unexpected error: %v", result.Err)
	}
	if !seen {
		t.Fatal("The request carried no deadline, so nothing bounds a push that never answers")
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

// expectAbout checks a deadline is the configured one, allowing for the time
// between setting it and reading it.
func expectAbout(t *testing.T, got, want time.Duration) {
	t.Helper()
	if got > want || got < want-deadlineSkew {
		t.Errorf("Expected a deadline of about %v (within %v), got %v", want, deadlineSkew, got)
	}
}

// TestRequestTimeoutIsConfigurable is the point of #272: 30 seconds is a
// reasonable default and a bad fit for a caller that expects /push to answer
// within five.
func TestRequestTimeoutIsConfigurable(t *testing.T) {
	service := newTestService(t, "fcm", nil)
	defer service.Finalize()

	configureTimeout(t, service, "5", true)
	expectAbout(t, deadlineOfNextPush(t, service), 5*time.Second)
}

// TestRequestTimeoutFallsBackToTheDefault covers every way of not asking for a
// timeout, including the reconfiguration that removes one.
//
// The setting is stored unconditionally, so a config that no longer names it
// has to restore the default rather than leave the previous value in place --
// the same rule allow_non_apple_endpoints and record_size follow.
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
			service := newTestService(t, "fcm", nil)
			defer service.Finalize()

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

// TestRequestTimeoutActuallyStopsAPush proves the deadline is enforced rather
// than merely attached.
//
// The client no longer carries a Timeout of its own -- it cannot, being cached
// for the life of a provider, since it would freeze whatever the timeout was
// when it was built -- so the context is the only thing standing between a push
// and a push server that never answers.
func TestRequestTimeoutActuallyStopsAPush(t *testing.T) {
	service := newTestService(t, "fcm", nil)
	defer service.Finalize()
	configureTimeout(t, service, "1", true)

	service.OverrideClientFactory(func(*push.PushServiceProvider) (HTTPClient, error) {
		return &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			<-r.Context().Done()
			return nil, r.Context().Err()
		})}, nil
	})

	psp := newTestPSP(t, service)
	dp := newTestDP(t, service, "token-1")

	start := time.Now()
	result := pushOnce(t, service, psp, dp, &push.Notification{Data: map[string]string{"msg": "x"}})
	if result.Err == nil {
		t.Fatal("Expected a push that outran its timeout to fail")
	}
	if _, isConnection := result.Err.(*push.ConnectionError); !isConnection {
		t.Errorf("Expected a ConnectionError, got %T: %v", result.Err, result.Err)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Errorf("Expected the push to give up after about a second, took %v", elapsed)
	}
}
