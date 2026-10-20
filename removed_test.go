package main

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/uniqush/uniqush-push/conf"
	"github.com/uniqush/uniqush-push/db"
	"github.com/uniqush/uniqush-push/log"
	"github.com/uniqush/uniqush-push/push"
	"github.com/uniqush/uniqush-push/srv"
	"github.com/uniqush/uniqush-push/srv/apns/apnstest"
	"github.com/uniqush/uniqush-push/srv/apns/common"
	"github.com/uniqush/uniqush-push/srv/webpush"
)

// Tests that a device a push service reports gone is reported to the caller,
// in the /push response and in /removed, pushed through the real backend
// against the vendor simulators and a real redis.

const removalSubscriber = "removal_subscriber"

var installWebPushOnce sync.Once

// setUpRemovalTest returns a backend and REST API over a freshly cleared redis.
func setUpRemovalTest(t *testing.T) (*PushBackEnd, *RestAPI) {
	t.Helper()
	installAPNSOnce.Do(srv.InstallAPNS)
	installWebPushOnce.Do(srv.InstallWebPush)
	psm := push.GetPushServiceManager()

	// The simulators listen on loopback, which both backends refuse by default.
	path := filepath.Join(t.TempDir(), "uniqush.conf")
	if err := os.WriteFile(path, []byte("[webpush]\nallow_private_addresses=true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	config, err := conf.ReadConfigFile(path)
	if err != nil {
		t.Fatal(err)
	}
	psm.SetConfigFile(config)
	common.SetAllowNonAppleEndpoints(true)
	t.Cleanup(func() { common.SetAllowNonAppleEndpoints(false) })

	backend := NewPushBackEnd(psm, connectAcceptanceDatabase(t, psm), silentLoggers())
	return backend, NewRestAPI(psm, silentLoggers(), "test", backend)
}

// webPushServer answers each push with the next of statuses, repeating the last.
func webPushServer(t *testing.T, statuses ...int) *httptest.Server {
	t.Helper()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		n := int(requests.Add(1)) - 1
		// Without it a retry would be a minute out.
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(statuses[min(n, len(statuses)-1)])
	}))
	t.Cleanup(server.Close)
	return server
}

func webPushFixture(t *testing.T, service, endpoint string) (pspKV, dpKV map[string]string) {
	t.Helper()
	privateKey, publicKey, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		t.Fatal(err)
	}
	key, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pspKV = map[string]string{
		"service": service, "pushservicetype": "webpush",
		"vapidpublickey": publicKey, "vapidprivatekey": privateKey, "subscriber": "admin@example.org",
	}
	dpKV = map[string]string{
		"service": service, "subscriber": removalSubscriber, "pushservicetype": "webpush",
		"endpoint": endpoint,
		"p256dh":   base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes()),
		"auth":     base64.RawURLEncoding.EncodeToString([]byte("0123456789abcdef")),
		"devid":    "device-1",
	}
	return pspKV, dpKV
}

func addRemovalProvider(t *testing.T, backend *PushBackEnd, pspKV map[string]string) *push.PushServiceProvider {
	t.Helper()
	psp, err := push.GetPushServiceManager().BuildPushServiceProviderFromMap(pspKV)
	if err != nil {
		t.Fatalf("Could not build the provider: %v", err)
	}
	if err = backend.AddPushServiceProvider(pspKV["service"], psp, false); err != nil {
		t.Fatalf("Could not add the provider: %v", err)
	}
	return psp
}

func subscribeForRemoval(t *testing.T, backend *PushBackEnd, pspKV, dpKV map[string]string) *push.DeliveryPoint {
	t.Helper()
	addRemovalProvider(t, backend, pspKV)
	dp, err := push.GetPushServiceManager().BuildDeliveryPointFromMap(dpKV)
	if err != nil {
		t.Fatalf("Could not build the delivery point: %v", err)
	}
	if _, err = backend.Subscribe(dpKV["service"], removalSubscriber, dp); err != nil {
		t.Fatalf("Could not subscribe: %v", err)
	}
	return dp
}

func pushForRemoval(t *testing.T, backend *PushBackEnd, service string) (APIPushResponse, string) {
	t.Helper()
	handler := newPushResponseHandler(log.NewLogger(io.Discard, "[test]", log.LevelSilent))
	backend.Push("removal-request", "127.0.0.1", service, []string{removalSubscriber}, nil,
		&push.Notification{Data: map[string]string{"msg": "hello"}}, nil,
		log.NewLogger(io.Discard, "[test]", log.LevelSilent), handler)
	body := handler.ToJSON()
	var response APIPushResponse
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("Could not read the push response %s: %v", body, err)
	}
	return response, string(body)
}

type removedResponse struct {
	Code      string                    `json:"code"`
	ErrorMsg  *string                   `json:"errorMsg"`
	Removed   []db.RemovedDeliveryPoint `json:"removed"`
	Next      string                    `json:"next"`
	Truncated bool                      `json:"truncated"`
}

func getRemoved(t *testing.T, api *RestAPI, query string) removedResponse {
	t.Helper()
	recorder := httptest.NewRecorder()
	api.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, QueryRemovedURL+"?"+query, nil))
	var response removedResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("Could not read the /removed response %q: %v", recorder.Body.String(), err)
	}
	if response.Code != UNIQUSH_SUCCESS {
		t.Fatalf("/removed answered %s: %v", response.Code, stringOrEmpty(response.ErrorMsg))
	}
	return response
}

// expectRemoved checks that got describes dp, with its devid and address.
func expectRemoved(t *testing.T, got db.RemovedDeliveryPoint, dp *push.DeliveryPoint, address string) {
	t.Helper()
	want := db.RemovedDeliveryPoint{
		ID: got.ID, Time: got.Time,
		Service: dp.FixedData["service"], Subscriber: removalSubscriber,
		PushServiceType: dp.PushServiceName(), DeliveryPoint: dp.Name(), DevID: "device-1",
		Endpoint: dp.FixedData["endpoint"], DevToken: dp.FixedData["devtoken"], RegID: dp.FixedData["regid"],
		Reason: UNIQUSH_UPDATE_UNSUBSCRIBE,
	}
	if got != want {
		t.Errorf("Expected the removal\n%+v\ngot\n%+v", want, got)
	}
	if got.Endpoint+got.DevToken+got.RegID != address {
		t.Errorf("Expected the removal to name %q, got %+v", address, got)
	}
}

func TestPushReportsRemovedDeliveryPoints(t *testing.T) {
	cases := []struct {
		name string
		// inResponse is whether the removal makes it into the /push response.
		// APNs reports success when it has sent the request and reads Apple's
		// answer afterwards, so its removals only ever reach /removed.
		inResponse bool
		// setUp subscribes a device its push service will report gone, and
		// returns it with the address the push service was given for it.
		setUp func(t *testing.T, backend *PushBackEnd, service string) (*push.DeliveryPoint, string)
	}{
		{"webpush 410", true, func(t *testing.T, backend *PushBackEnd, service string) (*push.DeliveryPoint, string) {
			endpoint := webPushServer(t, http.StatusGone).URL + "/sub/1"
			pspKV, dpKV := webPushFixture(t, service, endpoint)
			return subscribeForRemoval(t, backend, pspKV, dpKV), endpoint
		}},
		{"apns Unregistered", false, func(t *testing.T, backend *PushBackEnd, service string) (*push.DeliveryPoint, string) {
			server := apnstest.NewServer()
			t.Cleanup(server.Close)
			caCert, err := server.WriteCACert(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			token := apnstest.DeviceToken(1)
			server.SetResponse(token, apnstest.Response{Status: http.StatusGone, Reason: "Unregistered", Timestamp: time.Now()})
			return subscribeForRemoval(t, backend, map[string]string{
				"service": service, "pushservicetype": "apns", "bundleid": acceptanceBundleID,
				"cert": "srv/apns/apns-test/localhost.cert", "key": "srv/apns/apns-test/localhost.key",
				common.EndpointKey: server.URL(), common.CACertKey: caCert,
			}, map[string]string{
				"service": service, "subscriber": removalSubscriber, "pushservicetype": "apns",
				"devtoken": token, "devid": "device-1",
			}), token
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			backend, api := setUpRemovalTest(t)
			service := "removal_" + strings.ReplaceAll(c.name, " ", "_")
			dp, address := c.setUp(t, backend, service)

			response, body := pushForRemoval(t, backend, service)
			if !c.inResponse {
				if response.DroppedCount != 0 {
					t.Fatalf("Expected no removal in the push response, got %s", body)
				}
			} else {
				if len(response.DroppedDetails) != 1 || response.DroppedDetails[0].Removed == nil {
					t.Fatalf("Expected one removal in the push response, got %s", body)
				}
				expectRemoved(t, *response.DroppedDetails[0].Removed, dp, address)
				if strings.Contains(body, dp.FixedData["auth"]) || strings.Contains(body, dp.FixedData["p256dh"]) {
					t.Errorf("The push response carries the subscription's keys: %s", body)
				}
			}

			removed := waitForRemoved(t, api, service, 5*time.Second)
			entry := removed.Removed[0]
			expectRemoved(t, entry, dp, address)
			if entry.ID == "" || removed.Next != entry.ID || removed.Truncated {
				t.Errorf("Expected the cursor to be the entry's id and nothing truncated, got %+v", removed)
			}
			if now := time.Now().Unix(); entry.Time < now-60 || entry.Time > now {
				t.Errorf("Expected the entry to be dated now, got %d", entry.Time)
			}
			if after := getRemoved(t, api, "service="+service+"&since="+removed.Next); len(after.Removed) != 0 || after.Next != removed.Next {
				t.Errorf("Expected nothing after the cursor and the cursor back, got %+v", after)
			}
			if n := backend.NumberOfDeliveryPoints(service, removalSubscriber, silentLoggers()[0]); n != 0 {
				t.Errorf("Expected the delivery point to be gone, %d remain", n)
			}
		})
	}
}

// A removal made by a retry happens after /push has answered, so /removed is
// the only place it can be reported.
func TestRemovalDuringRetryIsRecorded(t *testing.T) {
	backend, api := setUpRemovalTest(t)
	const service = "removal_retry"
	endpoint := webPushServer(t, http.StatusTooManyRequests, http.StatusGone).URL + "/sub/1"
	pspKV, dpKV := webPushFixture(t, service, endpoint)
	dp := subscribeForRemoval(t, backend, pspKV, dpKV)

	if response, body := pushForRemoval(t, backend, service); response.DroppedCount != 0 {
		t.Fatalf("Expected the first attempt to be retried rather than removed, got %s", body)
	}

	// The first retry is 5 seconds out.
	removed := waitForRemoved(t, api, service, 15*time.Second)
	expectRemoved(t, removed.Removed[0], dp, endpoint)
}

// waitForRemoved polls /removed until the service has one entry.
func waitForRemoved(t *testing.T, api *RestAPI, service string, timeout time.Duration) removedResponse {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		removed := getRemoved(t, api, "service="+service)
		if len(removed.Removed) == 1 {
			return removed
		}
		if time.Now().After(deadline) {
			t.Fatalf("Expected one entry in /removed, got %+v", removed)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// A caller pushing by delivery_point_id passes the ids /subscribe gave it, and
// one it holds for a device uniqush has since removed is reported, not skipped.
func TestPushByDeliveryPointIDReportsTheOnesNotSubscribed(t *testing.T) {
	backend, api := setUpRemovalTest(t)
	const service = "removal_by_id"
	pspKV, dpKV := webPushFixture(t, service, webPushServer(t, http.StatusCreated).URL+"/sub/1")
	psp := addRemovalProvider(t, backend, pspKV)

	post := func(path string, form url.Values) []byte {
		t.Helper()
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		recorder := httptest.NewRecorder()
		api.ServeHTTP(recorder, request)
		return recorder.Body.Bytes()
	}
	subscribeForm := url.Values{}
	for k, v := range dpKV {
		subscribeForm.Set(k, v)
	}
	var subscribed APISimpleResponse
	if err := json.Unmarshal(post(AddDeliveryPointToServiceURL, subscribeForm), &subscribed); err != nil || subscribed.Details.DeliveryPoint == nil {
		t.Fatalf("Could not subscribe: %+v, %v", subscribed, err)
	}
	id := *subscribed.Details.DeliveryPoint
	const gone = "webpush:0000000000000000000000000000000000000000"

	pushByID := func() (APIPushResponse, []byte) {
		t.Helper()
		body := post(PushNotificationURL, url.Values{"service": {service}, "subscriber": {removalSubscriber},
			"msg": {"hello"}, "delivery_point_id": {id + "," + gone}})
		var response APIPushResponse
		if err := json.Unmarshal(body, &response); err != nil {
			t.Fatalf("Could not read the push response %s: %v", body, err)
		}
		return response, body
	}

	response, body := pushByID()
	if response.SuccessCount != 1 || *response.SuccessDetails[0].DeliveryPoint != id {
		t.Errorf("Expected the push to reach %s, got %s", id, body)
	}
	if response.FailureCount != 0 || response.DroppedCount != 1 ||
		response.DroppedDetails[0].Code != UNIQUSH_NOT_SUBSCRIBED || *response.DroppedDetails[0].DeliveryPoint != gone {
		t.Errorf("Expected %s to be reported not subscribed, got %s", gone, body)
	}

	// A device whose provider is gone is still subscribed, and comes back when
	// the provider does, so it must not be reported as not subscribed.
	if err := backend.RemovePushServiceProvider(service, psp); err != nil {
		t.Fatal(err)
	}
	response, body = pushByID()
	if response.FailureCount != 1 || response.FailureDetails[0].Code != UNIQUSH_ERROR_NO_PUSH_SERVICE_PROVIDER ||
		*response.FailureDetails[0].DeliveryPoint != id || response.DroppedCount != 1 {
		t.Errorf("Expected %s to fail for want of a provider and only %s to be not subscribed, got %s", id, gone, body)
	}
}

// A delivery_point_id is matched only within the named subscribers, so naming
// another subscriber's device neither reaches it nor reports it as theirs.
func TestPushByDeliveryPointIDStaysWithinTheNamedSubscribers(t *testing.T) {
	backend, _ := setUpRemovalTest(t)
	const service = "removal_other_subscriber"
	var mutex sync.Mutex
	pushed := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		mutex.Lock()
		pushed[r.URL.Path]++
		mutex.Unlock()
		w.WriteHeader(http.StatusCreated)
	}))
	t.Cleanup(server.Close)

	pspKV, aliceKV := webPushFixture(t, service, server.URL+"/alice")
	addRemovalProvider(t, backend, pspKV)
	_, bobKV := webPushFixture(t, service, server.URL+"/bob")
	bobKV["subscriber"] = "bob"
	var dps []*push.DeliveryPoint
	for _, kv := range []map[string]string{aliceKV, bobKV} {
		dp, err := push.GetPushServiceManager().BuildDeliveryPointFromMap(kv)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = backend.Subscribe(service, kv["subscriber"], dp); err != nil {
			t.Fatal(err)
		}
		dps = append(dps, dp)
	}
	bobDP := dps[1]

	handler := newPushResponseHandler(log.NewLogger(io.Discard, "[test]", log.LevelSilent))
	backend.Push("removal-request", "127.0.0.1", service, []string{removalSubscriber}, []string{bobDP.Name()},
		&push.Notification{Data: map[string]string{"msg": "hello"}}, nil,
		log.NewLogger(io.Discard, "[test]", log.LevelSilent), handler)
	body := handler.ToJSON()
	var response APIPushResponse
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatal(err)
	}

	if response.SuccessCount != 0 || response.DroppedCount != 1 ||
		response.DroppedDetails[0].Code != UNIQUSH_NOT_SUBSCRIBED || *response.DroppedDetails[0].DeliveryPoint != bobDP.Name() {
		t.Errorf("Expected bob's id to be reported not subscribed, got %s", body)
	}
	mutex.Lock()
	defer mutex.Unlock()
	if len(pushed) != 0 {
		t.Errorf("Expected nothing to be pushed, the push server saw %v", pushed)
	}
}
