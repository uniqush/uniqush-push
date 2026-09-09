package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/uniqush/uniqush-push/push"
)

// Tests for /unsubscribe?alldevices=1.
//
// The database tests cover the removal itself. These cover the boundary: that
// the parameter is recognised before uniqush tries to build a device out of a
// request that names none, that the subscriber is still validated, and that a
// caller can tell what happened from the response.

// unsubscribeAllDatabase records what /unsubscribe asked of it.
type unsubscribeAllDatabase struct {
	recordingDatabase
	mutex      sync.Mutex
	calls      int
	service    string
	subscriber string
	removed    int
	err        error
}

func (d *unsubscribeAllDatabase) RemoveAllDeliveryPointsFromService(service, subscriber string) (int, error) {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	d.calls++
	d.service = service
	d.subscriber = subscriber
	return d.removed, d.err
}

// postUnsubscribe drives the real handler and decodes the response.
func postUnsubscribe(t *testing.T, database *unsubscribeAllDatabase, form url.Values) APIResponseDetails {
	t.Helper()

	psm := push.GetPushServiceManager()
	api := NewRestAPI(psm, silentLoggers(), "test", NewPushBackEnd(psm, database, silentLoggers()))

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, RemoveDeliveryPointFromServiceURL, strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	api.ServeHTTP(recorder, request)

	var response APISimpleResponse
	body := strings.TrimSpace(recorder.Body.String())
	if err := json.Unmarshal([]byte(body), &response); err != nil {
		t.Fatalf("Could not decode the /unsubscribe response %q: %v", body, err)
	}
	return response.Details
}

func allDevicesForm() url.Values {
	return url.Values{
		"service":    {"chat"},
		"subscriber": {"alice"},
		"alldevices": {"1"},
	}
}

// TestUnsubscribeAllDevicesNeedsNoDevice is the point of the parameter.
//
// Every other subscription request is built into a delivery point first, which
// needs a pushservicetype and a token. This one names no device -- the caller
// is deleting an account and does not know what the account had -- so
// recognising it has to come before that build, or the request is refused for
// missing the thing it exists not to need.
func TestUnsubscribeAllDevicesNeedsNoDevice(t *testing.T) {
	database := &unsubscribeAllDatabase{removed: 3}
	details := postUnsubscribe(t, database, allDevicesForm())

	if details.Code != UNIQUSH_SUCCESS {
		t.Fatalf("Expected success, got %s (%v)", details.Code, details.ErrorMsg)
	}
	if database.calls != 1 {
		t.Fatalf("Expected one bulk removal, got %d", database.calls)
	}
	if database.service != "chat" || database.subscriber != "alice" {
		t.Errorf("Removed devices for service %q subscriber %q", database.service, database.subscriber)
	}
	if details.DevicesRemoved == nil || *details.DevicesRemoved != 3 {
		t.Errorf("Expected the response to report 3 devices removed, got %v", details.DevicesRemoved)
	}
}

// TestUnsubscribeAllDevicesReportsRemovingNone covers what the issue asked for
// in as many words: this should report success even if there was nothing to
// delete.
//
// The count is reported as 0 rather than omitted, so a caller can tell "the
// account had no devices" from "this uniqush does not know the parameter".
func TestUnsubscribeAllDevicesReportsRemovingNone(t *testing.T) {
	database := &unsubscribeAllDatabase{removed: 0}
	details := postUnsubscribe(t, database, allDevicesForm())

	if details.Code != UNIQUSH_SUCCESS {
		t.Fatalf("Expected removing nothing to be a success, got %s (%v)", details.Code, details.ErrorMsg)
	}
	if details.DevicesRemoved == nil {
		t.Fatal("Expected the response to report a count of 0, not to omit it")
	}
	if *details.DevicesRemoved != 0 {
		t.Errorf("Expected 0 devices removed, got %d", *details.DevicesRemoved)
	}
}

// TestUnsubscribeAllDevicesValidatesTheSubscriber is the blast-radius test.
//
// This deletes every device behind a name. A wildcard reaching the database
// would empty every subscriber it matched, so the same validation every other
// subscription operation applies has to apply here -- and /push's unvalidated
// subscriber parameter is a few lines away in the same file.
func TestUnsubscribeAllDevicesValidatesTheSubscriber(t *testing.T) {
	for _, subscriber := range []string{"*", "alice*", "alice bob", "`whoami`"} {
		database := &unsubscribeAllDatabase{removed: 99}
		form := allDevicesForm()
		form.Set("subscriber", subscriber)

		details := postUnsubscribe(t, database, form)
		if details.Code == UNIQUSH_SUCCESS {
			t.Errorf("Subscriber %q was accepted", subscriber)
		}
		if database.calls != 0 {
			t.Errorf("Subscriber %q reached the database", subscriber)
		}
	}
}

// TestUnsubscribeOneDeviceStillWorks checks the parameter is opt-in, and that
// anything other than 1 leaves the ordinary path alone.
func TestUnsubscribeOneDeviceStillWorks(t *testing.T) {
	registerAddPSPTestTypeOnce.Do(func() {
		if err := push.GetPushServiceManager().RegisterPushServiceType(&echoingPushServiceType{}); err != nil {
			t.Fatalf("Could not register the test push service type: %v", err)
		}
	})

	for _, value := range []string{"", "0", "true", "yes"} {
		database := &unsubscribeAllDatabase{removed: 99}
		form := allDevicesForm()
		form.Set("pushservicetype", addPSPTestType)
		form.Set("devtoken", "sometoken")
		if value == "" {
			form.Del("alldevices")
		} else {
			form.Set("alldevices", value)
		}

		postUnsubscribe(t, database, form)
		if database.calls != 0 {
			t.Errorf("alldevices=%q took the bulk path; only 1 asks for it", value)
		}
	}
}
