package apns

import (
	"encoding/pem"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/uniqush/uniqush-push/conf"
	"github.com/uniqush/uniqush-push/push"
	"github.com/uniqush/uniqush-push/srv/apns/apnstest"
)

func TestAPNsPerDeviceResults(t *testing.T) {
	cases := []struct {
		name      string
		responses []apnstest.Response
		firstID   uint32
	}{
		{name: "empty batch"},
		{name: "all accepted", responses: make([]apnstest.Response, 3)},
		{
			name:      "message IDs wrap",
			responses: make([]apnstest.Response, 3),
			firstID:   math.MaxUint32 - 1,
		},
		{
			name: "mixed outcomes",
			responses: []apnstest.Response{
				{},
				{Status: http.StatusServiceUnavailable, Reason: apnstest.ReasonServiceUnavailable},
				{Status: http.StatusGone, Reason: apnstest.ReasonUnregistered},
				{Status: http.StatusBadRequest, Reason: apnstest.ReasonBadPriority},
			},
		},
		{
			name: "unregistered device",
			responses: []apnstest.Response{
				{Status: http.StatusGone, Reason: apnstest.ReasonUnregistered},
			},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			server := startSimulator(t)
			service, asyncErrors := newSimulatorService(t)
			service.nextMessageID = testCase.firstID
			psp := newSimulatorPSP(t, server, nil)
			notif := createNotification("per-device results")
			tokens := make([]string, len(testCase.responses))
			for i, response := range testCase.responses {
				tokens[i] = apnstest.DeviceToken(byte(i + 1))
				server.SetResponse(tokens[i], response)
			}

			results := pushToSimulator(t, service, psp, notif, tokens...)
			if len(results) != len(tokens) {
				t.Fatalf("Expected one result for each of %d devices, got %s", len(tokens), describeResults(results))
			}
			byToken := make(map[string]*push.Result, len(results))
			for _, result := range results {
				if result.Destination == nil {
					t.Fatalf("A per-device outcome has no destination: %v", result.Err)
				}
				token := result.Destination.FixedData["devtoken"]
				if _, duplicate := byToken[token]; duplicate {
					t.Fatalf("Device %s was reported more than once", token)
				}
				byToken[token] = result
				if result.Provider != psp || result.Content != notif {
					t.Errorf("Device %s lost its provider or notification", token)
				}
			}
			for i, token := range tokens {
				result := byToken[token]
				if result == nil {
					t.Fatalf("Missing result for device %s", token)
				}
				switch testCase.responses[i].Status {
				case 0:
					if result.Err != nil {
						t.Errorf("Accepted device %s failed: %v", token, result.Err)
					}
					wantID := fmt.Sprintf("apns:%s-%d", psp.Name(), testCase.firstID+uint32(i))
					if result.MsgID != wantID {
						t.Errorf("Device %s has message ID %q, want %q", token, result.MsgID, wantID)
					}
				case http.StatusServiceUnavailable:
					retry, ok := result.Err.(*push.RetryError)
					if !ok {
						t.Fatalf("Expected a retry for device %s, got %T: %v", token, result.Err, result.Err)
					}
					if retry.Provider != psp || retry.Destination != result.Destination || retry.Content != notif || retry.After <= 0 {
						t.Errorf("Retry lost its destination, content, provider or backoff: %+v", retry)
					}
				case http.StatusGone:
					removed, ok := result.Err.(*push.UnsubscribeUpdate)
					if !ok {
						t.Fatalf("Expected an unsubscribe for device %s, got %T: %v", token, result.Err, result.Err)
					}
					if removed.Provider != psp || removed.Destination != result.Destination {
						t.Errorf("Unsubscribe lost its provider or destination: %+v", removed)
					}
				case http.StatusBadRequest:
					if _, ok := result.Err.(*push.BadNotification); !ok {
						t.Errorf("Expected a bad notification for device %s, got %T: %v", token, result.Err, result.Err)
					}
				}
				if result.Err != nil && result.MsgID != "" {
					t.Errorf("Rejected device %s also received success ID %q", token, result.MsgID)
				}
			}
			assertNoViolations(t, server)
			select {
			case err := <-asyncErrors:
				t.Errorf("Outcome was also reported asynchronously: %v", err)
			case <-time.After(20 * time.Millisecond):
			}
		})
	}
}

func TestAPNsWaitsForResponseOrDeadline(t *testing.T) {
	for _, timeout := range []bool{false, true} {
		t.Run(fmt.Sprintf("timeout=%t", timeout), func(t *testing.T) {
			allowNonAppleEndpoints(t)
			started := make(chan struct{}, 1)
			release := make(chan struct{})
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.ProtoMajor != 2 {
					t.Errorf("Expected HTTP/2, got %s", r.Proto)
				}
				started <- struct{}{}
				select {
				case <-release:
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusGone)
					_, _ = w.Write([]byte(`{"reason":"Unregistered"}`))
				case <-r.Context().Done():
				}
			}))
			server.EnableHTTP2 = true
			server.StartTLS()
			t.Cleanup(server.Close)
			caPath := filepath.Join(t.TempDir(), "ca.pem")
			ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
			if err := os.WriteFile(caPath, ca, 0600); err != nil {
				t.Fatal(err)
			}
			service, _ := newSimulatorService(t)
			configuration := conf.NewConfigFile()
			requestTimeout := "5"
			if timeout {
				requestTimeout = "1"
			}
			configuration.AddOption("apns", "request_timeout", requestTimeout)
			configuration.AddOption("apns", "allow_non_apple_endpoints", "true")
			service.SetPushServiceConfig(push.NewPushServiceConfig(configuration, "apns"))
			psp := buildProvider(t, map[string]string{"endpoint": server.URL, "cacert": caPath})
			dp := push.NewEmptyDeliveryPoint()
			dp.FixedData["devtoken"] = apnstest.DeviceToken(0x72)
			dp.FixedData["subscriber"] = "waiting"
			notif := createNotification("wait for the outcome")
			destinations := make(chan *push.DeliveryPoint, 1)
			destinations <- dp
			close(destinations)
			results := make(chan *push.Result, 2)
			go service.Push(psp, destinations, results, notif)
			select {
			case <-started:
			case <-time.After(5 * time.Second):
				t.Fatal("The request did not reach the HTTP/2 server")
			}
			if !timeout {
				select {
				case result := <-results:
					t.Fatalf("Push reported an outcome before the response: %+v", result)
				case <-time.After(30 * time.Millisecond):
				}
				close(release)
			}
			select {
			case result := <-results:
				if result == nil || result.Err == nil {
					t.Fatalf("Expected the rejected or timed-out push to fail, got %+v", result)
				}
				if result.Provider != psp || result.Destination != dp || result.Content != notif {
					t.Error("The outcome lost its provider, destination or notification")
				}
				if timeout {
					if _, ok := result.Err.(*push.ConnectionError); !ok {
						t.Errorf("Expected a connection timeout, got %T: %v", result.Err, result.Err)
					}
				} else if _, ok := result.Err.(*push.UnsubscribeUpdate); !ok {
					t.Errorf("Expected the server's unsubscribe, got %T: %v", result.Err, result.Err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("Push did not finish after its response or one-second deadline")
			}
			select {
			case result, open := <-results:
				if open {
					t.Errorf("Device was reported twice: %+v", result)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("Push did not close its result channel")
			}
		})
	}
}

func TestAPNsProviderFailureKeepsOtherDeviceResults(t *testing.T) {
	server := startSimulator(t)
	service, _ := newSimulatorService(t)
	psp := newSimulatorPSP(t, server, nil)
	notif := createNotification("mixed provider failure")
	accepted := apnstest.DeviceToken(0x31)
	rejected := apnstest.DeviceToken(0x32)
	server.SetResponse(rejected, apnstest.Response{
		Status: http.StatusForbidden,
		Reason: apnstest.ReasonForbidden,
	})
	results := pushToSimulator(t, service, psp, notif, accepted, rejected)
	if len(results) != 2 {
		t.Fatalf("Expected a provider failure and a success, got %s", describeResults(results))
	}
	var successes, failures int
	for _, result := range results {
		if result.Provider != psp || result.Content != notif {
			t.Error("Outcome lost its provider or notification")
		}
		if result.Err == nil {
			successes++
			if result.Destination == nil || result.Destination.FixedData["devtoken"] != accepted || result.MsgID == "" {
				t.Errorf("Success did not name the accepted device: %+v", result)
			}
			continue
		}
		failures++
		if _, ok := result.Err.(*push.BadPushServiceProvider); !ok {
			t.Errorf("Expected a provider error, got %T: %v", result.Err, result.Err)
		}
		if result.Destination != nil || result.MsgID != "" {
			t.Errorf("Provider failure was assigned a device or a success ID: %+v", result)
		}
	}
	if successes != 1 || failures != 1 {
		t.Errorf("Expected one success and one failure, got %d successes and %d failures", successes, failures)
	}
	assertNoViolations(t, server)
}
