package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/uniqush/uniqush-push/db"
	"github.com/uniqush/uniqush-push/push"
)

// Tests for /stats, which is the boundary between a counted index and a
// dashboard.
//
// The counting itself is tested against redis in the db package. What is here
// is the handler: which parameters reach the database, and the one case where
// the answer has to be a refusal rather than a number.

type statsResponse struct {
	Services map[string]*db.ServiceStats `json:"services"`
	ErrorMsg *string                     `json:"errorMsg,omitempty"`
	Code     string                      `json:"code"`
}

func getStats(t *testing.T, database *recordingDatabase, query string) statsResponse {
	t.Helper()

	psm := push.GetPushServiceManager()
	api := NewRestAPI(psm, silentLoggers(), "test", NewPushBackEnd(psm, database, silentLoggers()))

	recorder := httptest.NewRecorder()
	api.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, QueryStatsURL+query, nil))

	var response statsResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("Could not read the response %q: %v", recorder.Body.String(), err)
	}
	return response
}

func TestStatsReturnsTheCounts(t *testing.T) {
	recent := int64(11)
	database := &recordingDatabase{stats: map[string]*db.ServiceStats{
		"myservice": {
			Subscribers:      12034,
			SubscribersSince: &recent,
			DeliveryPoints:   map[string]int64{"apns": 9000, "fcm": 4100},
		},
	}}

	response := getStats(t, database, "?service=myservice&since=1756900000")

	if response.Code != UNIQUSH_SUCCESS {
		t.Fatalf("Expected success, got %q (%v)", response.Code, response.ErrorMsg)
	}
	entry, ok := response.Services["myservice"]
	if !ok {
		t.Fatalf("Expected the service in the response, got %v", response.Services)
	}
	if entry.Subscribers != 12034 || entry.DeliveryPoints["apns"] != 9000 {
		t.Errorf("The counts did not survive the round trip: %+v", entry)
	}
	if entry.SubscribersSince == nil || *entry.SubscribersSince != 11 {
		t.Errorf("Expected subscribers_since to survive, got %+v", entry.SubscribersSince)
	}

	if len(database.statsServices) != 1 || database.statsServices[0] != "myservice" {
		t.Errorf("Expected the service to reach the database, got %v", database.statsServices)
	}
	if database.statsSince == nil || *database.statsSince != 1756900000 {
		t.Errorf("Expected since to reach the database, got %v", database.statsSince)
	}
}

// TestStatsWithoutParametersAsksForEverything covers both defaults at once: no
// service means every service, and no since means no recency count.
func TestStatsWithoutParametersAsksForEverything(t *testing.T) {
	database := &recordingDatabase{stats: map[string]*db.ServiceStats{}}

	if response := getStats(t, database, ""); response.Code != UNIQUSH_SUCCESS {
		t.Fatalf("Expected success, got %q", response.Code)
	}
	if database.statsServices != nil {
		t.Errorf("Expected no service filter, got %v", database.statsServices)
	}
	if database.statsSince != nil {
		t.Errorf("Expected no since, got %v", *database.statsSince)
	}
}

// TestStatsRefusesAnUnbuiltIndexWithItsOwnCode is the response a dashboard has
// to be able to tell apart from a small service.
func TestStatsRefusesAnUnbuiltIndexWithItsOwnCode(t *testing.T) {
	database := &recordingDatabase{statsErr: db.ErrSubscriberIndexNotBuilt}

	response := getStats(t, database, "?service=myservice")

	if response.Code != UNIQUSH_ERROR_INDEX_NOT_BUILT {
		t.Errorf("Expected %s, got %q", UNIQUSH_ERROR_INDEX_NOT_BUILT, response.Code)
	}
	if len(response.Services) != 0 {
		t.Errorf("Expected no counts alongside the refusal, got %v", response.Services)
	}
	// The message has to name the fix, since the code alone does not.
	if response.ErrorMsg == nil || *response.ErrorMsg == "" {
		t.Fatal("Expected the refusal to carry a message")
	}
}

// TestStatsRejectsAnUnreadableSince refuses rather than quietly ignoring the
// parameter, which would answer a different question than the one asked.
func TestStatsRejectsAnUnreadableSince(t *testing.T) {
	database := &recordingDatabase{stats: map[string]*db.ServiceStats{}}

	response := getStats(t, database, "?since=last+tuesday")

	if response.Code != UNIQUSH_ERROR_GENERIC {
		t.Errorf("Expected an error for an unreadable since, got %q", response.Code)
	}
	if database.calls != 0 {
		t.Errorf("Expected the request not to reach the database, got %d calls", database.calls)
	}
}

// TestStatsTrimsServiceNames keeps "a, b" meaning a and b. A service named " b"
// does not exist, and its zero counts would look like a real answer.
func TestStatsTrimsServiceNames(t *testing.T) {
	database := &recordingDatabase{stats: map[string]*db.ServiceStats{}}

	getStats(t, database, "?service=first,%20second%20,,")

	if len(database.statsServices) != 2 || database.statsServices[0] != "first" || database.statsServices[1] != "second" {
		t.Errorf("Expected [first second], got %q", database.statsServices)
	}
}
