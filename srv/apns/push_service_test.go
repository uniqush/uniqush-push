package apns

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"sync"
	"testing"
)

import (
	"github.com/uniqush/uniqush-push/push"
	"github.com/uniqush/uniqush-push/srv/apns/common"
)

const APNS_SUCCESS uint8 = 0
const APNS_UNSUBSCRIBE uint8 = 8

type MockPushRequestProcessor struct {
	status      uint8
	didFinalize bool
	errChan     chan<- push.PushError
}

func newMockRequestProcessor(status uint8) *MockPushRequestProcessor {
	return &MockPushRequestProcessor{
		status:      status,
		didFinalize: false,
	}
}

var _ common.PushRequestProcessor = &MockPushRequestProcessor{}

func (self *MockPushRequestProcessor) AddRequest(request *common.PushRequest) {
	close(request.ErrChan) // Would have contents only for an invalid request. Send nothing.
	go func() {
		for i := range request.DPList {
			request.ResChan <- &common.APNSResult{
				MsgId:  request.GetId(i),
				Status: self.status,
				Err:    nil,
			}
		}
		// The real implementation doesn't close ResChan, either. That would require knowing which goroutine was the last.
	}()
}

func (self *MockPushRequestProcessor) GetMaxPayloadSize() int {
	return 2048
}

func (self *MockPushRequestProcessor) Finalize() {
	self.didFinalize = true
}

func (self *MockPushRequestProcessor) SetErrorReportChan(errChan chan<- push.PushError) {
	self.errChan = errChan
}

func TestCreatePushService(t *testing.T) {
	mockRequestProcessor := newMockRequestProcessor(APNS_SUCCESS)
	service := NewPushService()
	service.binaryRequestProcessor = mockRequestProcessor
	service.httpRequestProcessor = mockRequestProcessor
	service.Finalize()
}

func newPushServiceWithErrorChannel(status uint8) (*pushService, *MockPushRequestProcessor, chan push.PushError) {
	mockRequestProcessor := newMockRequestProcessor(status)
	service := NewPushService()
	service.binaryRequestProcessor = mockRequestProcessor
	service.httpRequestProcessor = mockRequestProcessor
	errChan := make(chan push.PushError, 100)
	service.SetErrorReportChan(errChan)
	return service, mockRequestProcessor, errChan
}

func commonAPNSMocks(status uint8) (*push.PushServiceProvider, *MockPushRequestProcessor, *pushService, chan push.PushError) {
	service, mockRequestProcessor, errChan := newPushServiceWithErrorChannel(status)

	// Overwrite the APNS service.
	psm := push.GetPushServiceManager()
	psm.RegisterPushServiceType(service)

	psp, err := psm.BuildPushServiceProviderFromMap(map[string]string{
		"pushservicetype": service.Name(),
		"service":         "mockservice",
		"cert":            "apns-test/localhost.cert",
		"subscriber":      "mocksubscriber",
		"key":             "apns-test/localhost.key",
	})

	if err != nil {
		panic(err)
	}
	return psp, mockRequestProcessor, service, errChan
}

func createNotification(expectedContentId int, pushType string, msg string) *push.Notification {
	return &push.Notification{
		Data: map[string]string{
			"msg": "hello world",
		},
	}
}

func asyncCreateDPQueue(wg *sync.WaitGroup, dpQueue chan<- *push.DeliveryPoint, devToken, subscriber string) {
	mockDeliveryPoint := push.NewEmptyDeliveryPoint()
	mockDeliveryPoint.FixedData["devtoken"] = devToken
	mockDeliveryPoint.FixedData["subscriber"] = subscriber
	dpQueue <- mockDeliveryPoint
	close(dpQueue)
	wg.Done()
}

func asyncPush(wg *sync.WaitGroup, service *pushService, psp *push.PushServiceProvider, dpQueue <-chan *push.DeliveryPoint, resQueue chan<- *push.PushResult, notif *push.Notification) {
	service.Push(psp, dpQueue, resQueue, notif)
	wg.Done()
}

// TestPushSingle tests the ability to send a single push without error, and shut down cleanly.
func TestPushSingle(t *testing.T) {
	expectedContentId := 2223511
	expectedToken := hex.EncodeToString([]byte("FakeDevToken"))

	psp, _, service, errChan := commonAPNSMocks(APNS_SUCCESS)

	resQueue := make(chan *push.PushResult)
	notif := createNotification(expectedContentId, "helloworld", "Hello World")

	wg := new(sync.WaitGroup)
	wg.Add(2)

	dpQueue := make(chan *push.DeliveryPoint)
	go asyncCreateDPQueue(wg, dpQueue, expectedToken, "unusedsubscriber1")
	go asyncPush(wg, service, psp, dpQueue, resQueue, notif)
	resCount := 0
	for res := range resQueue {
		resCount += 1
		// Might not be worth testing
		if res == nil {
			t.Fatal("Unexpected result - closed")
		}
		if res.Err != nil {
			t.Fatalf("Encountered error %v\n", res.Err)
		}
		if notif != res.Content {
			t.Errorf("Expected %#v, got %#v\n", notif, res.Content)
		}
	}
	if resCount != 1 {
		t.Errorf("Unexpected number of results: want 1, got %d\n", resCount)
	}
	wg.Wait()
	service.Finalize()
	if numErrs := len(errChan); numErrs > 0 {
		t.Errorf("Unexpected number of errors: want none, got %d\n", numErrs)
	}
}

func TestPushMultiple(t *testing.T) {
	pushes := 3
	expectedContentId := 2223511
	expectedToken := hex.EncodeToString([]byte("FakeDevToken"))

	psp, _, service, _ := commonAPNSMocks(APNS_SUCCESS)

	resQueues := make([]chan *push.PushResult, pushes)
	notifs := make([]*push.Notification, pushes)

	wg := new(sync.WaitGroup)
	wg.Add(pushes * 2)

	for i := 0; i < pushes; i++ {
		dpQueue := make(chan *push.DeliveryPoint)
		go asyncCreateDPQueue(wg, dpQueue, expectedToken, "unusedsubscriber2")
		notif := createNotification(expectedContentId, fmt.Sprintf("helloworld%d", i), fmt.Sprintf("Hello World%d", i))
		notifs[i] = notif
		resQueue := make(chan *push.PushResult)
		resQueues[i] = resQueue
		go asyncPush(wg, service, psp, dpQueue, resQueue, notif)
	}
	for i, resQueue := range resQueues {
		resCount := 0
		notif := notifs[i]
		for res := range resQueue {
			resCount += 1
			// Might not be worth testing
			if res == nil {
				t.Fatal("Unexpected result - closed")
			}
			if res.Err != nil {
				t.Fatalf("Encountered error %v\n", res.Err)
			}
			if notif != res.Content {
				t.Errorf("Expected %#v, got %#v\n", notif, res.Content)
			}
		}
		if resCount != 1 {
			t.Errorf("Unexpected number of results: got %d\n", resCount)
		}
	}
	wg.Wait()
	service.Finalize()
}

// TestPushUnsubscribe tests that an UnsubscribeUpdate should be generated from the corresponding apns status code.
func TestPushUnsubscribe(t *testing.T) {
	expectedContentId := 2223511
	expectedToken := hex.EncodeToString([]byte("FakeDevToken"))

	psp, _, service, errChan := commonAPNSMocks(APNS_UNSUBSCRIBE)

	resQueue := make(chan *push.PushResult)
	notif := createNotification(expectedContentId, "helloworld", "Hello World")

	wg := new(sync.WaitGroup)
	wg.Add(2)

	dpQueue := make(chan *push.DeliveryPoint)
	expectedSubscriber := "subscriber3"
	go asyncCreateDPQueue(wg, dpQueue, expectedToken, expectedSubscriber)
	go asyncPush(wg, service, psp, dpQueue, resQueue, notif)
	resCount := 0
	for res := range resQueue {
		resCount += 1
		// Might not be worth testing
		if res == nil {
			t.Fatal("Unexpected result - closed")
		}
		if res.Err != nil {
			t.Fatalf("Encountered error %v\n", res.Err)
		}
		if notif != res.Content {
			t.Errorf("Expected %#v, got %#v\n", notif, res.Content)
		}
	}
	if resCount != 1 {
		t.Errorf("Unexpected number of results: got %d\n", resCount)
	}
	wg.Wait()
	service.Finalize()

	// No simple way to shut down waitResult sending errors that I can think of right now
	err := <-errChan
	close(errChan)
	if len(errChan) != 0 {
		t.Errorf("Unexpectedly have %d more errors", len(errChan))
	}

	if unsubscribeErr, ok := err.(*push.UnsubscribeUpdate); ok {
		if service, ok := unsubscribeErr.Provider.FixedData["service"]; !ok || service != "mockservice" {
			t.Errorf("Wanted unsubscribe service %v, got %v", "mockservice", service)
		}
		if subscriber, ok := unsubscribeErr.Destination.FixedData["subscriber"]; !ok || subscriber != expectedSubscriber {
			t.Errorf("Wanted unsubscribe subscriber %v, got %v", expectedSubscriber, subscriber)
		}
	} else {
		t.Errorf("Unexpected error - not an Unsubscribe: %v\n", unsubscribeErr)
		return
	}
}

func TestValidateRawAPNSPayload(t *testing.T) {
	json := `{"aps":{"alert":"an alert message"}, "type": "foo", "foo": {}}`
	payload, err := validateRawAPNSPayload(json)
	if err != nil {
		t.Fatalf("Error decoding payload: %v", err)
	}
	if string(payload) != json {
		t.Errorf("Unexpected payload: want %s, got %s", json, string(payload))
	}
}

func TestValidateSilentPayload(t *testing.T) {
	json := `{"aps":{"content-available":"1"},"type":"foo","foo": {}}`
	payload, err := validateRawAPNSPayload(json)
	if err != nil {
		t.Fatalf("Error decoding payload: %v", err)
	}
	if string(payload) != json {
		t.Errorf("Unexpected payload: want %s, got %s", json, string(payload))
	}
}

func TestRejectInvalidAPNSPayload(t *testing.T) {
	invalidPayloads := []string{
		`{"aps":42, "type": "foo", "foo": {}}`,
		`{"aps":null, "type": "foo", "foo": {}}`,
		`{"aps":{}, "type": "foo", "foo": {}}`, // no alert
		`not JSON`,
	}
	for _, json := range invalidPayloads {
		_, err := validateRawAPNSPayload(json)
		if err == nil {
			t.Errorf("Expected error for payload %s", json)
		}
	}
}

func TestToAPNSPayloadWithKey(t *testing.T) {
	json := `{"aps":{"alert":"an alert message"}}`
	notification := &push.Notification{
		Data: map[string]string{"uniqush.payload.apns": json},
	}
	payload, err := toAPNSPayload(notification)
	if err != nil {
		t.Fatalf("Got error decoding payload: %v", err)
	}
	if string(payload) != json {
		t.Errorf("Unexpected payload contents: want %s, got %s", json, string(payload))
	}
}

func TestToAPNSPayload(t *testing.T) {
	expectedJson := `{"aps":{"alert":{"body":"hello world <&>"}}}`
	notification := &push.Notification{
		Data: map[string]string{"msg": "hello world <&>"},
	}
	payload, err := toAPNSPayload(notification)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Equal([]byte(expectedJson), payload) {
		t.Errorf("Expected %v(%s), Got %v(%s)", []byte(expectedJson), expectedJson, payload, string(payload))
	}
}

func TestToAPNSPayloadAllParams(t *testing.T) {
	expectedJSON := `{"aps":{"alert":{"action-loc-key":"foo","body":"hello world","launch-image":"Default2.png","loc-args":["one","two"],"loc-key":"bar"},"badge":777,"content-available":1,"sound":"hi.wav"},"myKey":"myValue"}`
	notification := &push.Notification{
		Data: map[string]string{
			"msg":               "hello world",
			"action-loc-key":    "foo",
			"loc-key":           "bar",
			"loc-args":          "one,two",
			"badge":             "777",
			"sound":             "hi.wav",
			"content-available": "1",
			"img":               "Default2.png",
			"id":                "unused",
			"expiry":            "unused",
			"ttl":               "42",
			"myKey":             "myValue",
			// Keys beginning with "uniqush." are reserved by uniqush.
			"uniqush.foo": "ignored",
		},
	}
	payload, err := toAPNSPayload(notification)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Equal([]byte(expectedJSON), payload) {
		t.Errorf("Expected %s, Got %s", expectedJSON, string(payload))
	}
}

func TestPreview(t *testing.T) {
	expectedJSON := `{"aps":{"alert":{"action-loc-key":"foo","body":"hello world","launch-image":"Default2.png","loc-args":["one","two"],"loc-key":"bar"},"badge":777,"content-available":1,"sound":"hi.wav"},"myKey":"myValue"}`
	notification := &push.Notification{
		Data: map[string]string{
			"msg":               "hello world",
			"action-loc-key":    "foo",
			"loc-key":           "bar",
			"loc-args":          "one,two",
			"badge":             "777",
			"sound":             "hi.wav",
			"content-available": "1",
			"img":               "Default2.png",
			"id":                "unused",
			"expiry":            "unused",
			"ttl":               "42",
			"myKey":             "myValue",
			// Keys beginning with "uniqush." are reserved by uniqush.
			"uniqush.foo": "ignored",
		},
	}
	_, _, service, _ := commonAPNSMocks(APNS_UNSUBSCRIBE)
	defer service.Finalize()
	payload, err := service.Preview(notification)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Equal([]byte(expectedJSON), payload) {
		t.Errorf("Expected %s, Got %s", expectedJSON, string(payload))
	}
}

func expectMapEquals(t *testing.T, expected map[string]string, actual map[string]string, description string) {
	for k, v := range expected {
		actualV, ok := actual[k]
		if !ok {
			t.Errorf("%s is missing key %q: expected=%v, got=%v", description, k, expected, actual)
		} else if actualV != v {
			t.Errorf("%s has wrong value for key %q: got=%v, want=%v", description, k, v, actualV)
		}
	}
	for k, v := range actual {
		if _, ok := expected[k]; !ok {
			t.Errorf("%s unexpectedly has a value %q for key %q", description, v, k)
		}
	}
}

func TestBuildPushServiceProviderFromMap(t *testing.T) {
	service, _, _ := newPushServiceWithErrorChannel(APNS_SUCCESS)

	// Overwrite the APNS service.
	psm := push.GetPushServiceManager()
	psm.RegisterPushServiceType(service)

	dp, err := psm.BuildDeliveryPointFromMap(map[string]string{
		"pushservicetype": service.Name(),
		"service":         "mockservice",
		"subscriber":      "mocksubscriber",
		"devtoken":        "303f3f3f",
	})
	if err != nil {
		t.Fatalf("Unexpected err in BuildDeliveryPointFromMap: %v", err)
	}
	expectedFixedData := map[string]string{
		"service":    "mockservice",
		"subscriber": "mocksubscriber",
		"devtoken":   "303f3f3f",
	}
	expectedVolatileData := map[string]string{}
	expectMapEquals(t, expectedFixedData, dp.FixedData, "dp.FixedData")
	expectMapEquals(t, expectedVolatileData, dp.VolatileData, "dp.VolatileData")
}
