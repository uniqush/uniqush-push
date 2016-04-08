package srv

import (
	"encoding/json"
	"testing"
)
import (
	"github.com/uniqush/uniqush-push/push"
)

func testADMNotifToMessage(t *testing.T, postData map[string]string, expectedPayload string) {
	notif := push.NewEmptyNotification()
	notif.Data = postData
	msg, err := notifToMessage(notif)
	if err != nil {
		t.Fatalf("Encountered error %v\n", err)
	}
	payload, jsonErr := json.Marshal(msg)
	if jsonErr != nil {
		t.Fatalf("Encountered error decoding json: %v\n", jsonErr)
	}
	if string(payload) != expectedPayload {
		t.Errorf("Expected %s, got %s", expectedPayload, string(payload))
	}
}

func TestADMNotifToMessageWithRawPayload(t *testing.T) {
	postData := map[string]string{
		"msggroup":            "somegroup",
		"uniqush.payload.adm": `{"baz":"bat","foo":"bar"}`,
		"ignoredParam":        "foo",
	}
	expectedPayload := `{"data":{"baz":"bat","foo":"bar"},"consolidationKey":"somegroup"}`
	testADMNotifToMessage(t, postData, expectedPayload)
}

func TestADMNotifToMessageWithRawPayloadAndTTL(t *testing.T) {
	postData := map[string]string{
		"uniqush.payload.adm": `{"foo":"bar"}`,
		"ttl": "100",
	}
	expectedPayload := `{"data":{"foo":"bar"},"expiresAfter":100}`
	testADMNotifToMessage(t, postData, expectedPayload)
}

func TestADMNotifToMessageWithTTL(t *testing.T) {
	postData := map[string]string{
		"other":     "value",
		"other.foo": "bar",
		"ttl":       "5",
		// ADM module should ignore anything it doesn't recognize begining with "uniqush.", those are reserved.
		"uniqush.payload.apns": "{}",
		"uniqush.payload.gcm":  `{"key":{},"x":"y"}`,
	}
	expectedPayload := `{"data":{"other":"value","other.foo":"bar"},"expiresAfter":5}`
	testADMNotifToMessage(t, postData, expectedPayload)
}
