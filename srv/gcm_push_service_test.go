package srv

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"reflect"
	"sync"
	"testing"

	"github.com/uniqush/uniqush-push/push"
)

const (
	MOCK_API_KEY    = "mockapikey"
	MOCK_PROJECT_ID = "mockprojectid"
	MOCK_SERVICE    = "mockservice"
)

type mockHTTPClient struct {
	status       int
	responseBody []byte
	headers      map[string]string
	requestError error
	// Mocks for responses given json encoded request, TODO write expectations.
	// mockResponses map[string]string
	performed []*mockResponse
}

type mockResponse struct {
	impl    *bytes.Reader
	closed  bool
	request *http.Request
}

var _ io.ReadCloser = &mockResponse{}

func newMockResponse(contents []byte, request *http.Request) *mockResponse {
	return &mockResponse{
		impl:    bytes.NewReader(contents),
		closed:  false,
		request: request,
	}
}

func (r *mockResponse) Read(p []byte) (n int, err error) {
	return r.impl.Read(p)
}

func (r *mockResponse) Close() error {
	r.closed = true
	return nil
}

func (c *mockHTTPClient) Do(request *http.Request) (*http.Response, error) {
	h := http.Header{}
	for k, v := range c.headers {
		h.Set(k, v)
	}
	body := newMockResponse(c.responseBody, request)
	result := &http.Response{
		StatusCode: c.status,
		Body:       body,
		Header:     h,
	}
	c.performed = append(c.performed, body)
	return result, c.requestError
}

var _ HTTPClient = &mockHTTPClient{}

func commonGCMMocks(responseCode int, responseBody []byte, headers map[string]string, requestError error) (*push.PushServiceProvider, *mockHTTPClient, *gcmPushService, chan push.PushError) {
	client := &mockHTTPClient{
		status:       responseCode,
		responseBody: responseBody,
		headers:      headers,
		requestError: requestError,
	}
	service := newGCMPushService()
	service.OverrideClient(client)

	// Overwrite the APNS service.
	psm := push.GetPushServiceManager()
	psm.RegisterPushServiceType(service)

	errChan := make(chan push.PushError, 100)
	service.SetErrorReportChan(errChan)

	psp, err := psm.BuildPushServiceProviderFromMap(map[string]string{
		"pushservicetype": service.Name(),
		"service":         MOCK_SERVICE,
		"subscriber":      "mocksubscriber",
		"apikey":          MOCK_API_KEY,
		"projectid":       MOCK_PROJECT_ID,
	})

	if psp == nil {
		panic(fmt.Errorf("psp should not be nil"))
	}
	if err != nil {
		panic(err)
	}
	return psp, client, service, errChan
}

func asyncCreateDPQueue(wg *sync.WaitGroup, dpQueue chan<- *push.DeliveryPoint, regId, subscriber string) {
	psm := push.GetPushServiceManager()
	mockDeliveryPoint, err := psm.BuildDeliveryPointFromMap(map[string]string{
		"regid":           regId,
		"subscriber":      subscriber,
		"pushservicetype": "gcm",
		"service":         MOCK_SERVICE,
	})
	if err != nil {
		panic(err)
	}
	dpQueue <- mockDeliveryPoint
	close(dpQueue)
	wg.Done()
}

func asyncPush(wg *sync.WaitGroup, service *gcmPushService, psp *push.PushServiceProvider, dpQueue <-chan *push.DeliveryPoint, resQueue chan<- *push.PushResult, notif *push.Notification) {
	service.Push(psp, dpQueue, resQueue, notif)
	wg.Done()
}

// TestPushSingle tests the ability to send a single push without error, and shut down cleanly.
func TestPushSingle(t *testing.T) {
	expectedRegId := "mockregid"
	notif := push.NewEmptyNotification()
	expectedPayload := `{"message":{"aPushType":{"foo":"bar","other":"value"},"gcm":{},"others":{"type":"aPushType"}}}`
	notif.Data = map[string]string{
		"uniqush.payload.gcm": expectedPayload,
	}
	mockHTTPResponse := []byte(`{"multicast_id":777,"canonical_ids":1,"success":1,"failure":0,"results":[{"message_id":"UID12345"}]}`)
	psp, mockHTTPClient, service, errChan := commonGCMMocks(200, mockHTTPResponse, map[string]string{}, nil)
	dpQueue := make(chan *push.DeliveryPoint)
	resQueue := make(chan *push.PushResult)
	wg := new(sync.WaitGroup)
	wg.Add(2)
	go asyncCreateDPQueue(wg, dpQueue, expectedRegId, "unusedsubscriber1")
	go asyncPush(wg, service, psp, dpQueue, resQueue, notif)
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
	if len(mockHTTPClient.performed) != 1 {
		t.Errorf("Unexpected number of http calls: want 1, got %#v", mockHTTPClient.performed)
	}
	mockResponse := mockHTTPClient.performed[0]
	if !mockResponse.closed {
		t.Error("Expected the mock response body to be closed")
	}

	assertExpectedGCMRequest(t, mockResponse.request, expectedRegId, expectedPayload)
}

// TestPushSingleError tests the ability to send a single push with an error error, and shut down cleanly.
func TestPushSingleError(t *testing.T) {
	expectedRegId := "mockregid"
	notif := push.NewEmptyNotification()
	expectedPayload := `{"message":{"aPushType":{"foo":"bar","other":"value"},"gcm":{},"others":{"type":"aPushType"}}}`
	notif.Data = map[string]string{
		"uniqush.payload.gcm": expectedPayload,
	}
	mockHTTPResponse := []byte(`HTTP/401 error mock response`)
	psp, mockHTTPClient, service, errChan := commonGCMMocks(401, mockHTTPResponse, map[string]string{}, nil)
	dpQueue := make(chan *push.DeliveryPoint)
	resQueue := make(chan *push.PushResult)
	wg := new(sync.WaitGroup)
	wg.Add(2)
	go asyncCreateDPQueue(wg, dpQueue, expectedRegId, "unusedsubscriber1")
	go asyncPush(wg, service, psp, dpQueue, resQueue, notif)
	resCount := 0
	for res := range resQueue {
		if res.Err == nil {
			t.Fatal("Expected error but lacked one")
		}
		err, ok := res.Err.(*push.BadPushServiceProvider)
		if !ok {
			t.Fatalf("Expected type BadPushServiceProvider, got %t", err)
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
	if len(mockHTTPClient.performed) != 1 {
		t.Errorf("Unexpected number of http calls: want 1, got %#v", mockHTTPClient.performed)
	}
	mockResponse := mockHTTPClient.performed[0]
	if !mockResponse.closed {
		t.Error("Expected the mock response body to be closed")
	}

	assertExpectedGCMRequest(t, mockResponse.request, expectedRegId, expectedPayload)
}

// Helper function, because golang json serialization has an unpredictable order.
// Uses reflect.DeepEqual.
func expectJSONIsEquivalent(t *testing.T, expected []byte, actual []byte) {
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

func assertExpectedGCMRequest(t *testing.T, request *http.Request, expectedRegId, expectedPayload string) {
	actualURL := request.URL.String()
	if actualURL != gcmServiceURL {
		t.Errorf("Expected URL %q, got %q", gcmServiceURL, actualURL)
	}
	actualContentType := request.Header.Get("Content-Type")
	expectedContentType := "application/json"
	if actualContentType != expectedContentType {
		t.Errorf("Expected URL %q, got %q", expectedContentType, actualContentType)
	}

	actualAuth := request.Header.Get("Authorization")
	expectedAuth := "key=" + MOCK_API_KEY
	if actualAuth != expectedAuth {
		t.Errorf("Expected auth %q, got %q", expectedAuth, actualAuth)
	}

	actualBodyBytes, err := ioutil.ReadAll(request.Body)
	if err != nil {
		t.Fatalf("Unexpected error reading body: %v", err)
	}
	expectedBody := fmt.Sprintf(`{"registration_ids":[%q],"data":%s,"time_to_live":3600}`, expectedRegId, expectedPayload)
	expectJSONIsEquivalent(t, []byte(expectedBody), actualBodyBytes)
}

// Overlaps with TestToGCMPayload, since Preview just calls toGCMPayload.
func TestPreviewWithCommonParameters(t *testing.T) {
	postData := map[string]string{
		"msggroup":  "somegroup",
		"other":     "value",
		"other.foo": "bar",
		"ttl":       "5",
		// GCM module should ignore anything it doesn't recognize begining with "uniqush.", those are reserved.
		"uniqush.payload.apns": "{}",
		"uniqush.foo":          "foo",
	}
	expectedPayload := `{"registration_ids":["placeholderRegId"],"collapse_key":"somegroup","data":{"other":"value","other.foo":"bar"},"time_to_live":5}`

	notif := push.NewEmptyNotification()
	notif.Data = postData

	_, _, service, _ := commonGCMMocks(200, []byte("unused"), map[string]string{}, nil)
	defer service.Finalize()

	payload, err := service.Preview(notif)
	if err != nil {
		t.Fatalf("Encountered error %v\n", err)
	}
	if string(payload) != expectedPayload {
		t.Errorf("Expected %s, got %s", expectedPayload, string(payload))
	}
}
