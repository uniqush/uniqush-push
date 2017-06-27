package http_api

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"testing"

	"github.com/uniqush/uniqush-push/push"
	"github.com/uniqush/uniqush-push/srv/apns/common"
)

const (
	authToken  = "test_auth_token"
	authToken2 = "update_auth_token"
	keyID      = "FD8789SD9"
	teamID     = "JVNS20943"
	bundleID   = "com.example.test"
)

var (
	pushServiceProvider = initPSP()
	devToken            = []byte("test_device_token")
	payload             = []byte(`{"alert":"test_message"}`)
	apiURL              = fmt.Sprintf("%s/3/device/%s", pushServiceProvider.VolatileData["addr"], hex.EncodeToString(devToken))
	mockServiceName     = "myService"
)

// TODO: refactor into a common test library.
type mockAPNSServiceType struct{}

var _ push.PushServiceType = &mockAPNSServiceType{}

func (self *mockAPNSServiceType) BuildPushServiceProviderFromMap(kv map[string]string, psp *push.PushServiceProvider) error {
	for key, value := range kv {
		switch key {
		case "addr", "bundleid", "skipverify":
			psp.VolatileData[key] = value
		case "service", "pushservicetype", "cert", "subscriber", "key":
			psp.FixedData[key] = value
		}
	}
	return nil
}
func (self *mockAPNSServiceType) BuildDeliveryPointFromMap(map[string]string, *push.DeliveryPoint) error {
	panic("Not implemented")
}
func (self *mockAPNSServiceType) Name() string {
	return "apns"
}
func (self *mockAPNSServiceType) Push(*push.PushServiceProvider, <-chan *push.DeliveryPoint, chan<- *push.PushResult, *push.Notification) {
	panic("Not implemented")
}
func (self *mockAPNSServiceType) Preview(*push.Notification) ([]byte, push.PushError) {
	panic("Not implemented")
}
func (self *mockAPNSServiceType) SetErrorReportChan(errChan chan<- push.PushError) {
	panic("Not implemented")
}
func (self *mockAPNSServiceType) Finalize() {}

func initPSP() *push.PushServiceProvider {
	psm := push.GetPushServiceManager()
	psm.RegisterPushServiceType(&mockAPNSServiceType{})
	psp, err := psm.BuildPushServiceProviderFromMap(map[string]string{
		"service":         mockServiceName,
		"pushservicetype": "apns",
		"cert":            "../apns-test/localhost.cert",
		"subscriber":      "mocksubscriber",
		"key":             "../apns-test/localhost.key",
		"addr":            "gateway.push.apple.com:2195",
		"skipverify":      "true",
		"bundleid":        bundleID,
	})
	if err != nil {
		panic(err)
	}
	return psp
}

// TODO: remove unrelated fields
type mockHTTP2Client struct {
	processorFn func(r *http.Request) (*http.Response, *mockResponse, error)
	// Mocks for responses given json encoded request, TODO write expectations.
	// mockResponses map[string]string
	performed []*mockResponse
}

func (c *mockHTTP2Client) Do(request *http.Request) (*http.Response, error) {
	result, mockResponse, err := c.processorFn(request)
	c.performed = append(c.performed, mockResponse)
	return result, err
}

var _ HTTPClient = &mockHTTP2Client{}

type mockResponse struct {
	impl    *bytes.Reader
	closed  bool
	request *http.Request
}

func (r *mockResponse) Read(p []byte) (n int, err error) {
	return r.impl.Read(p)
}

func (r *mockResponse) Close() error {
	r.closed = true
	return nil
}

var _ io.ReadCloser = &mockResponse{}

func newMockResponse(contents []byte, request *http.Request) *mockResponse {
	return &mockResponse{
		impl:    bytes.NewReader(contents),
		closed:  false,
		request: request,
	}
}

func mockAPNSRequest(requestProcessor *HTTPPushRequestProcessor, fn func(r *http.Request) (*http.Response, *mockResponse, error)) {
	requestProcessor.clientFactory = func(_ *http.Transport) HTTPClient {
		return &mockHTTP2Client{
			processorFn: fn,
		}
	}
}

func newPushRequest() (*common.PushRequest, chan push.PushError, chan *common.APNSResult) {
	errChan := make(chan push.PushError)
	resChan := make(chan *common.APNSResult, 1)
	request := &common.PushRequest{
		PSP:       pushServiceProvider,
		Devtokens: [][]byte{devToken},
		Payload:   payload,
		ErrChan:   errChan,
		ResChan:   resChan,
	}

	return request, errChan, resChan
}

func newHTTPRequestProcessor() *HTTPPushRequestProcessor {
	res := NewRequestProcessor().(*HTTPPushRequestProcessor)
	// Force tests to override this or crash.
	res.clientFactory = nil
	return res
}

func TestAddRequestPushSuccessful(t *testing.T) {
	requestProcessor := newHTTPRequestProcessor()

	request, errChan, resChan := newPushRequest()
	mockAPNSRequest(requestProcessor, func(r *http.Request) (*http.Response, *mockResponse, error) {
		if auth, ok := r.Header["authorization"]; ok {
			// temporarily disabled
			t.Errorf("Unexpected authorization header %v", auth)
		}
		if len(r.Header["apns-expiration"]) == 0 {
			t.Error("Missing apns-expiration header")
		}
		if len(r.Header["apns-priority"]) == 0 {
			t.Error("Missing apns-priority header")
		}
		if len(r.Header["apns-topic"]) == 0 {
			t.Error("Missing apns-topic header")
		}
		requestBody, err := ioutil.ReadAll(r.Body)
		if err != nil {
			t.Error("Error reading request body:", err)
		}
		if bytes.Compare(requestBody, payload) != 0 {
			t.Errorf("Wrong message payload, expected `%v`, got `%v`", payload, requestBody)
		}
		// Return empty body
		body := newMockResponse([]byte{}, r)
		response := &http.Response{
			StatusCode: http.StatusOK,
			Body:       body,
		}
		return response, body, nil
	})

	requestProcessor.AddRequest(request)

	select {
	case res := <-resChan:
		if res.MsgId == 0 {
			t.Fatal("Expected non-zero message id, got zero")
		}
	case err := <-errChan:
		t.Fatalf("Response was unexpectedly an error: %v\n", err)
	}
}

func TestAddRequestPushFailConnectionError(t *testing.T) {
	requestProcessor := newHTTPRequestProcessor()

	request, errChan, _ := newPushRequest()
	mockAPNSRequest(requestProcessor, func(r *http.Request) (*http.Response, *mockResponse, error) {
		return nil, nil, fmt.Errorf("No connection")
	})

	requestProcessor.AddRequest(request)

	err := <-errChan
	if _, ok := err.(*push.ConnectionError); !ok {
		t.Fatal("Expected Connection error, got", err)
	}
}

func newMockJSONResponse(r *http.Request, status int, responseData *APNSErrorResponse) (*http.Response, *mockResponse, error) {
	responseBytes, err := json.Marshal(responseData)
	if err != nil {
		panic(fmt.Sprintf("newMockJSONResponse failed: %v", err))
	}
	body := newMockResponse(responseBytes, r)
	response := &http.Response{
		StatusCode: status,
		Body:       body,
	}
	return response, body, nil
}

func TestAddRequestPushFailNotificationError(t *testing.T) {
	requestProcessor := newHTTPRequestProcessor()

	request, errChan, _ := newPushRequest()
	mockAPNSRequest(requestProcessor, func(r *http.Request) (*http.Response, *mockResponse, error) {
		response := &APNSErrorResponse{
			Reason: "BadDeviceToken",
		}
		return newMockJSONResponse(r, http.StatusBadRequest, response)
	})

	requestProcessor.AddRequest(request)

	err := <-errChan
	if _, ok := err.(*push.BadNotification); !ok {
		t.Fatal("Expected BadNotification error, got", err)
	}
}

func TestGetMaxPayloadSize(t *testing.T) {
	maxPayloadSize := NewRequestProcessor().GetMaxPayloadSize()
	if maxPayloadSize != 4096 {
		t.Fatalf("Wrong max payload, expected `4096`, got `%d`", maxPayloadSize)
	}
}
