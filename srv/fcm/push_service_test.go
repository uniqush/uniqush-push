package fcm

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/uniqush/uniqush-push/push"
)

// roundTripFunc stands in for the network.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func newResponse(status int, body string, header http.Header) *http.Response {
	if header == nil {
		header = http.Header{}
	}
	return &http.Response{
		StatusCode: status,
		Header:     header,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// serviceAccountJSON builds a syntactically real service account document.
//
// Registration now parses the file rather than stat-ing it, so a stub like
// `{"type":"service_account"}` is correctly rejected: the parse needs a usable
// private key. Generating one costs a moment, so it is done once for the
// package. No token is ever fetched -- parsing is offline, and tests override
// the client factory anyway.
var (
	serviceAccountOnce sync.Once
	serviceAccountBody []byte
	serviceAccountErr  error
)

func serviceAccountJSON(t *testing.T) []byte {
	t.Helper()
	serviceAccountOnce.Do(func() {
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			serviceAccountErr = err
			return
		}
		der, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			serviceAccountErr = err
			return
		}
		pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
		serviceAccountBody, serviceAccountErr = json.Marshal(map[string]string{
			"type":         "service_account",
			"project_id":   "my-project",
			"private_key":  string(pemBytes),
			"client_email": "uniqush@my-project.iam.gserviceaccount.com",
			"token_uri":    "https://oauth2.googleapis.com/token",
		})
	})
	if serviceAccountErr != nil {
		t.Fatalf("Could not build a test service account: %v", serviceAccountErr)
	}
	return serviceAccountBody
}

func credentialsFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "service-account.json")
	if err := os.WriteFile(path, serviceAccountJSON(t), 0600); err != nil {
		t.Fatalf("Could not write the test credentials file: %v", err)
	}
	return path
}

func newTestService(t *testing.T, name string, handler roundTripFunc) *pushService {
	t.Helper()
	service := NewPushService(name).(*pushService)
	service.OverrideClientFactory(func(*push.PushServiceProvider) (HTTPClient, error) {
		return &http.Client{Transport: handler}, nil
	})
	return service
}

// registerOnce attaches a push service type to the global manager.
//
// Push service providers and delivery points must be built through the manager,
// because that is what attaches the push service type to them. Without it,
// PushPeer.Name() and PushServiceName() dereference a nil and panic.
//
// The manager is a singleton and rejects duplicate names, so registration
// happens once per name. That is harmless here: the registered instance is only
// consulted for building and for its name, while each test drives its own
// instance directly, which is where the mocked client factory lives.
var registerOnce sync.Map

func registerForTest(name string) {
	if _, loaded := registerOnce.LoadOrStore(name, true); loaded {
		return
	}
	//nolint:errcheck // a duplicate registration from another test is fine
	push.GetPushServiceManager().RegisterPushServiceType(NewPushService(name))
}

func newTestPSP(t *testing.T, service *pushService) *push.PushServiceProvider {
	t.Helper()
	registerForTest(service.Name())
	psp, err := push.GetPushServiceManager().BuildPushServiceProviderFromMap(map[string]string{
		"service":         "testservice",
		"pushservicetype": service.Name(),
		"projectid":       "my-project",
		"credentialsfile": credentialsFile(t),
	})
	if err != nil {
		t.Fatalf("Could not build push service provider: %v", err)
	}
	return psp
}

func newTestDP(t *testing.T, service *pushService, regID string) *push.DeliveryPoint {
	t.Helper()
	registerForTest(service.Name())
	dp, err := push.GetPushServiceManager().BuildDeliveryPointFromMap(map[string]string{
		"service":         "testservice",
		"subscriber":      "testsubscriber",
		"pushservicetype": service.Name(),
		"regid":           regID,
	})
	if err != nil {
		t.Fatalf("Could not build delivery point: %v", err)
	}
	return dp
}

// TestProviderFixedDataShapeIsPreserved is the upgrade-compatibility test, and
// the reason projectid lives in a different map depending on the name.
//
// A PushPeer's name hashes its FixedData, and /addpsp rejects an update whose
// fixed data changed -- it reads it as a second, conflicting provider. The two
// legacy backends disagreed about the shape: gcm kept projectid in FixedData,
// fcm had no projectid at all. If either shape moves, existing providers cannot
// be updated in place and every device has to re-subscribe.
func TestProviderFixedDataShapeIsPreserved(t *testing.T) {
	buildDirectly := func(t *testing.T, name string) *push.PushServiceProvider {
		t.Helper()
		service := NewPushService(name).(*pushService)
		psp := push.NewEmptyPushServiceProvider()
		if err := service.BuildPushServiceProviderFromMap(map[string]string{
			"service":         "testservice",
			"projectid":       "my-project",
			"credentialsfile": credentialsFile(t),
		}, psp); err != nil {
			t.Fatalf("Could not build push service provider: %v", err)
		}
		return psp
	}

	t.Run("fcm keeps only service fixed", func(t *testing.T) {
		psp := buildDirectly(t, "fcm")
		if got := psp.FixedData["projectid"]; got != "" {
			t.Errorf("fcm must not put projectid in FixedData, got %q", got)
		}
		if got := psp.VolatileData["projectid"]; got != "my-project" {
			t.Errorf("fcm should keep projectid in VolatileData, got %q", got)
		}
		if len(psp.FixedData) != 1 || psp.FixedData["service"] != "testservice" {
			t.Errorf("fcm FixedData should be exactly {service}, got %v", psp.FixedData)
		}
	})

	t.Run("gcm keeps service and projectid fixed", func(t *testing.T) {
		psp := buildDirectly(t, "gcm")
		if got := psp.FixedData["projectid"]; got != "my-project" {
			t.Errorf("gcm should keep projectid in FixedData, got %q", got)
		}
		if len(psp.FixedData) != 2 {
			t.Errorf("gcm FixedData should be exactly {service, projectid}, got %v", psp.FixedData)
		}
	})

	t.Run("the credential is never part of the identity", func(t *testing.T) {
		for _, name := range []string{"fcm", "gcm"} {
			psp := buildDirectly(t, name)
			if got := psp.FixedData["credentialsfile"]; got != "" {
				t.Errorf("%s must not put credentialsfile in FixedData, got %q", name, got)
			}
			if psp.VolatileData["credentialsfile"] == "" {
				t.Errorf("%s should keep credentialsfile in VolatileData", name)
			}
		}
	})
}

func TestBuildPushServiceProviderRejectsMissingFields(t *testing.T) {
	service := NewPushService("fcm").(*pushService)
	base := map[string]string{
		"service":         "testservice",
		"projectid":       "my-project",
		"credentialsfile": credentialsFile(t),
	}
	for _, missing := range []string{"service", "projectid", "credentialsfile"} {
		t.Run("no "+missing, func(t *testing.T) {
			kv := map[string]string{}
			for k, v := range base {
				kv[k] = v
			}
			delete(kv, missing)
			psp := push.NewEmptyPushServiceProvider()
			if err := service.BuildPushServiceProviderFromMap(kv, psp); err == nil {
				t.Error("Expected an error")
			}
		})
	}

	// Registration reads and parses the credentials rather than stat-ing the
	// path. os.Stat would be the obvious check and is not good enough: it
	// succeeds on a file the process cannot open, so a permissions mistake would
	// pass /addpsp and only surface later as a failed push -- while the error
	// message claimed the file had been checked for readability.
	t.Run("the credentials file is validated at registration", func(t *testing.T) {
		withCredentials := func(t *testing.T, contents []byte, mode os.FileMode) error {
			t.Helper()
			path := filepath.Join(t.TempDir(), "service-account.json")
			if err := os.WriteFile(path, contents, 0600); err != nil {
				t.Fatalf("Could not seed the credentials file: %v", err)
			}
			if mode != 0600 {
				if err := os.Chmod(path, mode); err != nil {
					t.Fatalf("Could not set the mode: %v", err)
				}
				t.Cleanup(func() { _ = os.Chmod(path, 0600) })
			}
			kv := map[string]string{}
			for k, v := range base {
				kv[k] = v
			}
			kv["credentialsfile"] = path
			return service.BuildPushServiceProviderFromMap(kv, push.NewEmptyPushServiceProvider())
		}

		t.Run("a missing file", func(t *testing.T) {
			kv := map[string]string{}
			for k, v := range base {
				kv[k] = v
			}
			kv["credentialsfile"] = "/nonexistent/service-account.json"
			if err := service.BuildPushServiceProviderFromMap(kv, push.NewEmptyPushServiceProvider()); err == nil {
				t.Error("Expected an error for a missing credentials file")
			}
		})

		t.Run("a file that cannot be read", func(t *testing.T) {
			if os.Geteuid() == 0 {
				t.Skip("running as root, which can read anything")
			}
			err := withCredentials(t, serviceAccountJSON(t), 0)
			if err == nil {
				t.Fatal("Expected an error: os.Stat would have accepted this, which is the bug")
			}
			if !strings.Contains(err.Error(), "could not read") {
				t.Errorf("Expected a read error, got: %v", err)
			}
		})

		t.Run("a file that is not JSON", func(t *testing.T) {
			if err := withCredentials(t, []byte("not json"), 0600); err == nil {
				t.Error("Expected an error for a non-JSON credentials file")
			}
		})

		t.Run("the wrong kind of Google credential", func(t *testing.T) {
			// Pointing at an OAuth client or a google-services.json instead of a
			// service account is an easy mistake and opaque to debug at push time.
			other := []byte(`{"type":"authorized_user","client_id":"x","client_secret":"y","refresh_token":"z"}`)
			err := withCredentials(t, other, 0600)
			if err == nil {
				t.Error("Expected an error for a credential that is not a service account")
			}
		})

		t.Run("a valid service account is accepted", func(t *testing.T) {
			if err := withCredentials(t, serviceAccountJSON(t), 0600); err != nil {
				t.Errorf("Expected a valid service account to be accepted, got: %v", err)
			}
		})
	})
}

func TestBuildDeliveryPointFromMap(t *testing.T) {
	service := NewPushService("fcm").(*pushService)
	dp := push.NewEmptyDeliveryPoint()
	err := service.BuildDeliveryPointFromMap(map[string]string{
		"service": "testservice", "subscriber": "sub", "regid": "abc123", "account": "acct",
	}, dp)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	// regid is the device's identity and must stay fixed, so existing
	// subscriptions keep their database key.
	if dp.FixedData["regid"] != "abc123" {
		t.Errorf("Expected regid in FixedData, got %v", dp.FixedData)
	}
	if dp.FixedData["account"] != "acct" {
		t.Errorf("Expected account in FixedData, got %v", dp.FixedData)
	}

	dp = push.NewEmptyDeliveryPoint()
	if err := service.BuildDeliveryPointFromMap(map[string]string{
		"service": "testservice", "subscriber": "sub",
	}, dp); err == nil {
		t.Error("Expected an error when regid is missing")
	}
}

// decodeSentMessage pulls the v1 message out of a captured request.
func decodeSentMessage(t *testing.T, request *http.Request) map[string]interface{} {
	t.Helper()
	body, err := io.ReadAll(request.Body)
	if err != nil {
		t.Fatalf("Could not read the request body: %v", err)
	}
	var envelope map[string]interface{}
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("Request body is not JSON: %v (%s)", err, body)
	}
	message, ok := envelope["message"].(map[string]interface{})
	if !ok {
		t.Fatalf("Expected a top-level \"message\" object, got %s", body)
	}
	return message
}

func pushOnce(t *testing.T, service *pushService, psp *push.PushServiceProvider, dp *push.DeliveryPoint, notif *push.Notification) *push.Result {
	t.Helper()
	dpQueue := make(chan *push.DeliveryPoint, 1)
	dpQueue <- dp
	close(dpQueue)
	resQueue := make(chan *push.Result, 4)
	service.Push(psp, dpQueue, resQueue, notif)

	var results []*push.Result
	for res := range resQueue {
		results = append(results, res)
	}
	if len(results) != 1 {
		t.Fatalf("Expected exactly 1 result, got %d", len(results))
	}
	return results[0]
}

// TestPushSendsV1Message covers the request shape: the v1 URL with the project
// id in the path, one token per request, and the message envelope.
func TestPushSendsV1Message(t *testing.T) {
	var captured *http.Request
	var message map[string]interface{}
	service := newTestService(t, "fcm", func(r *http.Request) (*http.Response, error) {
		captured = r
		message = decodeSentMessage(t, r)
		return newResponse(200, `{"name":"projects/my-project/messages/0:123"}`, nil), nil
	})
	defer service.Finalize()

	psp := newTestPSP(t, service)
	dp := newTestDP(t, service, "token-1")
	result := pushOnce(t, service, psp, dp, &push.Notification{Data: map[string]string{"msg": "hello"}})

	if result.Err != nil {
		t.Fatalf("Expected success, got: %v", result.Err)
	}
	if captured == nil {
		t.Fatal("No request was made")
	}
	// The project id belongs in the path; this is the biggest visible change
	// from the legacy endpoint.
	wantURL := "https://fcm.googleapis.com/v1/projects/my-project/messages:send"
	if got := captured.URL.String(); got != wantURL {
		t.Errorf("Expected URL %s, got %s", wantURL, got)
	}
	if got := message["token"]; got != "token-1" {
		t.Errorf("Expected the token in the message body, got %v", got)
	}
	// v1 has no registration_ids array; sending one would be silently ignored.
	if _, ok := message["registration_ids"]; ok {
		t.Error("registration_ids is a legacy field and must not be sent")
	}
	if !strings.Contains(result.MsgID, "projects/my-project/messages/0:123") {
		t.Errorf("Expected the returned message name in MsgID, got %q", result.MsgID)
	}
	// "application/json", not the "application/json; UTF-8" Google's own examples
	// show. That is not a valid media type parameter -- the parameter is spelled
	// charset -- and a strict proxy is entitled to object. JSON is UTF-8 by
	// definition (RFC 8259 s8.1), so there is nothing to declare.
	if got := captured.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Expected Content-Type application/json, got %q", got)
	}
}

func TestPushPayloadMapping(t *testing.T) {
	testCases := []struct {
		name  string
		data  map[string]string
		check func(t *testing.T, message map[string]interface{})
	}{
		{
			name: "plain fields become string data",
			data: map[string]string{"msg": "hello", "badge": "3"},
			check: func(t *testing.T, message map[string]interface{}) {
				data, _ := message["data"].(map[string]interface{})
				if data["msg"] != "hello" || data["badge"] != "3" {
					t.Errorf("Expected msg and badge in data, got %v", data)
				}
			},
		},
		{
			name: "uniqush control keys are stripped",
			data: map[string]string{"msg": "hello", "uniqush.selfonly": "1", "ttl": "60", "msggroup": "g"},
			check: func(t *testing.T, message map[string]interface{}) {
				data, _ := message["data"].(map[string]interface{})
				for _, key := range []string{"uniqush.selfonly", "ttl", "msggroup"} {
					if _, ok := data[key]; ok {
						t.Errorf("%q should not appear in data, got %v", key, data)
					}
				}
			},
		},
		{
			// The legacy field was time_to_live, a bare integer. v1 wants a
			// Duration string under android.
			name: "ttl becomes an android duration string",
			data: map[string]string{"msg": "x", "ttl": "60"},
			check: func(t *testing.T, message map[string]interface{}) {
				android, _ := message["android"].(map[string]interface{})
				if android["ttl"] != "60s" {
					t.Errorf("Expected android.ttl of \"60s\", got %v", android["ttl"])
				}
			},
		},
		{
			name: "ttl defaults to an hour",
			data: map[string]string{"msg": "x"},
			check: func(t *testing.T, message map[string]interface{}) {
				android, _ := message["android"].(map[string]interface{})
				if android["ttl"] != "3600s" {
					t.Errorf("Expected the default ttl of \"3600s\", got %v", android["ttl"])
				}
			},
		},
		{
			name: "msggroup becomes android collapse_key",
			data: map[string]string{"msg": "x", "msggroup": "chat-42"},
			check: func(t *testing.T, message map[string]interface{}) {
				android, _ := message["android"].(map[string]interface{})
				if android["collapse_key"] != "chat-42" {
					t.Errorf("Expected collapse_key chat-42, got %v", android["collapse_key"])
				}
			},
		},
		{
			name: "a raw payload replaces the data map",
			data: map[string]string{"msg": "ignored", "uniqush.payload.fcm": `{"custom":"value"}`},
			check: func(t *testing.T, message map[string]interface{}) {
				data, _ := message["data"].(map[string]interface{})
				if data["custom"] != "value" {
					t.Errorf("Expected the raw payload to be used, got %v", data)
				}
				if _, ok := data["msg"]; ok {
					t.Errorf("A raw payload replaces the data map wholesale, got %v", data)
				}
			},
		},
		{
			// The legacy backend allowed data and notification together, and
			// callers rely on it: data goes to the app, notification is drawn
			// by the OS.
			name: "notification and data coexist",
			data: map[string]string{"msg": "x", "uniqush.notification.fcm": `{"title":"Hi","body":"There"}`},
			check: func(t *testing.T, message map[string]interface{}) {
				notification, _ := message["notification"].(map[string]interface{})
				if notification["title"] != "Hi" {
					t.Errorf("Expected the notification block, got %v", notification)
				}
				data, _ := message["data"].(map[string]interface{})
				if data["msg"] != "x" {
					t.Errorf("Expected data to survive alongside notification, got %v", data)
				}
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var message map[string]interface{}
			service := newTestService(t, "fcm", func(r *http.Request) (*http.Response, error) {
				message = decodeSentMessage(t, r)
				return newResponse(200, `{"name":"n"}`, nil), nil
			})
			defer service.Finalize()
			psp := newTestPSP(t, service)
			dp := newTestDP(t, service, "token-1")
			result := pushOnce(t, service, psp, dp, &push.Notification{Data: testCase.data})
			if result.Err != nil {
				t.Fatalf("Unexpected error: %v", result.Err)
			}
			testCase.check(t, message)
		})
	}
}

// TestNonStringDataIsRejectedLocally covers the one payload rule v1 added. The
// legacy API accepted arbitrary JSON in data and uniqush passed it through, so
// this is a real behaviour change for callers and deserves a specific message
// rather than FCM's opaque 400.
func TestNonStringDataIsRejectedLocally(t *testing.T) {
	service := newTestService(t, "fcm", func(*http.Request) (*http.Response, error) {
		t.Error("No request should be made for an invalid payload")
		return newResponse(200, `{}`, nil), nil
	})
	defer service.Finalize()
	psp := newTestPSP(t, service)
	dp := newTestDP(t, service, "token-1")

	for _, raw := range []string{`{"count":3}`, `{"nested":{"a":"b"}}`, `{"flag":true}`} {
		result := pushOnce(t, service, psp, dp, &push.Notification{
			Data: map[string]string{"uniqush.payload.fcm": raw},
		})
		if _, ok := result.Err.(*push.BadNotification); !ok {
			t.Errorf("Expected a BadNotification for %s, got %T: %v", raw, result.Err, result.Err)
		}
		if !strings.Contains(fmt.Sprint(result.Err), "string") {
			t.Errorf("Expected the error to explain the string requirement, got: %v", result.Err)
		}
	}
}

// TestErrorMapping is the heart of the change. The legacy API had NotRegistered
// and InvalidRegistration as distinct signals; v1 collapses most bad input into
// INVALID_ARGUMENT, so mapping that to "unsubscribe" would delete working
// subscriptions whenever a caller sent a bad payload.
func TestErrorMapping(t *testing.T) {
	fcmError := func(code string, message string) string {
		return fmt.Sprintf(`{"error":{"code":400,"message":%q,"status":"INVALID_ARGUMENT","details":[
			{"@type":"type.googleapis.com/google.firebase.fcm.v1.FcmError","errorCode":%q}]}}`, message, code)
	}

	testCases := []struct {
		name     string
		status   int
		body     string
		header   http.Header
		expected func(t *testing.T, err push.Error)
	}{
		{
			name: "UNREGISTERED unsubscribes", status: 404,
			body: fcmError("UNREGISTERED", "app instance unregistered"),
			expected: func(t *testing.T, err push.Error) {
				if _, ok := err.(*push.UnsubscribeUpdate); !ok {
					t.Errorf("Expected UnsubscribeUpdate, got %T: %v", err, err)
				}
			},
		},
		{
			name: "SENDER_ID_MISMATCH unsubscribes", status: 403,
			body: fcmError("SENDER_ID_MISMATCH", "wrong project"),
			expected: func(t *testing.T, err push.Error) {
				if _, ok := err.(*push.UnsubscribeUpdate); !ok {
					t.Errorf("Expected UnsubscribeUpdate, got %T: %v", err, err)
				}
			},
		},
		{
			name: "INVALID_ARGUMENT does NOT unsubscribe", status: 400,
			body: fcmError("INVALID_ARGUMENT", "payload too big"),
			expected: func(t *testing.T, err push.Error) {
				if _, ok := err.(*push.UnsubscribeUpdate); ok {
					t.Fatal("INVALID_ARGUMENT must not unsubscribe: it usually means our payload was wrong, " +
						"and dropping the subscription would destroy a working registration")
				}
				if _, ok := err.(*push.BadNotification); !ok {
					t.Errorf("Expected BadNotification, got %T: %v", err, err)
				}
			},
		},
		{
			name: "QUOTA_EXCEEDED retries and honours Retry-After", status: 429,
			body:   fcmError("QUOTA_EXCEEDED", "slow down"),
			header: http.Header{"Retry-After": []string{"120"}},
			expected: func(t *testing.T, err push.Error) {
				retry, ok := err.(*push.RetryError)
				if !ok {
					t.Fatalf("Expected RetryError, got %T: %v", err, err)
				}
				if retry.After.Seconds() != 120 {
					t.Errorf("Expected the Retry-After header to be honoured, got %v", retry.After)
				}
			},
		},
		{
			name: "UNAVAILABLE retries", status: 503,
			body: fcmError("UNAVAILABLE", "overloaded"),
			expected: func(t *testing.T, err push.Error) {
				if _, ok := err.(*push.RetryError); !ok {
					t.Errorf("Expected RetryError, got %T: %v", err, err)
				}
			},
		},
		{
			name: "INTERNAL retries", status: 500,
			body: fcmError("INTERNAL", "boom"),
			expected: func(t *testing.T, err push.Error) {
				if _, ok := err.(*push.RetryError); !ok {
					t.Errorf("Expected RetryError, got %T: %v", err, err)
				}
			},
		},
		{
			name: "THIRD_PARTY_AUTH_ERROR blames the provider, not the device", status: 401,
			body: fcmError("THIRD_PARTY_AUTH_ERROR", "bad APNs cert in the Firebase project"),
			expected: func(t *testing.T, err push.Error) {
				if _, ok := err.(*push.BadPushServiceProvider); !ok {
					t.Errorf("Expected BadPushServiceProvider, got %T: %v", err, err)
				}
			},
		},
		{
			// Exactly what the decommissioned legacy endpoint returns today.
			name: "an HTML 404 explains the legacy decommission", status: 404,
			body: "<HTML><HEAD><TITLE>Not Found</TITLE></HEAD></HTML>",
			expected: func(t *testing.T, err push.Error) {
				if !strings.Contains(err.Error(), "2024-06-20") {
					t.Errorf("Expected the error to mention the legacy decommission, got: %v", err)
				}
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			service := newTestService(t, "fcm", func(*http.Request) (*http.Response, error) {
				return newResponse(testCase.status, testCase.body, testCase.header), nil
			})
			defer service.Finalize()
			psp := newTestPSP(t, service)
			dp := newTestDP(t, service, "token-1")
			result := pushOnce(t, service, psp, dp, &push.Notification{Data: map[string]string{"msg": "x"}})
			if result.Err == nil {
				t.Fatal("Expected an error")
			}
			testCase.expected(t, result.Err)
		})
	}
}

// TestPushFansOutPerToken checks the architectural change: no multicast, one
// request per registration token, each carrying only its own token.
func TestPushFansOutPerToken(t *testing.T) {
	var mu sync.Mutex
	var tokens []string
	service := newTestService(t, "fcm", func(r *http.Request) (*http.Response, error) {
		message := decodeSentMessage(t, r)
		mu.Lock()
		tokens = append(tokens, fmt.Sprint(message["token"]))
		mu.Unlock()
		return newResponse(200, `{"name":"n"}`, nil), nil
	})
	defer service.Finalize()

	psp := newTestPSP(t, service)
	const count = 50 // more than maxConcurrentPushes, to exercise the semaphore

	dpQueue := make(chan *push.DeliveryPoint, count)
	for i := 0; i < count; i++ {
		dpQueue <- newTestDP(t, service, fmt.Sprintf("token-%d", i))
	}
	close(dpQueue)
	resQueue := make(chan *push.Result, count)
	service.Push(psp, dpQueue, resQueue, &push.Notification{Data: map[string]string{"msg": "x"}})

	results := 0
	for res := range resQueue {
		results++
		if res.Err != nil {
			t.Errorf("Unexpected error: %v", res.Err)
		}
	}
	if results != count {
		t.Errorf("Expected %d results, got %d", count, results)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(tokens) != count {
		t.Fatalf("Expected %d requests, one per token, got %d", count, len(tokens))
	}
	seen := map[string]bool{}
	for _, token := range tokens {
		if seen[token] {
			t.Errorf("Token %s was sent more than once", token)
		}
		seen[token] = true
	}
}

// TestClientIsCachedPerProvider guards against re-authenticating on every push.
//
// The obvious place to cache an authenticated client is on the
// PushServiceProvider, and it does not work: a PSP is rebuilt from its
// serialized form for every request, so anything stored on it is discarded
// immediately. The result is a credentials-file read and a fresh OAuth2 token
// fetch per push batch, which is invisible until it is a latency problem.
func TestClientIsCachedPerProvider(t *testing.T) {
	var mu sync.Mutex
	factoryCalls := 0

	service := NewPushService("fcm").(*pushService)
	service.OverrideClientFactory(func(*push.PushServiceProvider) (HTTPClient, error) {
		mu.Lock()
		factoryCalls++
		mu.Unlock()
		return &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return newResponse(200, `{"name":"n"}`, nil), nil
		})}, nil
	})
	defer service.Finalize()

	psp := newTestPSP(t, service)
	for i := 0; i < 5; i++ {
		pushOnce(t, service, psp, newTestDP(t, service, "token-1"),
			&push.Notification{Data: map[string]string{"msg": "x"}})
	}

	mu.Lock()
	defer mu.Unlock()
	if factoryCalls != 1 {
		t.Errorf("Expected the client to be built once and reused, but the factory ran %d times", factoryCalls)
	}
}

func TestPushWithoutCredentialsReportsBadProvider(t *testing.T) {
	service := NewPushService("fcm").(*pushService)
	service.OverrideClientFactory(func(*push.PushServiceProvider) (HTTPClient, error) {
		return nil, fmt.Errorf("could not read credentialsfile")
	})
	defer service.Finalize()

	psp := newTestPSP(t, service)
	result := pushOnce(t, service, psp, newTestDP(t, service, "token-1"),
		&push.Notification{Data: map[string]string{"msg": "x"}})
	if _, ok := result.Err.(*push.BadPushServiceProvider); !ok {
		t.Errorf("Expected BadPushServiceProvider, got %T: %v", result.Err, result.Err)
	}
}

// TestPushDrainsQueueOnProviderError makes sure a provider-level failure cannot
// wedge the caller, which is blocked writing to dpQueue.
func TestPushDrainsQueueOnProviderError(t *testing.T) {
	service := NewPushService("fcm").(*pushService)
	service.OverrideClientFactory(func(*push.PushServiceProvider) (HTTPClient, error) {
		return nil, fmt.Errorf("no credentials")
	})
	defer service.Finalize()
	psp := newTestPSP(t, service)

	dpQueue := make(chan *push.DeliveryPoint)
	resQueue := make(chan *push.Result, 8)
	wg := new(sync.WaitGroup)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 3; i++ {
			dpQueue <- newTestDP(t, service, "token-1")
		}
		close(dpQueue)
	}()

	service.Push(psp, dpQueue, resQueue, &push.Notification{Data: map[string]string{"msg": "x"}})
	wg.Wait() // would deadlock if Push stopped reading dpQueue

	count := 0
	for range resQueue {
		count++
	}
	if count != 1 {
		t.Errorf("Expected a single provider-level error, got %d results", count)
	}
}

func TestPreviewShowsTheV1Message(t *testing.T) {
	service := NewPushService("fcm")
	payload, err := service.Preview(&push.Notification{Data: map[string]string{"msg": "hello"}})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	var envelope map[string]interface{}
	if e := json.Unmarshal(payload, &envelope); e != nil {
		t.Fatalf("Preview is not valid JSON: %v", e)
	}
	message, ok := envelope["message"].(map[string]interface{})
	if !ok {
		t.Fatalf("Expected a message envelope, got %s", payload)
	}
	if message["token"] != "placeholderRegId" {
		t.Errorf("Expected the placeholder token, got %v", message["token"])
	}
}

func TestServiceNames(t *testing.T) {
	for _, name := range []string{"fcm", "gcm"} {
		if got := NewPushService(name).Name(); got != name {
			t.Errorf("Expected name %q, got %q", name, got)
		}
	}
}

// TestGCMAliasPushes checks the alias is a working backend rather than just a
// registered name. An existing gcm subscription has to keep pushing after the
// upgrade, since that is the entire reason the name was kept.
func TestGCMAliasPushes(t *testing.T) {
	var captured *http.Request
	var message map[string]interface{}
	service := newTestService(t, "gcm", func(r *http.Request) (*http.Response, error) {
		captured = r
		message = decodeSentMessage(t, r)
		return newResponse(200, `{"name":"projects/my-project/messages/0:1"}`, nil), nil
	})
	defer service.Finalize()

	psp := newTestPSP(t, service)
	dp := newTestDP(t, service, "legacy-gcm-token")
	result := pushOnce(t, service, psp, dp, &push.Notification{Data: map[string]string{"msg": "hi"}})

	if result.Err != nil {
		t.Fatalf("A gcm push should work exactly like fcm, got: %v", result.Err)
	}
	// Same v1 endpoint: gcm is an alias, not a second implementation.
	if got := captured.URL.String(); got != "https://fcm.googleapis.com/v1/projects/my-project/messages:send" {
		t.Errorf("Expected the v1 endpoint, got %s", got)
	}
	if message["token"] != "legacy-gcm-token" {
		t.Errorf("Expected the registration token in the message, got %v", message["token"])
	}
	// The raw-payload key is per-name, so a gcm caller keeps using
	// uniqush.payload.gcm rather than having to switch.
	if got := service.rawPayloadKey(); got != "uniqush.payload.gcm" {
		t.Errorf("Expected the gcm payload key to be preserved, got %q", got)
	}
}
