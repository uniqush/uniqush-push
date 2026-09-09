package apns

import (
	"testing"

	"github.com/uniqush/uniqush-push/push"
	"github.com/uniqush/uniqush-push/srv/apns/common"
)

// erroringRequestProcessor reports one error per device over ErrChan, which is
// the path a request-time failure takes.
//
// The results built from that channel are not built from the loop over delivery
// points, so before #265 there was nothing in them to say which device each
// error was for.
type erroringRequestProcessor struct {
	MockPushRequestProcessor
	// build makes the error for the i-th device.
	build func(dp *push.DeliveryPoint) push.Error
}

// AddRequest reports asynchronously, as the real one does. Push hands over the
// request and only then starts reading ErrChan, so a processor that reported
// inline would deadlock on its first send.
func (p *erroringRequestProcessor) AddRequest(request *common.PushRequest) {
	go func() {
		defer close(request.ErrChan)
		for i := range request.Devtokens {
			var dp *push.DeliveryPoint
			if i < len(request.DPList) {
				dp = request.DPList[i]
			}
			request.ErrChan <- p.build(dp)
		}
	}()
}

// pushWithErrors runs one push through a processor that fails, and returns the
// results the backend would log.
func pushWithErrors(t *testing.T, build func(dp *push.DeliveryPoint) push.Error) []*push.Result {
	t.Helper()
	ensureAPNSRegistered()

	service := NewPushService().(*pushService)
	service.httpRequestProcessor = &erroringRequestProcessor{build: build}
	service.SetErrorReportChan(make(chan push.Error, 10))
	defer service.Finalize()

	psp := buildProvider(t, nil)
	dp, err := push.GetPushServiceManager().BuildDeliveryPointFromMap(map[string]string{
		"pushservicetype": "apns",
		"service":         "environments",
		"subscriber":      "alice",
		"devtoken":        "0123456789abcdef",
	})
	if err != nil {
		t.Fatalf("Could not build a delivery point: %v", err)
	}

	dpQueue := make(chan *push.DeliveryPoint, 1)
	dpQueue <- dp
	close(dpQueue)
	resQueue := make(chan *push.Result, 10)
	service.Push(psp, dpQueue, resQueue, createNotification("Hello"))

	var results []*push.Result
	for res := range resQueue {
		results = append(results, res)
	}
	return results
}

// TestResultsCarryTheDeviceAnErrorNames is the end of the chain #265 asked for.
//
// The backend logs a push result, and reads the subscriber out of the result's
// destination. A result built from an error channel has no destination of its
// own, so unless the error carries one the line says Subscriber=Unknown -- for
// a failure that was always about exactly one device.
func TestResultsCarryTheDeviceAnErrorNames(t *testing.T) {
	testCases := []struct {
		name  string
		build func(dp *push.DeliveryPoint) push.Error
	}{
		{
			name: "a rejected notification",
			build: func(dp *push.DeliveryPoint) push.Error {
				return push.NewBadNotificationForDeliveryPoint(dp, "BadPriority")
			},
		},
		{
			// Wrapped by the push service into a new error on its way out, so
			// this is also the test that wrapping does not lose the device.
			name: "a reported error",
			build: func(dp *push.DeliveryPoint) push.Error {
				return push.NewErrorForDeliveryPoint(dp, "something went wrong")
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			results := pushWithErrors(t, testCase.build)
			if len(results) != 1 {
				t.Fatalf("Expected one result, got %d", len(results))
			}
			res := results[0]
			if res.Err == nil {
				t.Fatalf("Expected the result to be an error")
			}
			if res.Destination == nil {
				t.Fatalf("The result names no device, so this failure logs as Subscriber=Unknown: %v", res.Err)
			}
			if got := res.Destination.FixedData["subscriber"]; got != "alice" {
				t.Errorf("Expected the result to name subscriber alice, got %q", got)
			}
		})
	}
}

// TestResultsWithoutADeviceStayEmpty checks the fallback claims nothing it
// should not.
//
// A provider whose credentials are rejected is about every device in the
// service at once. Attaching the first device that happened to be in hand would
// put a subscriber's name on a failure that has nothing to do with them.
func TestResultsWithoutADeviceStayEmpty(t *testing.T) {
	results := pushWithErrors(t, func(*push.DeliveryPoint) push.Error {
		return push.NewBadPushServiceProviderWithDetails(push.NewEmptyPushServiceProvider(), "expired certificate")
	})
	if len(results) != 1 {
		t.Fatalf("Expected one result, got %d", len(results))
	}
	if results[0].Destination != nil {
		t.Errorf("A provider failure named a device: %v", results[0].Destination)
	}
}
