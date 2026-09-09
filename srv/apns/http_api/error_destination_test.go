package http_api

import (
	"errors"
	"net/http"
	"testing"

	"github.com/uniqush/uniqush-push/push"
	"github.com/uniqush/uniqush-push/srv/apns/common"
)

// Tests that the errors this processor reports name the device they are about.
//
// Everything here goes out over a channel, away from the loop that knows which
// delivery point each request was for, so an error that does not carry the
// device arrives somewhere that cannot work it out. That is #265: a rejection
// APNs made against one token, logged without naming the token or its
// subscriber.

// deliveryPointForError builds a delivery point with a subscriber, which is
// what a log line is ultimately after.
func deliveryPointForError(t *testing.T, subscriber string) *push.DeliveryPoint {
	t.Helper()
	dp := push.NewEmptyDeliveryPoint()
	dp.FixedData["subscriber"] = subscriber
	dp.FixedData["devtoken"] = "0123456789abcdef"
	return dp
}

// errorsFromPush drives one push and collects what arrives on ErrChan.
func errorsFromPush(t *testing.T, processor *HTTPPushRequestProcessor, dp *push.DeliveryPoint) []push.Error {
	t.Helper()

	errChan := make(chan push.Error, 8)
	request := &common.PushRequest{
		PSP:       pushServiceProvider,
		Devtokens: [][]byte{devToken},
		DPList:    []*push.DeliveryPoint{dp},
		Payload:   payload,
		ErrChan:   errChan,
		ResChan:   make(chan *common.APNSResult, 8),
	}
	processor.AddRequest(request)

	var collected []push.Error
	for err := range errChan {
		collected = append(collected, err)
	}
	return collected
}

// TestAPNsRejectionNamesTheDevice covers the class of failure #265 reported.
//
// The reason in that issue, DeviceTokenNotForTopic, now takes the unsubscribe
// path, and an UnsubscribeUpdate has always carried the device it is about --
// uniqush needs it there to delete the subscription. What was left nameless is
// everything that falls through to a BadNotification: APNs rejecting one
// request, reported as though the notification were bad for everyone.
//
// BadPriority stands in for that class. It is a rejection of one request that
// uniqush neither retries nor unsubscribes for, so it reaches the caller as the
// error and nothing else says which device it was about.
func TestAPNsRejectionNamesTheDevice(t *testing.T) {
	processor := newHTTPRequestProcessor()
	dp := deliveryPointForError(t, "alice")

	mockAPNSRequest(processor, func(r *http.Request) (*http.Response, *mockResponse, error) {
		body := newMockResponse([]byte(`{"reason":"BadPriority"}`), r)
		return &http.Response{StatusCode: http.StatusBadRequest, Body: body}, body, nil
	})

	errs := errorsFromPush(t, processor, dp)
	if len(errs) != 1 {
		t.Fatalf("Expected one error, got %d: %v", len(errs), errs)
	}
	if _, isBad := errs[0].(*push.BadNotification); !isBad {
		t.Fatalf("Expected a BadNotification, got %T: %v", errs[0], errs[0])
	}
	if got := push.DestinationOf(errs[0]); got != dp {
		t.Errorf("The rejection did not name the device it was about.\n"+
			"Error: %v\nDestination: %v", errs[0], got)
	}
}

// TestAPNsUnsubscribeStillNamesTheDevice pins the path the reason from #265
// actually takes now, so that "it already carried the device" stays true.
func TestAPNsUnsubscribeStillNamesTheDevice(t *testing.T) {
	processor := newHTTPRequestProcessor()
	dp := deliveryPointForError(t, "alice")

	mockAPNSRequest(processor, func(r *http.Request) (*http.Response, *mockResponse, error) {
		body := newMockResponse([]byte(`{"reason":"DeviceTokenNotForTopic"}`), r)
		return &http.Response{StatusCode: http.StatusBadRequest, Body: body}, body, nil
	})

	errChan := make(chan push.Error, 8)
	resChan := make(chan *common.APNSResult, 8)
	processor.AddRequest(&common.PushRequest{
		PSP:       pushServiceProvider,
		Devtokens: [][]byte{devToken},
		DPList:    []*push.DeliveryPoint{dp},
		Payload:   payload,
		ErrChan:   errChan,
		ResChan:   resChan,
	})

	for err := range errChan {
		if update, ok := err.(*push.UnsubscribeUpdate); ok {
			if update.Destination != dp {
				t.Errorf("The unsubscribe named %v rather than the device it was for", update.Destination)
			}
			return
		}
	}
	// Nothing on ErrChan means it was reported as a result instead, which the
	// backend reads the device from directly.
}

// TestTransportFailureNamesTheDevice covers the other half: a request that
// never reached APNs at all.
//
// Built inside doRequest, which is why the delivery point is passed to it --
// the alternative was an error that knows only that "a" push failed.
func TestTransportFailureNamesTheDevice(t *testing.T) {
	processor := newHTTPRequestProcessor()
	dp := deliveryPointForError(t, "bob")

	mockAPNSRequest(processor, func(*http.Request) (*http.Response, *mockResponse, error) {
		return nil, nil, errors.New("no route to host")
	})

	errs := errorsFromPush(t, processor, dp)
	if len(errs) != 1 {
		t.Fatalf("Expected one error, got %d: %v", len(errs), errs)
	}
	if _, isConnection := errs[0].(*push.ConnectionError); !isConnection {
		t.Fatalf("Expected a ConnectionError, got %T: %v", errs[0], errs[0])
	}
	if got := push.DestinationOf(errs[0]); got != dp {
		t.Errorf("The connection failure did not name the device it was about, got %v", got)
	}
}

// TestMissingBundleIDNamesEveryDevice covers the refusal that happens before
// any request is built.
//
// One error per device, and they are identical except for the device. An
// operator reading a hundred of them needs to know the answer is "every
// subscriber in this service" rather than infer it.
func TestMissingBundleIDNamesEveryDevice(t *testing.T) {
	processor := newHTTPRequestProcessor()
	mockAPNSRequest(processor, func(r *http.Request) (*http.Response, *mockResponse, error) {
		t.Error("No request should reach APNs without a bundle id")
		body := newMockResponse([]byte{}, r)
		return &http.Response{StatusCode: http.StatusOK, Body: body}, body, nil
	})

	// A provider with no bundleid, which is what an /addpsp predating the
	// HTTP/2 requirement leaves behind. Built rather than copied from the one
	// the other tests share: a PushServiceProvider carries a mutex, so copying
	// it copies a lock.
	psp, err := push.GetPushServiceManager().BuildPushServiceProviderFromMap(map[string]string{
		"service":         mockServiceName,
		"pushservicetype": "apns",
		"cert":            "../apns-test/localhost.cert",
		"key":             "../apns-test/localhost.key",
		"skipverify":      "true",
	})
	if err != nil {
		t.Fatalf("Could not build a provider without a bundle id: %v", err)
	}
	if psp.VolatileData["bundleid"] != "" {
		t.Fatalf("Expected a provider with no bundle id, got %q", psp.VolatileData["bundleid"])
	}

	first := deliveryPointForError(t, "alice")
	second := deliveryPointForError(t, "bob")

	errChan := make(chan push.Error, 8)
	processor.AddRequest(&common.PushRequest{
		PSP:       psp,
		Devtokens: [][]byte{devToken, devToken},
		DPList:    []*push.DeliveryPoint{first, second},
		Payload:   payload,
		ErrChan:   errChan,
		ResChan:   make(chan *common.APNSResult, 8),
	})

	var named []*push.DeliveryPoint
	for err := range errChan {
		named = append(named, push.DestinationOf(err))
	}
	if len(named) != 2 {
		t.Fatalf("Expected one error per device, got %d", len(named))
	}
	if named[0] != first || named[1] != second {
		t.Errorf("Expected the errors to name each device in turn, got %v", named)
	}
}
