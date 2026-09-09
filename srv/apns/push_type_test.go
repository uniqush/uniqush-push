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

// newPushServiceWithRecordingProcessor builds a pushService whose transport
// records what it was asked to send, so a test can assert on the request rather
// than on a network.
func newPushServiceWithRecordingProcessor() (*pushService, *MockPushRequestProcessor, chan push.Error) {
	http2 := newMockRequestProcessor(APNSSuccess)
	service := NewPushService().(*pushService)
	service.httpRequestProcessor = http2
	errChan := make(chan push.Error, 100)
	service.SetErrorReportChan(errChan)
	return service, http2, errChan
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

// TestObsoleteHTTP2ParameterStillPushes covers what happens to a caller who was
// passing uniqush.http2 when the binary protocol was removed.
//
// Every value has to end in a delivered push, including the 0 that used to
// select binary. Refusing that push would convert a stale request parameter --
// one that has been unable to deliver anything since 2021-03-31 -- into an
// outage on upgrade, which is the opposite of the point. The caller is told
// instead, which is what the warning is for, and only 0 earns it: 1 and the
// unrecognised values were already asking for what they now get.
func TestObsoleteHTTP2ParameterStillPushes(t *testing.T) {
	testCases := []struct {
		name          string
		http2Value    string
		setHTTP2      bool
		expectWarning bool
	}{
		{name: "unset pushes over http2", setHTTP2: false},
		{name: "uniqush.http2=1 pushes over http2", setHTTP2: true, http2Value: "1"},
		{name: "unrecognised value pushes over http2", setHTTP2: true, http2Value: "yes"},
		{
			name:     "uniqush.http2=0 pushes over http2, with a notice",
			setHTTP2: true, http2Value: "0",
			expectWarning: true,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			service, http2, errChan := newPushServiceWithRecordingProcessor()
			defer service.Finalize()

			notif := createNotification("Hello World")
			if testCase.setHTTP2 {
				notif.Data["uniqush.http2"] = testCase.http2Value
			}
			pushOnceForTransportTest(t, service, notif)

			if pushed := len(http2.recorded()); pushed != 1 {
				t.Errorf("Expected the push to be sent over HTTP/2; got %d requests", pushed)
			}

			var warned bool
			for len(errChan) > 0 {
				if err := <-errChan; err != nil && strings.Contains(err.Error(), "binary protocol") {
					warned = true
				}
			}
			if warned != testCase.expectWarning {
				t.Errorf("Expected notice=%v, got %v", testCase.expectWarning, warned)
			}
		})
	}
}

// TestPushTypeReachesTheRequest checks the resolved push type is actually put on
// the PushRequest, which is what the HTTP/2 processor turns into a header.
func TestPushTypeReachesTheRequest(t *testing.T) {
	service, http2, _ := newPushServiceWithRecordingProcessor()
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
