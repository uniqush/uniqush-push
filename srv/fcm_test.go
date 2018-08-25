package srv

import (
	"testing"
)
import (
	"github.com/uniqush/uniqush-push/push"
)

func testToFCMPayload(t *testing.T, postData map[string]string, regIds []string, expectedPayload string) {
	notif := push.NewEmptyNotification()
	notif.Data = postData
	// Create a push service, just for the sake of realistically testing building payloads
	stubPushService := newFCMPushService()
	defer stubPushService.Finalize()
	payload, err := stubPushService.ToCMPayload(notif, regIds)
	if err != nil {
		t.Fatalf("Encountered error %v\n", err)
	}
	if string(payload) != expectedPayload {
		t.Errorf("Expected %s, got %s", expectedPayload, string(payload))
	}
}

func TestToFCMPayloadWithRawPayload(t *testing.T) {
	postData := map[string]string{
		"msggroup":            "somegroup",
		"uniqush.payload.fcm": `{"message":{"key": {},"x":"y"},"other":{}}`,
		"foo":                 "bar",
	}
	regIds := []string{"CAFE1-FF", "42-607"}
	expectedPayload := `{"registration_ids":["CAFE1-FF","42-607"],"collapse_key":"somegroup","time_to_live":3600,"data":{"message":{"key":{},"x":"y"},"other":{}}}`
	testToFCMPayload(t, postData, regIds, expectedPayload)
}

func TestToFCMPayloadWithRawEmptyPayload(t *testing.T) {
	postData := map[string]string{
		"msggroup":            "somegroup",
		"uniqush.payload.fcm": `{}`,
	}
	regIds := []string{"CAFE1-FF", "42-607"}
	expectedPayload := `{"registration_ids":["CAFE1-FF","42-607"],"collapse_key":"somegroup","time_to_live":3600,"data":{}}`
	testToFCMPayload(t, postData, regIds, expectedPayload)
}

func TestToFCMPayloadWithRawUnescapedPayload(t *testing.T) {
	postData := map[string]string{
		"msggroup":            "somegroup",
		"uniqush.payload.fcm": `{"message":{"key": {},"x":"<a☃?>\"'"},"other":{}}`,
		"foo":                 "bar",
	}
	regIds := []string{"CAFE1-FF", "42-607"}
	expectedPayload := `{"registration_ids":["CAFE1-FF","42-607"],"collapse_key":"somegroup","time_to_live":3600,"data":{"message":{"key":{},"x":"<a☃?>\"'"},"other":{}}}`
	testToFCMPayload(t, postData, regIds, expectedPayload)
}

func TestToFCMPayloadWithCommonParameters(t *testing.T) {
	postData := map[string]string{
		"msggroup":  "somegroup",
		"other":     "value",
		"other.foo": "bar",
		"ttl":       "5",
		// FCM module should ignore anything it doesn't recognize beginning with "uniqush.", those are reserved.
		"uniqush.payload.apns": "{}",
		"uniqush.foo":          "foo",
	}
	regIds := []string{"CAFE1-FF", "42-607"}
	expectedPayload := `{"registration_ids":["CAFE1-FF","42-607"],"collapse_key":"somegroup","time_to_live":5,"data":{"other":"value","other.foo":"bar"}}`
	testToFCMPayload(t, postData, regIds, expectedPayload)
}

// Test that it will be encoded properly if uniqush.payload.fcm is provided instead of uniqush.payload
func TestToFCMPayloadWithBlob(t *testing.T) {
	postData := map[string]string{
		"msggroup":            "somegroupnotif",
		"uniqush.payload.fcm": `{"message":{"aPushType":{"foo":"bar","other":"value"},"fcm":{},"others":{"type":"aPushType"}}}`,
	}
	regIds := []string{"CAFE1-FF", "42-607"}
	expectedPayload := `{"registration_ids":["CAFE1-FF","42-607"],"collapse_key":"somegroupnotif","time_to_live":3600,"data":{"message":{"aPushType":{"foo":"bar","other":"value"},"fcm":{},"others":{"type":"aPushType"}}}}`
	testToFCMPayload(t, postData, regIds, expectedPayload)
}

// Test that it will be encoded properly if uniqush.payload.fcm is provided instead of uniqush.payload
func TestToFCMPayloadNewWay(t *testing.T) {
	postData := map[string]string{
		"msggroup":            "somegroup",
		"uniqush.payload.fcm": `{"message":{"aPushType":{"foo":"bar","other":"value"},"fcm":{},"others":{"type":"aPushType"}}}`,
	}
	regIds := []string{"CAFE1-FF", "42-607"}
	expectedPayload := `{"registration_ids":["CAFE1-FF","42-607"],"collapse_key":"somegroup","time_to_live":3600,"data":{"message":{"aPushType":{"foo":"bar","other":"value"},"fcm":{},"others":{"type":"aPushType"}}}}`
	testToFCMPayload(t, postData, regIds, expectedPayload)
}

// Test that it will be encoded properly if uniqush.notification.fcm is provided
func TestToFCMPayloadUsesMsggroupForCollapseKey(t *testing.T) {
	postData := map[string]string{
		"msggroup":                 "somegroup",
		"uniqush.notification.fcm": `{"body":"text","icon":"myicon","title":"🔥Notification Title"}`,
	}
	regIds := []string{"CAFE1-FF", "11-213"}
	expectedPayload := `{"registration_ids":["CAFE1-FF","11-213"],"collapse_key":"somegroup","time_to_live":3600,"notification":{"body":"text","icon":"myicon","title":"🔥Notification Title"}}`
	testToFCMPayload(t, postData, regIds, expectedPayload)
}

// Test that it will be encoded properly if uniqush.notification.fcm and uniqush.notification.fcm are provided
func TestToFCMNotificationWithPayloadAndNotificationBlobs(t *testing.T) {
	postData := map[string]string{
		"msggroup":                 "bothgroup",
		"uniqush.notification.fcm": `{"body":"text","icon":"myicon","title":"mytitle"}`,
		"uniqush.payload.fcm":      `{"message":{"key": {},"x":"y"},"other":{}}`,
	}
	regIds := []string{"CAFE1-FF", "11-213"}
	expectedPayload := `{"registration_ids":["CAFE1-FF","11-213"],"collapse_key":"bothgroup","time_to_live":3600,"data":{"message":{"key":{},"x":"y"},"other":{}},"notification":{"body":"text","icon":"myicon","title":"mytitle"}}`
	testToFCMPayload(t, postData, regIds, expectedPayload)
}

// Test the return value of Name()
func TestFCMPushServiceName(t *testing.T) {
	stubPushService := newFCMPushService()
	defer stubPushService.Finalize()
	name := stubPushService.Name()
	if name != "fcm" {
		t.Errorf("Expected %s, got %s", "fcm", name)
	}
}
