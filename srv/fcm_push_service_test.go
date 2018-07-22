package srv

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"sync"
	"testing"

	"github.com/uniqush/uniqush-push/push"
	"github.com/uniqush/uniqush-push/testutil"
)

const (
	FCMMockAPIKey    = "mockapikey"
	FCMMockProjectID = "mockprojectid"
	FCMMockService   = "mockservice"
)

func commonFCMMocks(responseCode int, responseBody []byte, headers map[string]string, requestError error) (*push.PushServiceProvider, *mockCMHTTPClient, *fcmPushService, chan push.Error) {
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

	errChan := make(chan push.Error, 100)
	service.SetErrorReportChan(errChan)

	psp, err := psm.BuildPushServiceProviderFromMap(map[string]string{
		"pushservicetype": service.Name(),
		"service":         FCMMockService,
		"subscriber":      "mocksubscriber",
		"apikey":          FCMMockAPIKey,
		"projectid":       FCMMockProjectID,
	})

	if psp == nil {
		panic(fmt.Errorf("psp should not be nil"))
	}
	if err != nil {
		panic(err)
	}
	return psp, client, service, errChan
}

func fcmAsyncCreateDPQueue(wg *sync.WaitGroup, dpQueue chan<- *push.DeliveryPoint, regID, subscriber string) {
	psm := push.GetPushServiceManager()
	mockDeliveryPoint, err := psm.BuildDeliveryPointFromMap(map[string]string{
		"regid":           regID,
		"subscriber":      subscriber,
		"pushservicetype": "fcm",
		"service":         FCMMockService,
	})
	if err != nil {
		panic(err)
	}
	dpQueue <- mockDeliveryPoint
	close(dpQueue)
	wg.Done()
}

func fcmAsyncPush(wg *sync.WaitGroup, service *fcmPushService, psp *push.PushServiceProvider, dpQueue <-chan *push.DeliveryPoint, resQueue chan<- *push.Result, notif *push.Notification) {
	service.Push(psp, dpQueue, resQueue, notif)
	wg.Done()
}

// TestFCMPushSingle tests the ability to send a single push without error, and shut down cleanly.
func TestFCMPushSingle(t *testing.T) {
	expectedRegID := "mockregid"
	notif := push.NewEmptyNotification()
	expectedPayload := `{"message":{"aPushType":{"foo":"bar","other":"value"},"fcm":{},"others":{"type":"aPushType"}}}`
	notif.Data = map[string]string{
		"uniqush.payload.fcm": expectedPayload,
	}
	mockHTTPResponse := []byte(`{"multicast_id":777,"canonical_ids":1,"success":1,"failure":0,"results":[{"message_id":"UID12345"}]}`)
	psp, mockCMHTTPClient, service, errChan := commonFCMMocks(200, mockHTTPResponse, map[string]string{}, nil)
	dpQueue := make(chan *push.DeliveryPoint)
	resQueue := make(chan *push.Result)
	wg := new(sync.WaitGroup)
	wg.Add(2)
	go fcmAsyncCreateDPQueue(wg, dpQueue, expectedRegID, "unusedsubscriber1")
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
		resCount++
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

	assertExpectedFCMRequest(t, fcmMockResponse.request, expectedRegID, expectedPayload)
}

// TestFCMPushSingleError tests the ability to send a single push with an error error, and shut down cleanly.
func TestFCMPushSingleError(t *testing.T) {
	expectedRegID := "mockregid"
	notif := push.NewEmptyNotification()
	expectedPayload := `{"message":{"aPushType":{"foo":"bar","other":"value"},"fcm":{},"others":{"type":"aPushType"}}}`
	notif.Data = map[string]string{
		"uniqush.payload.fcm": expectedPayload,
	}
	mockHTTPResponse := []byte(`HTTP/401 error mock response`)
	psp, mockCMHTTPClient, service, errChan := commonFCMMocks(401, mockHTTPResponse, map[string]string{}, nil)
	dpQueue := make(chan *push.DeliveryPoint)
	resQueue := make(chan *push.Result)
	wg := new(sync.WaitGroup)
	wg.Add(2)
	go fcmAsyncCreateDPQueue(wg, dpQueue, expectedRegID, "unusedsubscriber1")
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
		resCount++
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

	assertExpectedFCMRequest(t, fcmMockResponse.request, expectedRegID, expectedPayload)
}

func assertExpectedFCMRequest(t *testing.T, request *http.Request, expectedRegID, expectedPayload string) {
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
	expectedAuth := "key=" + FCMMockAPIKey
	if actualAuth != expectedAuth {
		t.Errorf("Expected auth %q, got %q", expectedAuth, actualAuth)
	}

	actualBodyBytes, err := ioutil.ReadAll(request.Body)
	if err != nil {
		t.Fatalf("Unexpected error reading body: %v", err)
	}
	expectedBody := fmt.Sprintf(`{"registration_ids":[%q],"data":%s,"time_to_live":3600}`, expectedRegID, expectedPayload)
	testutil.ExpectJSONIsEquivalent(t, []byte(expectedBody), actualBodyBytes)
}

// Overlaps with TestToFCMPayload, since Preview just calls toFCMPayload.
func TestFCMPreviewWithCommonParameters(t *testing.T) {
	postData := map[string]string{
		"msggroup":  "somegroup",
		"other":     "value",
		"other.foo": "bar",
		"ttl":       "5",
		// FCM module should ignore anything it doesn't recognize beginning with "uniqush.", those are reserved.
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
	testutil.ExpectJSONIsEquivalent(t, []byte(expectedPayload), payload)
}
