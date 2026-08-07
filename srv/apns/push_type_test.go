package apns

import (
	"encoding/hex"
	"strings"
	"sync"
	"testing"

	"github.com/uniqush/uniqush-push/push"
	"github.com/uniqush/uniqush-push/srv/apns/common"
)

func TestPushTypeForNotification(t *testing.T) {
	testCases := []struct {
		name        string
		data        map[string]string
		expected    string
		expectError bool
	}{
		{
			name:     "defaults to alert when unspecified",
			data:     map[string]string{"msg": "hi"},
			expected: common.PushTypeAlert,
		},
		{
			name:     "explicit background",
			data:     map[string]string{"uniqush.apns_push_type": "background"},
			expected: common.PushTypeBackground,
		},
		{
			name:     "explicit liveactivity",
			data:     map[string]string{"uniqush.apns_push_type": "liveactivity"},
			expected: common.PushTypeLiveActivity,
		},
		{
			// uniqush.apns_voip predates the apns-push-type header and is still
			// in use by existing callers. It must keep working.
			name:     "legacy uniqush.apns_voip=1 implies voip",
			data:     map[string]string{"uniqush.apns_voip": "1"},
			expected: common.PushTypeVoIP,
		},
		{
			name:     "uniqush.apns_voip=0 does not imply voip",
			data:     map[string]string{"uniqush.apns_voip": "0"},
			expected: common.PushTypeAlert,
		},
		{
			name: "explicit push type wins over legacy voip flag",
			data: map[string]string{
				"uniqush.apns_push_type": "background",
				"uniqush.apns_voip":      "1",
			},
			expected: common.PushTypeBackground,
		},
		{
			name:     "empty value falls through to the default",
			data:     map[string]string{"uniqush.apns_push_type": ""},
			expected: common.PushTypeAlert,
		},
		{
			// APNs answers an unrecognised value with 400 InvalidPushType, which
			// is a slow and opaque way to learn about a typo.
			name:        "unknown push type is rejected locally",
			data:        map[string]string{"uniqush.apns_push_type": "alerts"},
			expectError: true,
		},
		{
			name:        "push type is case sensitive",
			data:        map[string]string{"uniqush.apns_push_type": "Alert"},
			expectError: true,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			notif := &push.Notification{Data: testCase.data}
			pushType, err := pushTypeForNotification(notif)
			if testCase.expectError {
				if err == nil {
					t.Fatalf("Expected an error, got push type %q", pushType)
				}
				// The message should name the offending value and the alternatives.
				if !strings.Contains(err.Error(), testCase.data["uniqush.apns_push_type"]) {
					t.Errorf("Error should quote the bad value, got: %v", err)
				}
				if !strings.Contains(err.Error(), common.PushTypeAlert) {
					t.Errorf("Error should list the valid push types, got: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}
			if pushType != testCase.expected {
				t.Errorf("Expected push type %q, got %q", testCase.expected, pushType)
			}
		})
	}
}

func TestPriorityForPushType(t *testing.T) {
	// A background push must use priority 5; APNs rejects 10 with BadPriority.
	if got := common.PriorityForPushType(common.PushTypeBackground); got != common.PriorityPowerAware {
		t.Errorf("Expected background push to use priority %s, got %s", common.PriorityPowerAware, got)
	}
	for _, pushType := range []string{common.PushTypeAlert, common.PushTypeVoIP, common.PushTypeLiveActivity, ""} {
		if got := common.PriorityForPushType(pushType); got != common.PriorityImmediate {
			t.Errorf("Expected push type %q to use priority %s, got %s", pushType, common.PriorityImmediate, got)
		}
	}
}

// newPushServiceWithSeparateProcessors builds a pushService whose two transports
// are distinguishable, so a test can tell which one Push selected.
func newPushServiceWithSeparateProcessors() (*pushService, *MockPushRequestProcessor, *MockPushRequestProcessor, chan push.Error) {
	binary := newMockRequestProcessor(APNSSuccess)
	http2 := newMockRequestProcessor(APNSSuccess)
	service := NewPushService().(*pushService)
	service.binaryRequestProcessor = binary
	service.httpRequestProcessor = http2
	errChan := make(chan push.Error, 100)
	service.SetErrorReportChan(errChan)
	return service, binary, http2, errChan
}

func pushOnceForTransportTest(t *testing.T, service *pushService, notif *push.Notification) {
	t.Helper()

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
		t.Fatalf("Could not build push service provider: %v", err)
	}

	resQueue := make(chan *push.Result)
	dpQueue := make(chan *push.DeliveryPoint)
	wg := new(sync.WaitGroup)
	wg.Add(2)
	go asyncCreateDPQueue(wg, dpQueue, hex.EncodeToString([]byte("FakeDevToken")), "unusedsubscriber")
	go asyncPush(wg, service, psp, dpQueue, resQueue, notif)
	for range resQueue { //nolint:revive // draining is the point
	}
	wg.Wait()
}

// TestTransportDefaultsToHTTP2 is the regression test for the change that made
// HTTP/2 the default. Apple shut the binary protocol down on 2021-03-31, so a
// push that silently takes the binary path is a push that never arrives.
func TestTransportDefaultsToHTTP2(t *testing.T) {
	testCases := []struct {
		name          string
		http2Value    string
		setHTTP2      bool
		expectBinary  bool
		expectWarning bool
	}{
		{name: "unset defaults to http2", setHTTP2: false},
		{name: "uniqush.http2=1 uses http2", setHTTP2: true, http2Value: "1"},
		{name: "unrecognised value still uses http2", setHTTP2: true, http2Value: "yes"},
		{
			name:     "uniqush.http2=0 opts back in to binary, with a warning",
			setHTTP2: true, http2Value: "0",
			expectBinary: true, expectWarning: true,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			service, binary, http2, errChan := newPushServiceWithSeparateProcessors()
			defer service.Finalize()

			notif := createNotification("Hello World")
			if testCase.setHTTP2 {
				notif.Data["uniqush.http2"] = testCase.http2Value
			}
			pushOnceForTransportTest(t, service, notif)

			binaryCount := len(binary.recorded())
			http2Count := len(http2.recorded())
			if testCase.expectBinary {
				if binaryCount != 1 || http2Count != 0 {
					t.Errorf("Expected the binary processor to be used; binary=%d http2=%d", binaryCount, http2Count)
				}
			} else {
				if http2Count != 1 || binaryCount != 0 {
					t.Errorf("Expected the HTTP/2 processor to be used; binary=%d http2=%d", binaryCount, http2Count)
				}
			}

			var warned bool
			for len(errChan) > 0 {
				if err := <-errChan; err != nil && strings.Contains(err.Error(), "binary protocol") {
					warned = true
				}
			}
			if warned != testCase.expectWarning {
				t.Errorf("Expected deprecation warning=%v, got %v", testCase.expectWarning, warned)
			}
		})
	}
}

// TestPushTypeReachesTheRequest checks the resolved push type is actually put on
// the PushRequest, which is what the HTTP/2 processor turns into a header.
func TestPushTypeReachesTheRequest(t *testing.T) {
	service, _, http2, _ := newPushServiceWithSeparateProcessors()
	defer service.Finalize()

	notif := createNotification("Hello World")
	notif.Data["uniqush.apns_push_type"] = common.PushTypeBackground
	pushOnceForTransportTest(t, service, notif)

	requests := http2.recorded()
	if len(requests) != 1 {
		t.Fatalf("Expected 1 request, got %d", len(requests))
	}
	if requests[0].PushType != common.PushTypeBackground {
		t.Errorf("Expected PushType %q on the request, got %q", common.PushTypeBackground, requests[0].PushType)
	}
}
