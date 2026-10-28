package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/uniqush/uniqush-push/db"
	"github.com/uniqush/uniqush-push/push"
)

func getMetrics(t *testing.T, api *RestAPI) string {
	t.Helper()
	recorder := httptest.NewRecorder()
	api.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, MetricsURL, nil))
	if got := recorder.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/plain; version=0.0.4") {
		t.Errorf("Expected the Prometheus text content type, got %q", got)
	}
	return recorder.Body.String()
}

func TestMetricsReportPushResultsRequestsAndSubscribers(t *testing.T) {
	registerAddPSPTestTypeOnce.Do(func() {
		if err := push.GetPushServiceManager().RegisterPushServiceType(&echoingPushServiceType{}); err != nil {
			t.Fatalf("Could not register the test push service type: %v", err)
		}
	})
	psm := push.GetPushServiceManager()
	// A backslash is a legal service name character, and has to be escaped in
	// a label value.
	database := &recordingDatabase{stats: map[string]*db.ServiceStats{
		`svc\a`: {Subscribers: 3, DeliveryPoints: map[string]int64{addPSPTestType: 5}},
	}}
	backend := NewPushBackEnd(psm, database, silentLoggers())
	api := NewRestAPI(psm, silentLoggers(), "uniqush-push test", backend)

	psp, err := psm.BuildPushServiceProviderFromMap(map[string]string{"pushservicetype": addPSPTestType, "service": "svc"})
	if err != nil {
		t.Fatal(err)
	}
	dp, err := psm.BuildDeliveryPointFromMap(map[string]string{"pushservicetype": addPSPTestType, "service": "svc", "subscriber": "sub"})
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan *push.Result, 4)
	results <- &push.Result{Provider: psp, Destination: dp}
	results <- &push.Result{Provider: psp, Destination: dp, Err: push.NewErrorForDeliveryPoint(dp, "boom")}
	results <- &push.Result{Provider: psp, Destination: dp, Err: push.NewInvalidRegistrationUpdate(psp, dp)}
	// Already past the one-minute ceiling, so this is abandoned, not retried.
	results <- &push.Result{Provider: psp, Destination: dp, Err: push.NewRetryError(psp, dp, push.NewEmptyNotification(), 0)}
	close(results)
	backend.collectResult("rid", "addr", "svc", nil, "", results, backend.loggers[LoggerPush], 2*time.Minute, &NullAPIResponseHandler{})

	// Reported asynchronously. A notice about no device
	// is not a push result. The last send returns once the one before it has
	// been handled.
	backend.errChan <- push.NewError("uniqush.http2=0 is ignored")
	backend.errChan <- push.NewInvalidRegistrationUpdate(psp, dp)
	backend.errChan <- push.NewError("flush")

	api.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, VersionInfoURL, nil))
	body := getMetrics(t, api)

	for _, want := range []string{
		`uniqush_push_results_total{service="svc",push_service_type="addpsptest",result="success"} 1`,
		`uniqush_push_results_total{service="svc",push_service_type="addpsptest",result="error"} 1`,
		`uniqush_push_results_total{service="svc",push_service_type="addpsptest",result="device_removed"} 2`,
		`uniqush_push_results_total{service="svc",push_service_type="addpsptest",result="retry"} 1`,
		`uniqush_push_retries_abandoned_total{service="svc",push_service_type="addpsptest"} 1`,
		`uniqush_http_requests_total{path="/version"} 1`,
		`uniqush_http_requests_total{path="/metrics"} 1`,
		`uniqush_build_info{version="test"} 1`,
		`uniqush_database_up 1`,
		`uniqush_subscribers{service="svc\\a"} 3`,
		`uniqush_delivery_points{service="svc\\a",push_service_type="addpsptest"} 5`,
		"# TYPE uniqush_push_results_total counter",
	} {
		if !strings.Contains(body, want+"\n") {
			t.Errorf("Expected %q in:\n%s", want, body)
		}
	}
	if strings.Contains(body, `push_service_type="unknown"`) {
		t.Errorf("Expected notices not to be counted as push results:\n%s", body)
	}
}

// Subscriber gauges are left out rather than reported as zero when they cannot
// be read, so a dashboard shows a gap instead of an empty service.
func TestMetricsOmitSubscriberGaugesTheDatabaseCannotAnswer(t *testing.T) {
	defer func(timeout time.Duration) { metricsDatabaseTimeout = timeout }(metricsDatabaseTimeout)
	metricsDatabaseTimeout = 10 * time.Millisecond
	release := make(chan struct{})
	defer close(release)

	for _, tc := range []struct {
		name     string
		database db.PushDatabase
		up       string
	}{
		{"database unreachable", &healthDatabase{pingErr: errors.New("connection refused")}, "uniqush_database_up 0\n"},
		{"index not built", &recordingDatabase{statsErr: db.ErrSubscriberIndexNotBuilt}, "uniqush_database_up 1\n"},
		{"database hangs", &hangingDatabase{release: release}, "uniqush_database_up 0\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			psm := push.GetPushServiceManager()
			body := getMetrics(t, NewRestAPI(psm, silentLoggers(), "test", NewPushBackEnd(psm, tc.database, silentLoggers())))
			if !strings.Contains(body, tc.up) {
				t.Errorf("Expected %q in:\n%s", tc.up, body)
			}
			if strings.Contains(body, "uniqush_subscribers") || strings.Contains(body, "uniqush_delivery_points") {
				t.Errorf("Expected no subscriber gauges in:\n%s", body)
			}
		})
	}
}

type hangingDatabase struct {
	recordingDatabase
	release chan struct{}
}

func (d *hangingDatabase) Ping() error {
	<-d.release
	return nil
}
