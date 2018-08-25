package srv

import (
	"testing"
)
import (
	"github.com/uniqush/uniqush-push/push"
)

func testToGCMPayload(t *testing.T, postData map[string]string, regIds []string, expectedPayload string) {
	t.Helper()
	notif := push.NewEmptyNotification()
	notif.Data = postData
	// Create a push service, just for the sake of realistically testing building payloads
	stubPushService := newGCMPushService()
	defer stubPushService.Finalize()
	payload, err := stubPushService.ToCMPayload(notif, regIds)
	if err != nil {
		t.Fatalf("Encountered error %v\n", err)
	}
	if string(payload) != expectedPayload {
		t.Errorf("Expected %s, got %s", expectedPayload, string(payload))
	}
}

func TestToGCMPayloadWithRawPayload(t *testing.T) {
	postData := map[string]string{
		"msggroup":            "somegroup",
		"uniqush.payload.gcm": `{"message":{"key": {},"x":"y"},"other":{}}`,
		"foo":                 "bar", // ignored
	}
	regIds := []string{"CAFE1-FF", "42-607"}
	expectedPayload := `{"registration_ids":["CAFE1-FF","42-607"],"collapse_key":"somegroup","time_to_live":3600,"data":{"message":{"key":{},"x":"y"},"other":{}}}`
	testToGCMPayload(t, postData, regIds, expectedPayload)
}

func TestToGCMPayloadWithRawEmptyPayload(t *testing.T) {
	postData := map[string]string{
		"msggroup":            "somegroup",
		"uniqush.payload.gcm": `{}`,
	}
	regIds := []string{"CAFE1-FF", "42-607"}
	expectedPayload := `{"registration_ids":["CAFE1-FF","42-607"],"collapse_key":"somegroup","time_to_live":3600,"data":{}}`
	testToGCMPayload(t, postData, regIds, expectedPayload)
}

func TestToGCMPayloadWithRawUnescapedPayload(t *testing.T) {
	postData := map[string]string{
		"msggroup":            "somegroup",
		"uniqush.payload.gcm": `{"message":{"key": {},"x":"<a☃?>\"'"},"other":{}}`,
		"foo":                 "bar",
	}
	regIds := []string{"CAFE1-FF", "42-607"}
	expectedPayload := `{"registration_ids":["CAFE1-FF","42-607"],"collapse_key":"somegroup","time_to_live":3600,"data":{"message":{"key":{},"x":"<a☃?>\"'"},"other":{}}}`
	testToGCMPayload(t, postData, regIds, expectedPayload)
}

func TestToGCMPayloadWithCommonParameters(t *testing.T) {
	postData := map[string]string{
		"msggroup":            "somegroup",
		"uniqush.payload.gcm": `{"message":{"key": {},"x":"<a☃?>\"'"},"other":{}}`,
		"foo":                 "bar",
	}
	regIds := []string{"CAFE1-FF", "42-607"}
	expectedPayload := `{"registration_ids":["CAFE1-FF","42-607"],"collapse_key":"somegroup","time_to_live":3600,"data":{"message":{"key":{},"x":"<a☃?>\"'"},"other":{}}}`
	testToGCMPayload(t, postData, regIds, expectedPayload)
}

func TestToGCMPayloadWithCommonParametersV2(t *testing.T) {
	postData := map[string]string{
		"msggroup":  "somegroup",
		"other":     "value",
		"other.foo": "bar",
		"ttl":       "5",
		// GCM module should ignore anything it doesn't recognize beginning with "uniqush.", those are reserved.
		"uniqush.payload.apns": "{}",
		"uniqush.foo":          "foo",
	}
	regIds := []string{"CAFE1-FF", "42-607"}
	expectedPayload := `{"registration_ids":["CAFE1-FF","42-607"],"collapse_key":"somegroup","time_to_live":5,"data":{"other":"value","other.foo":"bar"}}`
	testToGCMPayload(t, postData, regIds, expectedPayload)
}

// Test that it will be encoded properly if uniqush.payload.gcm is provided instead of uniqush.payload
func TestToGCMPayloadWithBlob(t *testing.T) {
	postData := map[string]string{
		"msggroup":            "somegroupnotif",
		"uniqush.payload.gcm": `{"message":{"aPushType":{"foo":"bar","other":"value"},"gcm":{},"others":{"type":"aPushType"}}}`,
	}
	regIds := []string{"CAFE1-FF", "42-607"}
	expectedPayload := `{"registration_ids":["CAFE1-FF","42-607"],"collapse_key":"somegroupnotif","time_to_live":3600,"data":{"message":{"aPushType":{"foo":"bar","other":"value"},"gcm":{},"others":{"type":"aPushType"}}}}`
	testToGCMPayload(t, postData, regIds, expectedPayload)
}

// Test that the push type isn't used as a fallback collapse key or anything else.
func TestToGCMPayloadUsesMsggroupForCollapseKey(t *testing.T) {
	postData := map[string]string{
		"uniqush.payload.gcm": `{"message":{"aPushType":{"foo":"bar","other":"value"},"gcm":{},"others":{"type":"aPushType"}}}`,
		"msggroup":            "AMsgGroup",
	}
	regIds := []string{"CAFE1-FF", "42-607"}
	expectedPayload := `{"registration_ids":["CAFE1-FF","42-607"],"collapse_key":"AMsgGroup","time_to_live":3600,"data":{"message":{"aPushType":{"foo":"bar","other":"value"},"gcm":{},"others":{"type":"aPushType"}}}}`
	testToGCMPayload(t, postData, regIds, expectedPayload)
}

// Test that it will be encoded properly if uniqush.notification.gcm is provided
func TestToGCMNotificationWithBlob(t *testing.T) {
	postData := map[string]string{
		"msggroup":                 "somegroup",
		"uniqush.notification.gcm": `{"body":"text","icon":"myicon","title":"🔥Notification Title"}`,
	}
	regIds := []string{"CAFE1-FF", "11-213"}
	expectedPayload := `{"registration_ids":["CAFE1-FF","11-213"],"collapse_key":"somegroup","time_to_live":3600,"notification":{"body":"text","icon":"myicon","title":"🔥Notification Title"}}`
	testToGCMPayload(t, postData, regIds, expectedPayload)
}

// Test that it will be encoded properly if uniqush.notification.gcm and uniqush.notification.fcm are provided
func TestToGCMNotificationWithPayloadAndNotificationBlobs(t *testing.T) {
	postData := map[string]string{
		"msggroup":                 "bothgroup",
		"uniqush.notification.gcm": `{"body":"text","icon":"myicon","title":"mytitle"}`,
		"uniqush.payload.gcm":      `{"message":{"key": {},"x":"y"},"other":{}}`,
	}
	regIds := []string{"CAFE1-FF", "11-213"}
	expectedPayload := `{"registration_ids":["CAFE1-FF","11-213"],"collapse_key":"bothgroup","time_to_live":3600,"data":{"message":{"key":{},"x":"y"},"other":{}},"notification":{"body":"text","icon":"myicon","title":"mytitle"}}`
	testToGCMPayload(t, postData, regIds, expectedPayload)
}

// Test the return value of Name()
func TestGCMPushServiceName(t *testing.T) {
	stubPushService := newGCMPushService()
	defer stubPushService.Finalize()
	name := stubPushService.Name()
	if name != "gcm" {
		t.Errorf("Expected %s, got %s", "gcm", name)
	}
}
