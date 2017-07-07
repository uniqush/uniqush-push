package srv

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"reflect"
	"sync"
	"testing"

	"github.com/uniqush/uniqush-push/push"
)

const (
	FCM_MOCK_API_KEY    = "mockapikey"
	FCM_MOCK_PROJECT_ID = "mockprojectid"
	FCM_MOCK_SERVICE    = "mockservice"
)

func commonFCMMocks(responseCode int, responseBody []byte, headers map[string]string, requestError error) (*push.PushServiceProvider, *mockCMHTTPClient, *fcmPushService, chan push.PushError) {
	client := &mockCMHTTPClient{
		status:       responseCode,
		responseBody: responseBody,
		headers:      headers,
		requestError: requestError,
	}
	service := newFCMPushService()
	service.OverrideClient(client)

	// Overwrite the APNS service.
	psm := push.GetPushServiceManager()
	psm.RegisterPushServiceType(service)

	errChan := make(chan push.PushError, 100)
	service.SetErrorReportChan(errChan)

	psp, err := psm.BuildPushServiceProviderFromMap(map[string]string{
		"pushservicetype": service.Name(),
		"service":         FCM_MOCK_SERVICE,
		"subscriber":      "mocksubscriber",
		"apikey":          FCM_MOCK_API_KEY,
		"projectid":       FCM_MOCK_PROJECT_ID,
	})

	if psp == nil {
		panic(fmt.Errorf("psp should not be nil"))
	}
	if err != nil {
		panic(err)
	}
	return psp, client, service, errChan
}

func fcmAsyncCreateDPQueue(wg *sync.WaitGroup, dpQueue chan<- *push.DeliveryPoint, regId, subscriber string) {
	psm := push.GetPushServiceManager()
	mockDeliveryPoint, err := psm.BuildDeliveryPointFromMap(map[string]string{
		"regid":           regId,
		"subscriber":      subscriber,
		"pushservicetype": "fcm",
		"service":         FCM_MOCK_SERVICE,
	})
	if err != nil {
		panic(err)
	}
	dpQueue <- mockDeliveryPoint
	close(dpQueue)
	wg.Done()
}

func fcmAsyncPush(wg *sync.WaitGroup, service *fcmPushService, psp *push.PushServiceProvider, dpQueue <-chan *push.DeliveryPoint, resQueue chan<- *push.PushResult, notif *push.Notification) {
	service.Push(psp, dpQueue, resQueue, notif)
	wg.Done()
}

// fcmTestPushSingle tests the ability to send a single push without error, and shut down cleanly.
func fcmTestPushSingle(t *testing.T) {
	expectedRegId := "mockregid"
	notif := push.NewEmptyNotification()
	expectedPayload := `{"message":{"aPushType":{"foo":"bar","other":"value"},"fcm":{},"others":{"type":"aPushType"}}}`
	notif.Data = map[string]string{
		"uniqush.payload.fcm": expectedPayload,
	}
	mockHTTPResponse := []byte(`{"multicast_id":777,"canonical_ids":1,"success":1,"failure":0,"results":[{"message_id":"UID12345"}]}`)
	psp, mockCMHTTPClient, service, errChan := commonFCMMocks(200, mockHTTPResponse, map[string]string{}, nil)
	dpQueue := make(chan *push.DeliveryPoint)
	resQueue := make(chan *push.PushResult)
	wg := new(sync.WaitGroup)
	wg.Add(2)
	go fcmAsyncCreateDPQueue(wg, dpQueue, expectedRegId, "unusedsubscriber1")
	go fcmAsyncPush(wg, service, psp, dpQueue, resQueue, notif)
	resCount := 0
	for res := range resQueue {
		if res == nil {
			t.Fatal("Unexpected result - closed")
		}
		if res.Err != nil {
			t.Fatalf("Encountered error %v\n", res.Err)
		}
		if notif != res.Content {
			t.Errorf("Expected %#v, got %#v\n", notif, res.Content)
		}
		resCount += 1
	}
	if resCount != 1 {
		t.Errorf("Unexpected number of results: want 1, got %d", resCount)
	}
	// Wait for push to complete and
	wg.Wait()
	service.Finalize()
	if numErrs := len(errChan); numErrs > 0 {
		t.Errorf("Unexpected number of errors: want none, got %d", numErrs)
	}
	if len(mockCMHTTPClient.performed) != 1 {
		t.Errorf("Unexpected number of http calls: want 1, got %#v", mockCMHTTPClient.performed)
	}
	fcmMockResponse := mockCMHTTPClient.performed[0]
	if !fcmMockResponse.closed {
		t.Error("Expected the mock response body to be closed")
	}

	assertExpectedFCMRequest(t, fcmMockResponse.request, expectedRegId, expectedPayload)
}

// fcmTestPushSingleError tests the ability to send a single push with an error error, and shut down cleanly.
func fcmTestPushSingleError(t *testing.T) {
	expectedRegId := "mockregid"
	notif := push.NewEmptyNotification()
	expectedPayload := `{"message":{"aPushType":{"foo":"bar","other":"value"},"fcm":{},"others":{"type":"aPushType"}}}`
	notif.Data = map[string]string{
		"uniqush.payload.fcm": expectedPayload,
	}
	mockHTTPResponse := []byte(`HTTP/401 error mock response`)
	psp, mockCMHTTPClient, service, errChan := commonFCMMocks(401, mockHTTPResponse, map[string]string{}, nil)
	dpQueue := make(chan *push.DeliveryPoint)
	resQueue := make(chan *push.PushResult)
	wg := new(sync.WaitGroup)
	wg.Add(2)
	go fcmAsyncCreateDPQueue(wg, dpQueue, expectedRegId, "unusedsubscriber1")
	go fcmAsyncPush(wg, service, psp, dpQueue, resQueue, notif)
	resCount := 0
	for res := range resQueue {
		if res.Err == nil {
			t.Fatal("Expected error but lacked one")
		}
		err, ok := res.Err.(*push.BadPushServiceProvider)
		if !ok {
			t.Fatalf("Expected type BadPushServiceProvider, got %T", err)
		}
		if res.Provider != psp {
			t.Errorf("Unexpected psp %v in BadPushServiceProvider result", err.Provider)
		}
		if res.Content != notif {
			t.Errorf("Unexpected content %v in BadPushServiceProvider", res.Content)
		}
		resCount += 1
	}
	if resCount != 1 {
		t.Errorf("Unexpected number of results: want 1, got %d", resCount)
	}
	// Wait for push to complete and
	wg.Wait()
	service.Finalize()
	if numErrs := len(errChan); numErrs > 0 {
		t.Errorf("Unexpected number of errors: want none, got %d", numErrs)
	}
	if len(mockCMHTTPClient.performed) != 1 {
		t.Errorf("Unexpected number of http calls: want 1, got %#v", mockCMHTTPClient.performed)
	}
	fcmMockResponse := mockCMHTTPClient.performed[0]
	if !fcmMockResponse.closed {
		t.Error("Expected the mock response body to be closed")
	}

	assertExpectedFCMRequest(t, fcmMockResponse.request, expectedRegId, expectedPayload)
}

// Helper function, because golang json serialization has an unpredictable order.
// Uses reflect.DeepEqual.
func fcmExpectJSONIsEquivalent(t *testing.T, expected []byte, actual []byte) {
	var expectedObj map[string]interface{}
	var actualObj map[string]interface{}
	if err := json.Unmarshal(expected, &expectedObj); err != nil {
		t.Fatalf("Invalid test expectation of JSON %s: %v", string(expected), err.Error())
	}
	if err := json.Unmarshal(actual, &actualObj); err != nil {
		t.Fatalf("Invalid JSON %s: %v", string(actual), err.Error())
	}
	if !reflect.DeepEqual(actualObj, expectedObj) {
		t.Errorf("%s is not equivalent to %s", actual, expected)
	}
}

func assertExpectedFCMRequest(t *testing.T, request *http.Request, expectedRegId, expectedPayload string) {
	actualURL := request.URL.String()
	if actualURL != fcmServiceURL {
		t.Errorf("Expected URL %q, got %q", fcmServiceURL, actualURL)
	}
	actualContentType := request.Header.Get("Content-Type")
	expectedContentType := "application/json"
	if actualContentType != expectedContentType {
		t.Errorf("Expected URL %q, got %q", expectedContentType, actualContentType)
	}

	actualAuth := request.Header.Get("Authorization")
	expectedAuth := "key=" + FCM_MOCK_API_KEY
	if actualAuth != expectedAuth {
		t.Errorf("Expected auth %q, got %q", expectedAuth, actualAuth)
	}

	actualBodyBytes, err := ioutil.ReadAll(request.Body)
	if err != nil {
		t.Fatalf("Unexpected error reading body: %v", err)
	}
	expectedBody := fmt.Sprintf(`{"registration_ids":[%q],"data":%s,"time_to_live":3600}`, expectedRegId, expectedPayload)
	fcmExpectJSONIsEquivalent(t, []byte(expectedBody), actualBodyBytes)
}

// Overlaps with TestToFCMPayload, since Preview just calls toFCMPayload.
func FcmTestPreviewWithCommonParameters(t *testing.T) {
	postData := map[string]string{
		"msggroup":  "somegroup",
		"other":     "value",
		"other.foo": "bar",
		"ttl":       "5",
		// FCM module should ignore anything it doesn't recognize begining with "uniqush.", those are reserved.
		"uniqush.payload.apns": "{}",
		"uniqush.foo":          "foo",
	}
	expectedPayload := `{"registration_ids":["placeholderRegId"],"collapse_key":"somegroup","data":{"other":"value","other.foo":"bar"},"time_to_live":5}`

	notif := push.NewEmptyNotification()
	notif.Data = postData

	_, _, service, _ := commonFCMMocks(200, []byte("unused"), map[string]string{}, nil)
	defer service.Finalize()

	payload, err := service.Preview(notif)
	if err != nil {
		t.Fatalf("Encountered error %v\n", err)
	}
	if string(payload) != expectedPayload {
		t.Errorf("Expected %s, got %s", expectedPayload, string(payload))
	}
}
