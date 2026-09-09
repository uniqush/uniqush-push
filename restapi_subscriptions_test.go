package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/uniqush/uniqush-push/log"
	"github.com/uniqush/uniqush-push/push"
)

// Tests for what /subscriptions hands back about a device.
//
// The endpoint returns each delivery point's stored fields so that an
// application can reconcile them against its own records. For APNs and FCM
// those fields identify a device and are useless without the provider
// credentials uniqush holds. A Web Push subscription is not like that: RFC 8291
// derives the content encryption key from the auth secret, so endpoint, p256dh
// and auth together are everything needed to push to that browser, from
// anywhere, with no credential of uniqush's involved.

// subscriptionAuth is the value that must not come back by default.
const subscriptionAuth = "SECRET-webpush-auth"

// subscriptionsDatabase is a recordingDatabase with subscriptions to return.
type subscriptionsDatabase struct {
	recordingDatabase
	subscriptions []map[string]string
}

func (d *subscriptionsDatabase) GetSubscriptions([]string, string, log.Logger) ([]map[string]string, error) {
	// Copied, because the handler edits what it is given and a test that ran
	// twice against one fixture would otherwise measure the first run.
	copied := make([]map[string]string, 0, len(d.subscriptions))
	for _, subscription := range d.subscriptions {
		fields := make(map[string]string, len(subscription))
		for key, value := range subscription {
			fields[key] = value
		}
		copied = append(copied, fields)
	}
	return copied, nil
}

func webpushSubscription() map[string]string {
	return map[string]string{
		"service":         "chat",
		"pushservicetype": "webpush",
		"endpoint":        "https://updates.push.services.mozilla.com/wpush/v2/abc",
		"p256dh":          "BF-public-key",
		"auth":            subscriptionAuth,
	}
}

func apnsSubscription() map[string]string {
	return map[string]string{
		"service":         "chat",
		"pushservicetype": "apns",
		"devtoken":        "0123456789abcdef",
	}
}

// querySubscriptions drives the real handler and returns the raw body and the
// decoded subscriptions.
func querySubscriptions(t *testing.T, query string, subscriptions ...map[string]string) (string, []map[string]string) {
	t.Helper()

	psm := push.GetPushServiceManager()
	database := &subscriptionsDatabase{subscriptions: subscriptions}
	api := NewRestAPI(psm, silentLoggers(), "test", NewPushBackEnd(psm, database, silentLoggers()))

	recorder := httptest.NewRecorder()
	url := QuerySubscriptionsURL + "?subscriber=alice" + query
	api.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, url, nil))

	body := recorder.Body.String()
	var decoded []map[string]string
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("Could not decode the /subscriptions response %q: %v", body, err)
	}
	return body, decoded
}

// TestSubscriptionsWithholdsTheWebPushAuthSecret is the regression test for
// #319.
//
// Asserted against the whole body rather than one field, because what is being
// prevented is the secret appearing anywhere in the response.
func TestSubscriptionsWithholdsTheWebPushAuthSecret(t *testing.T) {
	body, decoded := querySubscriptions(t, "", webpushSubscription())

	if strings.Contains(body, subscriptionAuth) {
		t.Errorf("/subscriptions returned the auth secret by default.\n"+
			"With the endpoint and p256dh beside it, that is enough to push to this browser "+
			"from anywhere, with no credential of uniqush's involved.\nResponse: %s", body)
	}
	if len(decoded) != 1 {
		t.Fatalf("Expected one subscription, got %d", len(decoded))
	}
	if _, present := decoded[0]["auth"]; present {
		t.Error("Expected auth to be absent rather than blank or standing in for itself: a caller " +
			"writing this into its own store should end up with no key, not a fake one")
	}
}

// TestSubscriptionsKeepsTheIdentifyingFields is the other half. An endpoint
// that withheld everything would be secure and useless.
func TestSubscriptionsKeepsTheIdentifyingFields(t *testing.T) {
	_, decoded := querySubscriptions(t, "", webpushSubscription(), apnsSubscription())
	if len(decoded) != 2 {
		t.Fatalf("Expected two subscriptions, got %d", len(decoded))
	}

	byType := make(map[string]map[string]string, len(decoded))
	for _, subscription := range decoded {
		byType[subscription["pushservicetype"]] = subscription
	}

	// Web Push keeps everything that identifies the subscription. Without the
	// auth secret none of it can be used to encrypt a push.
	webpush := byType["webpush"]
	for key, want := range map[string]string{
		"service":  "chat",
		"endpoint": "https://updates.push.services.mozilla.com/wpush/v2/abc",
		"p256dh":   "BF-public-key",
	} {
		if got := webpush[key]; got != want {
			t.Errorf("Expected %s=%q, got %q", key, want, got)
		}
	}

	// A device token is not credential material: it is useless without the
	// certificate or signing key uniqush holds, and matching one against an
	// application's own records is what this endpoint is for.
	if got := byType["apns"]["devtoken"]; got != "0123456789abcdef" {
		t.Errorf("Expected the APNs device token to be returned, got %q", got)
	}
}

// TestSubscriptionsReturnsSecretsWhenAsked covers the opt-in, which is what
// keeps this a change to the default rather than a removal.
//
// A caller migrating subscriptions between push servers, or rebuilding after a
// restore, needs the whole subscription. Asking for it is a deliberate act that
// shows up in whatever made the request.
func TestSubscriptionsReturnsSecretsWhenAsked(t *testing.T) {
	_, decoded := querySubscriptions(t, "&include_subscription_secrets=1", webpushSubscription())
	if len(decoded) != 1 {
		t.Fatalf("Expected one subscription, got %d", len(decoded))
	}
	if got := decoded[0]["auth"]; got != subscriptionAuth {
		t.Errorf("Expected the auth secret when it was asked for, got %q", got)
	}
}

// TestSubscriptionsOptInIsExact keeps the parameter from being satisfied by
// anything truthy-looking. Only "1" asks for the secret, which is how
// include_delivery_point_ids beside it is read too.
func TestSubscriptionsOptInIsExact(t *testing.T) {
	for _, query := range []string{
		"&include_subscription_secrets=0",
		"&include_subscription_secrets=true",
		"&include_subscription_secrets=yes",
		"&include_subscription_secrets=",
	} {
		body, _ := querySubscriptions(t, query, webpushSubscription())
		if strings.Contains(body, subscriptionAuth) {
			t.Errorf("%q returned the auth secret; only =1 asks for it", query)
		}
	}
}
