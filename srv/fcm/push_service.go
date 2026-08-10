/*
 * Copyright 2011-2013 Nan Deng
 * Copyright 2013-2026 Uniqush Contributors.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *	http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

// Package fcm implements sending pushes through Firebase Cloud Messaging's
// HTTP v1 API.
//
// It replaces the legacy API, which Google decommissioned on 20 June 2024.
// Three things changed and all of them are load-bearing:
//
//   - Auth moved from a static server key ("Authorization: key=AAAA...") to an
//     OAuth2 bearer token minted from a service account.
//   - Multicast is gone. The legacy API took up to 1000 registration_ids per
//     request and returned a positional results[] array; v1's Message has a
//     target union of exactly one token. The batch endpoint that backed
//     sendAll/sendMulticast was decommissioned alongside the legacy API. One
//     push to N devices is now N HTTP requests, fanned out over a shared
//     HTTP/2 connection.
//   - Errors are per-request rather than array-index-correlated, and the codes
//     are entirely different.
//
// This is registered under two names, "fcm" and "gcm"; see srv/fcm.go.
package fcm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	"github.com/uniqush/uniqush-push/push"
	"github.com/uniqush/uniqush-push/util"
)

const (
	// sendURLFormat is the v1 send endpoint. The project id is part of the path,
	// which is why a project id is required where the legacy fcm backend did not
	// need one.
	sendURLFormat = "https://fcm.googleapis.com/v1/projects/%s/messages:send"

	// tokenScope is the OAuth2 scope for sending messages.
	tokenScope = "https://www.googleapis.com/auth/firebase.messaging"

	// maxConcurrentPushes bounds the fan-out. Google's guidance for replacing
	// the batch endpoint is concurrent requests over a reused HTTP/2
	// connection; this caps how many are in flight per push.
	maxConcurrentPushes = 32

	// requestTimeout applies per request. Google's scaling guide asks for at
	// least 10 seconds before retrying.
	requestTimeout = 30 * time.Second

	// defaultTTLSeconds matches the legacy backend's default of one hour.
	defaultTTLSeconds = 60 * 60

	// legacyGCMName is the alias this backend is also registered under. It only
	// matters for where projectid is stored; see BuildPushServiceProviderFromMap.
	legacyGCMName = "gcm"
)

// HTTPClient is the mockable subset of http.Client used here.
type HTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

var _ HTTPClient = &http.Client{}

// pushService implements push.PushServiceType for FCM HTTP v1.
type pushService struct {
	// name is the registered pushservicetype, so one implementation can serve
	// both "fcm" and "gcm".
	name string

	// clients caches one authenticated client per push service provider.
	//
	// This lives on the service, not on the PushServiceProvider. A PSP is
	// rebuilt from its serialized form on every request, so anything cached on
	// it is thrown away immediately -- which would mean re-reading the service
	// account file from disk and fetching a fresh OAuth2 token for every single
	// push batch.
	clients     map[string]HTTPClient
	clientsLock sync.RWMutex

	// clientFactory is overridden by tests to avoid needing real credentials.
	clientFactory func(psp *push.PushServiceProvider) (HTTPClient, error)

	errChan chan<- push.Error
}

var _ push.PushServiceType = &pushService{}

// NewPushService creates an FCM push service registered under the given name.
func NewPushService(name string) push.PushServiceType {
	ps := &pushService{
		name:    name,
		clients: make(map[string]HTTPClient),
	}
	ps.clientFactory = ps.newAuthenticatedClient
	return ps
}

func (ps *pushService) Name() string { return ps.name }

func (ps *pushService) SetErrorReportChan(errChan chan<- push.Error) { ps.errChan = errChan }

func (ps *pushService) SetPushServiceConfig(_ *push.PushServiceConfig) {}

func (ps *pushService) Finalize() {
	ps.clientsLock.Lock()
	defer ps.clientsLock.Unlock()
	for _, client := range ps.clients {
		if httpClient, ok := client.(*http.Client); ok {
			httpClient.CloseIdleConnections()
		}
	}
}

// OverrideClientFactory lets tests supply clients without real credentials.
func (ps *pushService) OverrideClientFactory(factory func(*push.PushServiceProvider) (HTTPClient, error)) {
	ps.clientsLock.Lock()
	defer ps.clientsLock.Unlock()
	ps.clientFactory = factory
	ps.clients = make(map[string]HTTPClient)
}

// loadCredentials reads and parses a service account file.
//
// Parsing is offline: google.CredentialsFromJSONWithType validates the document
// and prepares a token source, but no token is fetched until the first request.
// That makes it safe to call during /addpsp, which is where a bad path or a
// file that is not actually a service account should be caught.
//
// CredentialsFromJSONWithType rather than the deprecated CredentialsFromJSON:
// it verifies the file really is a service account rather than, say, an
// external-account config pointing somewhere else.
func loadCredentials(path string) (*google.Credentials, error) {
	if path == "" {
		return nil, errors.New("NoCredentialsFile")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("could not read credentialsfile %q: %w", path, err)
	}
	credentials, err := google.CredentialsFromJSONWithType(
		context.Background(), data, google.ServiceAccount, tokenScope)
	if err != nil {
		return nil, fmt.Errorf("could not parse credentialsfile %q: %w", path, err)
	}
	return credentials, nil
}

// newAuthenticatedClient builds an OAuth2 client for a provider.
//
// The returned client refreshes the access token by itself: oauth2 wraps the
// token source in a caching one that re-mints shortly before the hour is up.
func (ps *pushService) newAuthenticatedClient(psp *push.PushServiceProvider) (HTTPClient, error) {
	credentials, err := loadCredentials(psp.VolatileData["credentialsfile"])
	if err != nil {
		return nil, err
	}

	transport := &http.Transport{
		// Google's default of 2 idle connections per host is the documented
		// footgun for fan-out: it serialises the requests that replaced
		// multicast.
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   maxConcurrentPushes,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ForceAttemptHTTP2:     true,
	}
	base := &http.Client{Transport: transport, Timeout: requestTimeout}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, base)

	client := oauth2.NewClient(ctx, credentials.TokenSource)
	client.Timeout = requestTimeout
	return client, nil
}

// clientCacheKey identifies the authenticated client a provider needs.
//
// Deliberately not psp.Name(). A client is entirely determined by the project it
// sends to and the credentials it authenticates with, so two providers sharing
// both can share one token source. Keying on the provider hash would also mean
// depending on PushPeer.Name(), which panics if the push service type has not
// been attached -- true for any provider not built through the service manager.
func clientCacheKey(psp *push.PushServiceProvider) string {
	return projectIDOf(psp) + "\x00" + psp.VolatileData["credentialsfile"]
}

// getClient returns the cached client for a provider, creating it if needed.
func (ps *pushService) getClient(psp *push.PushServiceProvider) (HTTPClient, error) {
	name := clientCacheKey(psp)

	ps.clientsLock.RLock()
	client, ok := ps.clients[name]
	factory := ps.clientFactory
	ps.clientsLock.RUnlock()
	if ok {
		return client, nil
	}

	client, err := factory(psp)
	if err != nil {
		return nil, err
	}

	ps.clientsLock.Lock()
	defer ps.clientsLock.Unlock()
	// Another goroutine may have won the race; prefer its client so there is
	// only ever one token source per provider.
	if existing, ok := ps.clients[name]; ok {
		return existing, nil
	}
	ps.clients[name] = client
	return client, nil
}

// BuildPushServiceProviderFromMap reads the provider's Firebase identity.
//
// Which map each field lands in is not a free choice. A PushPeer's name is a
// hash of its FixedData, and /addpsp refuses to update a provider whose fixed
// data changed -- it treats it as a second, conflicting provider for the
// service. So the fixed data must keep the shape it had under the legacy
// backend, or upgrading in place is impossible and every device has to
// re-subscribe.
//
// The legacy backends disagreed about that shape: "gcm" required a projectid
// and kept it in FixedData, "fcm" had no projectid at all. Each name therefore
// keeps its own historical layout, which is the only way both can be upgraded
// without touching subscriptions.
func (ps *pushService) BuildPushServiceProviderFromMap(kv map[string]string, psp *push.PushServiceProvider) error {
	service, ok := kv["service"]
	if !ok || service == "" {
		return errors.New("NoService")
	}
	psp.FixedData["service"] = service

	projectID, ok := kv["projectid"]
	if !ok || projectID == "" {
		return errors.New("NoProjectID")
	}
	if ps.name == legacyGCMName {
		// Matches the old gcm layout, so an existing gcm provider updates in place.
		psp.FixedData["projectid"] = projectID
	} else {
		// The old fcm provider had only "service" fixed, so projectid has to go
		// in VolatileData to keep that hash stable.
		psp.VolatileData["projectid"] = projectID
	}

	credentialsFile, ok := kv["credentialsfile"]
	if !ok || credentialsFile == "" {
		return errors.New("NoCredentialsFile")
	}
	// A credential, and rotatable, so it must not be part of the identity.
	psp.VolatileData["credentialsfile"] = credentialsFile

	// Fail here rather than on the first push, and do it by actually reading and
	// parsing the file.
	//
	// os.Stat would be the obvious check and is not good enough: it succeeds on
	// a file this process cannot open, so a permissions mistake would sail
	// through /addpsp and only surface later as a failed push. Parsing also
	// catches a path pointing at the wrong JSON entirely -- an API key, an OAuth
	// client, a downloaded google-services.json -- which is an easy mistake and
	// an opaque one to debug at push time.
	if _, err := loadCredentials(credentialsFile); err != nil {
		return err
	}

	return nil
}

// projectID reads the project id from wherever this provider keeps it.
func projectIDOf(psp *push.PushServiceProvider) string {
	if id := psp.FixedData["projectid"]; id != "" {
		return id
	}
	return psp.VolatileData["projectid"]
}

// BuildDeliveryPointFromMap reads one device registration. Unchanged from the
// legacy backend, so existing subscriptions keep their identity.
func (ps *pushService) BuildDeliveryPointFromMap(kv map[string]string, dp *push.DeliveryPoint) error {
	if err := dp.AddCommonData(kv); err != nil {
		return err
	}
	if account, ok := kv["account"]; ok && account != "" {
		dp.FixedData["account"] = account
	}
	regID, ok := kv["regid"]
	if !ok || regID == "" {
		return errors.New("NoRegId")
	}
	dp.FixedData["regid"] = regID
	return nil
}

// message is the v1 request body.
type message struct {
	Message messageBody `json:"message"`
}

type messageBody struct {
	Token        string                 `json:"token,omitempty"`
	Data         map[string]string      `json:"data,omitempty"`
	Notification map[string]interface{} `json:"notification,omitempty"`
	Android      *androidConfig         `json:"android,omitempty"`
}

type androidConfig struct {
	CollapseKey string `json:"collapse_key,omitempty"`
	// TTL is a duration string with a seconds suffix, e.g. "3600s".
	TTL string `json:"ttl,omitempty"`
}

// buildMessage turns a uniqush notification into a v1 message body for one token.
func (ps *pushService) buildMessage(notif *push.Notification, regID string) (*message, push.Error) {
	data := notif.Data
	body := messageBody{Token: regID}
	android := new(androidConfig)

	if group, ok := data["msggroup"]; ok && group != "" {
		android.CollapseKey = group
	}
	ttl := uint64(defaultTTLSeconds)
	if raw, ok := data["ttl"]; ok {
		if parsed, err := strconv.ParseUint(raw, 10, 32); err == nil {
			ttl = parsed
		}
	}
	// v1 wants a Duration string rather than a bare integer.
	android.TTL = strconv.FormatUint(ttl, 10) + "s"
	body.Android = android

	if raw, ok := data[ps.rawNotificationKey()]; ok && raw != "" {
		notification, err := decodeRawObject(ps.rawNotificationKey(), raw)
		if err != nil {
			return nil, err
		}
		body.Notification = notification
	}

	if raw, ok := data[ps.rawPayloadKey()]; ok && raw != "" {
		// A raw payload replaces the data map wholesale, as it did before.
		decoded, err := decodeRawObject(ps.rawPayloadKey(), raw)
		if err != nil {
			return nil, err
		}
		stringified, err := stringifyData(ps.rawPayloadKey(), decoded)
		if err != nil {
			return nil, err
		}
		body.Data = stringified
	} else {
		body.Data = make(map[string]string, len(data))
		for key, value := range data {
			// uniqush.* is reserved, and these three are consumed above.
			if strings.HasPrefix(key, "uniqush.") {
				continue
			}
			switch key {
			case "msggroup", "ttl", "collapse_key":
				continue
			}
			body.Data[key] = value
		}
	}

	if len(body.Data) == 0 && len(body.Notification) == 0 {
		return nil, push.NewBadNotificationWithDetails("empty payload")
	}
	return &message{Message: body}, nil
}

func (ps *pushService) rawPayloadKey() string      { return "uniqush.payload." + ps.name }
func (ps *pushService) rawNotificationKey() string { return "uniqush.notification." + ps.name }

func decodeRawObject(key, raw string) (map[string]interface{}, push.Error) {
	decoded := make(map[string]interface{})
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		return nil, push.NewBadNotificationWithDetails(
			fmt.Sprintf("%s is not a JSON object: %v", key, err))
	}
	return decoded, nil
}

// stringifyData enforces the one payload rule v1 added: every value in the data
// map must be a string.
//
// The legacy API accepted arbitrary JSON here and uniqush passed it through, so
// a caller sending nested objects or numbers worked before and does not now.
// Rejecting it with a specific message is much kinder than FCM's opaque 400.
func stringifyData(key string, decoded map[string]interface{}) (map[string]string, push.Error) {
	result := make(map[string]string, len(decoded))
	for field, value := range decoded {
		switch typed := value.(type) {
		case string:
			result[field] = typed
		default:
			return nil, push.NewBadNotificationWithDetails(fmt.Sprintf(
				"%s[%q] is %T, but FCM HTTP v1 requires every value in \"data\" to be a string; "+
					"encode it yourself, or move it to %s",
				key, field, value, strings.Replace(key, "payload", "notification", 1)))
		}
	}
	return result, nil
}

// Preview shows the JSON that would be sent for a placeholder registration.
func (ps *pushService) Preview(notif *push.Notification) ([]byte, push.Error) {
	body, err := ps.buildMessage(notif, "placeholderRegId")
	if err != nil {
		return nil, err
	}
	encoded, e := util.MarshalJSONUnescaped(body)
	if e != nil {
		return nil, push.NewErrorf("Error converting payload to JSON: %v", e)
	}
	return encoded, nil
}

// Push sends the notification to every delivery point read from dpQueue.
//
// There is no multicast in v1, so this is one request per registration token,
// fanned out over a bounded worker pool sharing one HTTP/2 connection.
func (ps *pushService) Push(psp *push.PushServiceProvider, dpQueue <-chan *push.DeliveryPoint, resQueue chan<- *push.Result, notif *push.Notification) {
	defer close(resQueue)

	projectID := projectIDOf(psp)
	if projectID == "" {
		drain(dpQueue)
		resQueue <- &push.Result{
			Provider: psp, Content: notif,
			Err: push.NewBadPushServiceProviderWithDetails(psp, "NoProjectID"),
		}
		return
	}

	client, err := ps.getClient(psp)
	if err != nil {
		drain(dpQueue)
		resQueue <- &push.Result{
			Provider: psp, Content: notif,
			Err: push.NewBadPushServiceProviderWithDetails(psp, err.Error()),
		}
		return
	}

	url := fmt.Sprintf(sendURLFormat, projectID)
	wg := new(sync.WaitGroup)
	semaphore := make(chan struct{}, maxConcurrentPushes)

	for dp := range dpQueue {
		if psp.PushServiceName() != dp.PushServiceName() || psp.PushServiceName() != ps.name {
			resQueue <- &push.Result{
				Provider: psp, Destination: dp, Content: notif,
				Err: push.NewIncompatibleError(),
			}
			continue
		}
		regID := dp.VolatileData["regid"]
		if regID == "" {
			regID = dp.FixedData["regid"]
			if regID == "" {
				resQueue <- &push.Result{
					Provider: psp, Destination: dp, Content: notif,
					Err: push.NewBadDeliveryPointWithDetails(dp,
						fmt.Sprintf("uniqush delivery point for %s is missing regid", strings.ToUpper(ps.name))),
				}
				continue
			}
			// Mirrored into VolatileData so it can be updated in flight, as the
			// legacy backend did.
			dp.VolatileData["regid"] = regID
		}

		wg.Add(1)
		semaphore <- struct{}{}
		go func(dp *push.DeliveryPoint, regID string) {
			defer wg.Done()
			defer func() { <-semaphore }()
			resQueue <- ps.pushOne(client, url, psp, dp, notif, regID)
		}(dp, regID)
	}
	wg.Wait()
}

func drain(dpQueue <-chan *push.DeliveryPoint) {
	go func() {
		for range dpQueue { //nolint:revive // draining is the point
		}
	}()
}

// pushOne sends to a single registration token.
func (ps *pushService) pushOne(client HTTPClient, url string, psp *push.PushServiceProvider, dp *push.DeliveryPoint, notif *push.Notification, regID string) *push.Result {
	result := &push.Result{Provider: psp, Destination: dp, Content: notif}

	body, buildErr := ps.buildMessage(notif, regID)
	if buildErr != nil {
		result.Err = buildErr
		return result
	}
	encoded, err := util.MarshalJSONUnescaped(body)
	if err != nil {
		result.Err = push.NewErrorf("Error converting payload to JSON: %v", err)
		return result
	}

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(encoded))
	if err != nil {
		result.Err = push.NewErrorf("Error constructing HTTP request: %v", err)
		return result
	}
	// "application/json", not the "application/json; UTF-8" that Google's own
	// examples show. That is not a valid media type parameter -- the parameter
	// is spelled charset -- and JSON is UTF-8 by definition (RFC 8259 s8.1), so
	// there is nothing to declare. srv/adm.go already sends the plain form.
	request.Header.Set("Content-Type", "application/json")

	response, err := client.Do(request)
	if err != nil {
		result.Err = push.NewConnectionError(err)
		return result
	}
	defer response.Body.Close()

	result.Err = ps.interpretResponse(response, psp, dp, notif, &result.MsgID)
	return result
}
