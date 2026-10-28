package main

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
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
	"github.com/uniqush/uniqush-push/srv/fcm"
	"github.com/uniqush/uniqush-push/srv/webpush"
)

// Tests that a device a push service reports gone is reported to the caller,
// in the /push response and in /removed, pushed through the real backend
// against the vendor simulators and a real redis.

const removalSubscriber = "removal_subscriber"

var installWebPushOnce sync.Once

// FCM's existing client hook keeps the real send and response handling paths,
// without minting a token or sending a device registration to Google.
type removalFCMClient struct{}

func (removalFCMClient) Do(request *http.Request) (*http.Response, error) {
	request.Body.Close()
	return &http.Response{
		StatusCode: http.StatusForbidden,
		Body:       io.NopCloser(strings.NewReader(`{"error":{"status":"PERMISSION_DENIED","message":"Sender does not match","details":[{"@type":"type.googleapis.com/google.firebase.fcm.v1.FcmError","errorCode":"SENDER_ID_MISMATCH"}]}}`)),
	}, nil
}

func setUpFCMRemovalTest(t *testing.T) (*PushBackEnd, *RestAPI) {
	t.Helper()
	psm := new(push.PushServiceManager)
	psm.ClearAllPushServiceTypesForUnitTest()
	service := fcm.NewPushService("fcm")
	service.(interface {
		OverrideClientFactory(func(*push.PushServiceProvider) (fcm.HTTPClient, error))
	}).OverrideClientFactory(func(*push.PushServiceProvider) (fcm.HTTPClient, error) {
		return removalFCMClient{}, nil
	})
	if err := psm.RegisterPushServiceType(service); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Finalize)
	backend := NewPushBackEnd(psm, connectAcceptanceDatabase(t, psm), silentLoggers())
	return backend, NewRestAPI(psm, silentLoggers(), "test", backend)
}

func fcmRemovalCredentials(t *testing.T) string {
	t.Helper()
	// Reuse the repository's public test key; parsing credentials is offline.
	key, err := os.ReadFile("srv/apns/apns-test/localhost.key")
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]string{
		"type": "service_account", "project_id": "removal-project",
		"private_key": string(key), "client_email": "test@removal-project.iam.gserviceaccount.com",
		"token_uri": "https://oauth2.googleapis.com/token",
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "service-account.json")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

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
	t.Cleanup(func() { psm.SetConfigFile(nil) })
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
	psp, err := backend.psm.BuildPushServiceProviderFromMap(pspKV)
	if err != nil {
		t.Fatalf("Could not build the provider: %v", err)
	}
	if err = backend.AddPushServiceProvider(pspKV["service"], psp, false); err != nil {
		t.Fatalf("Could not add the provider: %v", err)
	}
	return psp
}

// subscribeForRemoval also adds the provider, unless pspKV is nil.
func subscribeForRemoval(t *testing.T, backend *PushBackEnd, pspKV, dpKV map[string]string) *push.DeliveryPoint {
	t.Helper()
	if pspKV != nil {
		addRemovalProvider(t, backend, pspKV)
	}
	dp, err := backend.psm.BuildDeliveryPointFromMap(dpKV)
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
		name       string
		newBackend func(*testing.T) (*PushBackEnd, *RestAPI)
		// setUp subscribes a device its push service will report gone, and
		// returns it with the address the push service was given for it.
		setUp func(t *testing.T, backend *PushBackEnd, service string) (*push.DeliveryPoint, string)
	}{
		{"webpush 410", nil, func(t *testing.T, backend *PushBackEnd, service string) (*push.DeliveryPoint, string) {
			endpoint := webPushServer(t, http.StatusGone).URL + "/sub/1"
			pspKV, dpKV := webPushFixture(t, service, endpoint)
			return subscribeForRemoval(t, backend, pspKV, dpKV), endpoint
		}},
		{"apns Unregistered", nil, func(t *testing.T, backend *PushBackEnd, service string) (*push.DeliveryPoint, string) {
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
		{"fcm SENDER_ID_MISMATCH", setUpFCMRemovalTest, func(t *testing.T, backend *PushBackEnd, service string) (*push.DeliveryPoint, string) {
			const token = "registration-token"
			return subscribeForRemoval(t, backend, map[string]string{
				"service": service, "pushservicetype": "fcm", "projectid": "removal-project",
				"credentialsfile": fcmRemovalCredentials(t),
			}, map[string]string{
				"service": service, "subscriber": removalSubscriber, "pushservicetype": "fcm",
				"regid": token, "devid": "device-1",
			}), token
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			newBackend := c.newBackend
			if newBackend == nil {
				newBackend = setUpRemovalTest
			}
			backend, api := newBackend(t)
			service := "removal_" + strings.ReplaceAll(c.name, " ", "_")
			dp, address := c.setUp(t, backend, service)

			response, body := pushForRemoval(t, backend, service)
			if response.DroppedCount != 1 || response.SuccessCount != 0 || response.FailureCount != 0 ||
				len(response.DroppedDetails) != 1 || response.DroppedDetails[0].Removed == nil {
				t.Fatalf("Expected one removal and no success or failure in the push response, got %s", body)
			}
			expectRemoved(t, *response.DroppedDetails[0].Removed, dp, address)
			for _, key := range []string{"auth", "p256dh"} {
				if secret := dp.FixedData[key]; secret != "" && strings.Contains(body, secret) {
					t.Errorf("The push response carries the subscription's keys: %s", body)
				}
			}
			metrics := getMetrics(t, api)
			labels := `service="` + service + `",push_service_type="` + dp.PushServiceName() + `",result="`
			if !strings.Contains(metrics, `uniqush_push_results_total{`+labels+`device_removed"} 1`+"\n") ||
				strings.Contains(metrics, `uniqush_push_results_total{`+labels+`success"}`) {
				t.Errorf("Expected the device to count only as removed, got:\n%s", metrics)
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
// the only place it can be reported; the response says the device is retrying.
func TestRemovalDuringRetryIsRecorded(t *testing.T) {
	backend, api := setUpRemovalTest(t)
	const service = "removal_retry"
	endpoint := webPushServer(t, http.StatusServiceUnavailable, http.StatusGone).URL + "/sub/1"
	pspKV, dpKV := webPushFixture(t, service, endpoint)
	dp := subscribeForRemoval(t, backend, pspKV, dpKV)

	response, body := pushForRemoval(t, backend, service)
	if response.DroppedCount != 0 || response.FailureCount != 1 || len(response.FailureDetails) != 1 {
		t.Fatalf("Expected the first attempt to be reported retrying, got %s", body)
	}
	got := response.FailureDetails[0]
	if got.Code != UNIQUSH_PUSH_RETRYING || stringOrEmpty(got.Subscriber) != removalSubscriber ||
		stringOrEmpty(got.DeliveryPoint) != dp.Name() || !strings.Contains(stringOrEmpty(got.ErrorMsg), "HTTP 503") {
		t.Errorf("Expected a retrying entry naming the device and why, got %s", body)
	}

	// The first retry is 5 seconds out.
	removed := waitForRemoved(t, api, service, 15*time.Second)
	expectRemoved(t, removed.Removed[0], dp, endpoint)
}

// A retry that finishes while the response still waits on a slower device
// stays out of it, so each device is reported once.
func TestRetryOutcomeStaysOutOfTheResponse(t *testing.T) {
	backend, _ := setUpRemovalTest(t)
	const service = "retry_outcome"
	retried := webPushServer(t, http.StatusServiceUnavailable, http.StatusCreated)
	// Slower than the first retry, which is 5 seconds out.
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		time.Sleep(7 * time.Second)
		w.WriteHeader(http.StatusCreated)
	}))
	t.Cleanup(slow.Close)
	pspKV, dpKV := webPushFixture(t, service, retried.URL+"/sub/1")
	subscribeForRemoval(t, backend, pspKV, dpKV)
	_, dpKV = webPushFixture(t, service, slow.URL+"/sub/2")
	dpKV["devid"] = "device-2"
	subscribeForRemoval(t, backend, nil, dpKV)

	response, body := pushForRemoval(t, backend, service)
	if response.SuccessCount != 1 || response.FailureCount != 1 ||
		response.FailureDetails[0].Code != UNIQUSH_PUSH_RETRYING {
		t.Errorf("Expected one success and one device retrying, got %s", body)
	}
}

// A provider stored before /addpsp checked the VAPID contact can still be
// removed, though /addpsp would now refuse it.
func TestRemovePSPWithAContactAddPSPRefuses(t *testing.T) {
	backend, api := setUpRemovalTest(t)
	pspKV, _ := webPushFixture(t, "old_contact", "https://push.example.org/sub/1")
	pspKV["subscriber"] = "admin"
	addRemovalProvider(t, backend, pspKV)

	post := func(path string) APISimpleResponse {
		form := url.Values{}
		for k, v := range pspKV {
			form.Set(k, v)
		}
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		recorder := httptest.NewRecorder()
		api.ServeHTTP(recorder, request)
		var response APISimpleResponse
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatalf("Could not read the %s response %q: %v", path, recorder.Body.String(), err)
		}
		return response
	}
	if response := post(AddPushServiceProviderToServiceURL); response.Details.Code != UNIQUSH_ERROR_BUILD_PUSH_SERVICE_PROVIDER {
		t.Errorf("Expected /addpsp to refuse the contact, got %+v", response)
	}
	if response := post(RemovePushServiceProviderFromServiceURL); response.Details.Code != UNIQUSH_SUCCESS {
		t.Fatalf("Expected /rmpsp to remove the provider, got %+v", response)
	}
	psps, err := backend.GetPushServiceProviderConfigs()
	if err != nil || len(psps) != 0 {
		t.Errorf("Expected no providers left, got %v, %v", psps, err)
	}
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

// delivery_point_id picks the devices, and must not reach them in the payload.
func TestPushByDeliveryPointIDLeavesItOutOfThePayload(t *testing.T) {
	cases := []struct {
		name string
		// setUp subscribes a device and returns a function waiting for the
		// payload its push service is sent.
		setUp func(t *testing.T, backend *PushBackEnd, service string) (*push.DeliveryPoint, func() []byte)
	}{
		{"apns", func(t *testing.T, backend *PushBackEnd, service string) (*push.DeliveryPoint, func() []byte) {
			server := apnstest.NewServer()
			t.Cleanup(server.Close)
			caCert, err := server.WriteCACert(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			dp := subscribeForRemoval(t, backend, map[string]string{
				"service": service, "pushservicetype": "apns", "bundleid": acceptanceBundleID,
				"cert": "srv/apns/apns-test/localhost.cert", "key": "srv/apns/apns-test/localhost.key",
				common.EndpointKey: server.URL(), common.CACertKey: caCert,
			}, map[string]string{
				"service": service, "subscriber": removalSubscriber, "pushservicetype": "apns",
				"devtoken": apnstest.DeviceToken(1),
			})
			return dp, func() []byte {
				deadline := time.Now().Add(5 * time.Second)
				for len(server.Requests()) == 0 && time.Now().Before(deadline) {
					time.Sleep(20 * time.Millisecond)
				}
				if len(server.Requests()) != 1 {
					t.Fatalf("Expected one request to the simulator, got %d", len(server.Requests()))
				}
				return server.Requests()[0].Payload
			}
		}},
		{"webpush", func(t *testing.T, backend *PushBackEnd, service string) (*push.DeliveryPoint, func() []byte) {
			bodies := make(chan []byte, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				bodies <- body
				w.WriteHeader(http.StatusCreated)
			}))
			t.Cleanup(server.Close)
			key, err := ecdh.P256().GenerateKey(rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			pspKV, dpKV := webPushFixture(t, service, server.URL+"/sub/1")
			dpKV["p256dh"] = base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes())
			auth, _ := base64.RawURLEncoding.DecodeString(dpKV["auth"])
			return subscribeForRemoval(t, backend, pspKV, dpKV), func() []byte {
				return decryptWebPush(t, key, auth, <-bodies)
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			backend, api := setUpRemovalTest(t)
			service := "payload_by_id_" + c.name
			dp, payload := c.setUp(t, backend, service)

			form := url.Values{"service": {service}, "subscriber": {removalSubscriber},
				"msg": {"hello"}, "delivery_point_id": {dp.Name()}}
			request := httptest.NewRequest(http.MethodPost, PushNotificationURL, strings.NewReader(form.Encode()))
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			recorder := httptest.NewRecorder()
			api.ServeHTTP(recorder, request)
			var response APIPushResponse
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil || response.SuccessCount != 1 {
				t.Fatalf("Expected the push to succeed, got %s", recorder.Body.String())
			}

			got := string(payload())
			if !strings.Contains(got, "hello") || strings.Contains(got, "delivery_point_id") || strings.Contains(got, dp.Name()) {
				t.Errorf("Expected the message without its delivery_point_id, got %s", got)
			}
		})
	}
}

// decryptWebPush reads a single-record aes128gcm body, per RFC 8291.
func decryptWebPush(t *testing.T, key *ecdh.PrivateKey, auth, body []byte) []byte {
	t.Helper()
	expand := func(secret, salt []byte, info string, length int) []byte {
		prk, err := hkdf.Extract(sha256.New, secret, salt)
		if err != nil {
			t.Fatal(err)
		}
		out, err := hkdf.Expand(sha256.New, prk, info, length)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	salt, idLen := body[:16], int(body[20])
	keyID, record := body[21:21+idLen], body[21+idLen:]
	serverKey, err := ecdh.P256().NewPublicKey(keyID)
	if err != nil {
		t.Fatal(err)
	}
	shared, err := key.ECDH(serverKey)
	if err != nil {
		t.Fatal(err)
	}
	ikm := expand(shared, auth, "WebPush: info\x00"+string(key.PublicKey().Bytes())+string(keyID), 32)
	block, err := aes.NewCipher(expand(ikm, salt, "Content-Encoding: aes128gcm\x00", 16))
	if err != nil {
		t.Fatal(err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	padded, err := aead.Open(nil, expand(ikm, salt, "Content-Encoding: nonce\x00", 12), record, nil)
	if err != nil {
		t.Fatal(err)
	}
	// The last record ends with a 2 delimiter and zero padding.
	return padded[:bytes.LastIndexByte(padded, 2)]
}

// Only a removal that happened is reported as one: not one whose record had
// already gone, nor one whose device re-subscribed after the push read it.
func TestOnlyActualRemovalsAreReported(t *testing.T) {
	cases := []struct {
		name string
		// meanwhile is what happens to the subscription between the push
		// reading it and the push service's answer.
		meanwhile   func(t *testing.T, backend *PushBackEnd, dpKV map[string]string, dp *push.DeliveryPoint)
		wantRemoved bool
		wantLeft    int // devices the subscriber has afterwards
	}{
		{"unchanged", func(*testing.T, *PushBackEnd, map[string]string, *push.DeliveryPoint) {}, true, 0},
		{"already removed", func(t *testing.T, backend *PushBackEnd, _ map[string]string, dp *push.DeliveryPoint) {
			if err := backend.Unsubscribe(dp.FixedData["service"], removalSubscriber, dp); err != nil {
				t.Fatal(err)
			}
		}, false, 0},
		{"re-subscribed with a new devid", func(t *testing.T, backend *PushBackEnd, dpKV map[string]string, _ *push.DeliveryPoint) {
			dpKV["devid"] = "device-2"
			subscribeForRemoval(t, backend, nil, dpKV)
		}, false, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			backend, api := setUpRemovalTest(t)
			service := "removal_" + strings.ReplaceAll(c.name, " ", "_")
			pspKV, dpKV := webPushFixture(t, service, "https://push.example.org/sub/1")
			psp := addRemovalProvider(t, backend, pspKV)
			dp := subscribeForRemoval(t, backend, nil, dpKV)
			c.meanwhile(t, backend, dpKV, dp)

			handler := &countingHandler{}
			backend.fixUnsubscribeUpdate(push.NewUnsubscribeUpdate(psp, dp), "removal-request", "127.0.0.1", silentLoggers()[0], handler)

			if len(handler.details) != 1 {
				t.Fatalf("Expected one result, got %+v", handler.details)
			}
			detail := handler.details[0]
			if (detail.Removed != nil) != c.wantRemoved || (detail.ErrorMsg == nil) != c.wantRemoved {
				t.Errorf("Expected removed=%v with an errorMsg only if not, got %+v", c.wantRemoved, detail)
			}
			wantLogged := 0
			if c.wantRemoved {
				wantLogged = 1
			}
			if logged := getRemoved(t, api, "service="+service); len(logged.Removed) != wantLogged {
				t.Errorf("Expected %d entries in /removed, got %+v", wantLogged, logged)
			}
			if left := backend.NumberOfDeliveryPoints(service, removalSubscriber, silentLoggers()[0]); left != c.wantLeft {
				t.Errorf("Expected %d devices left, got %d", c.wantLeft, left)
			}
		})
	}
}

// failingLookupDatabase cannot read one subscriber's devices.
type failingLookupDatabase struct {
	db.PushDatabase
	failFor string
}

func (d *failingLookupDatabase) GetPushServiceProviderDeliveryPointPairs(service, subscriber string, names []string,
	requestID string, logger log.Logger) ([]db.PushServiceProviderDeliveryPointPair, error) {
	if subscriber == d.failFor {
		return nil, errors.New("i/o timeout")
	}
	return d.PushDatabase.GetPushServiceProviderDeliveryPointPairs(service, subscriber, names, requestID, logger)
}

// When one subscriber's devices cannot be read, none of the requested ids is
// reported not subscribed: the ones not found may belong to that subscriber.
func TestFailedLookupReportsNothingNotSubscribed(t *testing.T) {
	backend, _ := setUpRemovalTest(t)
	const service = "removal_failed_lookup"
	pspKV, dpKV := webPushFixture(t, service, webPushServer(t, http.StatusCreated).URL+"/sub/1")
	dp := subscribeForRemoval(t, backend, pspKV, dpKV)
	backend.db = &failingLookupDatabase{PushDatabase: backend.db, failFor: "bob"}

	handler := newPushResponseHandler(silentLoggers()[0])
	backend.Push("removal-request", "127.0.0.1", service, []string{removalSubscriber, "bob"},
		[]string{dp.Name(), "webpush:0000000000000000000000000000000000000000"},
		&push.Notification{Data: map[string]string{"msg": "hello"}}, nil, silentLoggers()[0], handler)
	body := handler.ToJSON()
	var response APIPushResponse
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatal(err)
	}
	if response.SuccessCount != 1 || response.DroppedCount != 0 || response.FailureCount != 1 ||
		response.FailureDetails[0].Code != UNIQUSH_ERROR_DATABASE {
		t.Errorf("Expected alice pushed, bob's lookup failed and nothing reported not subscribed, got %s", body)
	}
}

func TestRemovedPagesWithLimit(t *testing.T) {
	backend, api := setUpRemovalTest(t)
	const service = "removal_paging"
	for _, sub := range []string{"a", "b", "c"} {
		if err := backend.db.RecordRemovedDeliveryPoint(db.RemovedDeliveryPoint{Service: service, Subscriber: sub}); err != nil {
			t.Fatal(err)
		}
	}

	var subscribers []string
	page := getRemoved(t, api, "service="+service+"&limit=2")
	for _, entry := range page.Removed {
		subscribers = append(subscribers, entry.Subscriber)
	}
	if len(page.Removed) != 2 || page.Next != page.Removed[1].ID {
		t.Fatalf("Expected a first page of two ending at next, got %+v", page)
	}
	page = getRemoved(t, api, "service="+service+"&limit=2&since="+page.Next)
	for _, entry := range page.Removed {
		subscribers = append(subscribers, entry.Subscriber)
	}
	if strings.Join(subscribers, ",") != "a,b,c" {
		t.Errorf("Expected the pages to hold a, b and c in order, got %v", subscribers)
	}

	for _, limit := range []string{"0", "-1", "ten", "1.5"} {
		recorder := httptest.NewRecorder()
		api.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, QueryRemovedURL+"?service="+service+"&limit="+limit, nil))
		var response removedResponse
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil || response.Code != UNIQUSH_ERROR_GENERIC {
			t.Errorf("Expected limit=%s to be refused with UNIQUSH_ERROR_GENERIC, got %s", limit, recorder.Body.String())
		}
	}
}
