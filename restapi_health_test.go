package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/uniqush/uniqush-push/push"
)

// Tests for /health.
//
// The endpoint exists to be read by a machine deciding whether to send traffic
// here, so the HTTP status code is the load-bearing part of the answer and the
// body is for whoever is looking at it afterwards.

// healthDatabase is a recordingDatabase whose reachability the test controls.
type healthDatabase struct {
	recordingDatabase
	pings   int
	pingErr error
}

func (d *healthDatabase) Ping() error {
	d.pings++
	return d.pingErr
}

// getHealth drives the real handler and returns the recorder alongside the
// decoded body.
func getHealth(t *testing.T, database *healthDatabase) (*httptest.ResponseRecorder, HealthResponse) {
	t.Helper()

	psm := push.GetPushServiceManager()
	api := NewRestAPI(psm, silentLoggers(), "uniqush-push test", NewPushBackEnd(psm, database, silentLoggers()))

	recorder := httptest.NewRecorder()
	api.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, HealthURL, nil))

	var response HealthResponse
	body := strings.TrimSpace(recorder.Body.String())
	if err := json.Unmarshal([]byte(body), &response); err != nil {
		t.Fatalf("Could not decode the /health response %q: %v", body, err)
	}
	return recorder, response
}

// TestHealthIsOKWhenTheDatabaseAnswers covers the ordinary case.
func TestHealthIsOKWhenTheDatabaseAnswers(t *testing.T) {
	database := &healthDatabase{}
	recorder, response := getHealth(t, database)

	if recorder.Code != http.StatusOK {
		t.Errorf("Expected 200, got %d", recorder.Code)
	}
	if response.Status != "ok" || response.Database != "ok" {
		t.Errorf("Expected a healthy response, got %+v", response)
	}
	if response.Code != UNIQUSH_SUCCESS {
		t.Errorf("Expected %s, got %s", UNIQUSH_SUCCESS, response.Code)
	}
	if response.Version != "uniqush-push test" {
		t.Errorf("Expected the version to be reported, got %q", response.Version)
	}
	if database.pings != 1 {
		t.Errorf("Expected the database to be asked exactly once, got %d", database.pings)
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Expected a JSON content type, got %q", got)
	}
}

// TestHealthIs503WhenTheDatabaseIsUnreachable is the case the endpoint exists
// for.
//
// The status code is what a load balancer reads, and it has to be a failure:
// an instance that cannot reach redis can serve no push and no subscription
// change, so sending it traffic only produces errors with a longer wait.
func TestHealthIs503WhenTheDatabaseIsUnreachable(t *testing.T) {
	database := &healthDatabase{pingErr: errors.New("could not reach redis: dial tcp 127.0.0.1:6379: connect: connection refused")}
	recorder, response := getHealth(t, database)

	// 503 rather than 500: this instance cannot serve now and may be able to
	// later, which is the distinction a load balancer acts on.
	if recorder.Code != http.StatusServiceUnavailable {
		t.Errorf("Expected 503, got %d", recorder.Code)
	}
	if response.Status != "unhealthy" {
		t.Errorf("Expected an unhealthy status, got %q", response.Status)
	}
	if response.Code != UNIQUSH_ERROR_DATABASE {
		t.Errorf("Expected %s, got %s", UNIQUSH_ERROR_DATABASE, response.Code)
	}
	// The reason, not just the verdict: whoever reads this is deciding whether
	// uniqush is the broken thing or the thing reporting something else broken.
	if !strings.Contains(response.Database, "connection refused") {
		t.Errorf("Expected the response to say what went wrong, got %q", response.Database)
	}
}

// TestHealthAsksTheDatabaseEveryTime guards against the answer being cached.
//
// A health check that remembers a previous answer reports an instance as
// healthy after it has stopped being so, which is worse than not having one:
// the load balancer keeps sending traffic and the failure looks like the
// application's.
func TestHealthAsksTheDatabaseEveryTime(t *testing.T) {
	database := &healthDatabase{}

	if recorder, _ := getHealth(t, database); recorder.Code != http.StatusOK {
		t.Fatalf("Expected the first check to pass, got %d", recorder.Code)
	}
	database.pingErr = errors.New("could not reach redis: i/o timeout")
	recorder, response := getHealth(t, database)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Errorf("Expected the check to notice redis had gone, got %d", recorder.Code)
	}
	if response.Status != "unhealthy" {
		t.Errorf("Expected an unhealthy status, got %q", response.Status)
	}
	if database.pings != 2 {
		t.Errorf("Expected one ping per request, got %d", database.pings)
	}
}
