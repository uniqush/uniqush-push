/*
 * Copyright 2026 Uniqush Contributors.
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

// Package webpush implements sending pushes over Web Push (RFC 8030), with
// payload encryption per RFC 8291 and VAPID authentication per RFC 8292.
//
// This is what UnifiedPush uses between an application server and a push
// server, so the same implementation serves both UnifiedPush distributors
// (ntfy, NextPush, Sunup, ...) and browser Web Push endpoints. It is registered
// under two names, "webpush" and "unifiedpush"; see srv/webpush.go.
//
// Unlike every other uniqush backend, the destination host here comes from the
// subscriber rather than from a vendor. See endpoint.go for what that implies.
package webpush

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	webpush "github.com/SherClockHolmes/webpush-go"

	"github.com/uniqush/uniqush-push/push"
	"github.com/uniqush/uniqush-push/util"
)

const (
	// defaultTTL is how long a push server may hold the message for a device
	// that is offline, in seconds.
	//
	// It is deliberately not zero. webpush-go always writes the TTL header, and
	// a TTL of 0 means "deliver only if the device is connected right now, else
	// discard" -- and Microsoft WNS rejects TTL: 0 outright with a 400. Since
	// Go's zero value for the option is 0, "just leave it unset" is the broken
	// choice.
	defaultTTL = 12 * 60 * 60

	// contentCodingHeaderSize is the RFC 8188 aes128gcm header: 16 salt + 4 rs + 1 idlen +
	// 65 keyid, where keyid is the sender's (application server's) uncompressed P-256 ECDH public key.
	contentCodingHeaderSize = 16 + 4 + 1 + 65

	// gcmTagSize and paddingDelimiterSize are the rest of what a record spends
	// on something other than the payload.
	gcmTagSize           = 16
	paddingDelimiterSize = 1

	// defaultRecordSize is the RFC 8188 record size, which determines the size
	// of the encrypted body. Override it with record_size in the [webpush] or
	// [unifiedpush] config section.
	//
	// webpush-go pads every message to completely fill the record, so the POST
	// body is always exactly this many bytes no matter how short the payload.
	// 4096 is therefore not free -- it spends a full MTU on a 20-byte wakeup
	// ping -- but it is the right default anyway:
	//
	//   - It is what every other application server emits (webpush-go's own
	//     default, pywebpush's, web-push's), and one client family reads the
	//     field strictly. google/tink's apps-webpush requires the record size in
	//     the header to *equal* its configured size, which defaults to 4096, so
	//     it rejects anything smaller outright. UnifiedPush forked Tink to fix
	//     that (connector 3.0.4 and later), but an app on an older connector, or
	//     one using stock apps-webpush directly, cannot read a 2048-byte record.
	//   - It is the largest the UnifiedPush spec permits, which makes the
	//     payload ceiling below match the spec's own 3993 bytes. A smaller
	//     record silently caps what an app can send.
	//
	// Servers sending mostly wakeup pings can halve their egress by setting
	// record_size=2048, at the cost of the Tink-based clients above.
	defaultRecordSize = 4096

	// minRecordSize is the smallest record that can carry a one-byte payload.
	// RFC 8188's own floor is 18, which is below the fixed overhead here.
	minRecordSize = contentCodingHeaderSize + gcmTagSize + paddingDelimiterSize + 1

	// maxRecordSize is the UnifiedPush spec's ceiling on a push message, and
	// also what browser push services accept. A larger record would be refused
	// with a 413 by everything worth sending to.
	maxRecordSize = 4096

	// maxConcurrentPushes bounds the worker pool. Web Push has no multicast, so
	// a push to N subscribers is N requests to N third-party hosts of unknown
	// latency; unbounded goroutines would be a poor neighbour to both.
	maxConcurrentPushes = 16

	// defaultRequestTimeout applies per push, and is what request_timeout in
	// this service's config section overrides.
	defaultRequestTimeout = 30 * time.Second

	// minRequestTimeout and maxRequestTimeout bound request_timeout. Below a
	// second nothing on the public internet completes, so a smaller value would
	// only fail every push; above five minutes the caller waiting on /push has
	// given up long before uniqush does.
	minRequestTimeout = 1 * time.Second
	maxRequestTimeout = 5 * time.Minute

	// defaultRetryAfter is used when a push server rate-limits us without
	// saying for how long.
	defaultRetryAfter = 60 * time.Second
)

// payloadKey is the /push parameter carrying a raw payload to deliver verbatim.
const payloadKey = "uniqush.payload.webpush"

// pushService implements push.PushServiceType for Web Push.
type pushService struct {
	// name is the registered pushservicetype, so the same implementation can
	// serve both "webpush" and "unifiedpush".
	name string

	// timeout is the per-push deadline, in nanoseconds. Atomic for the same
	// reason as recordSize below. Zero means the default, which is only the
	// case before the first configuration.
	timeout atomic.Int64

	// recordSize is the RFC 8188 record size every push is padded to, and so
	// also the size of every POST body. See defaultRecordSize.
	//
	// Atomic for the same reason as policy below: a reconfiguration writes it
	// while pushes are reading it, and a push that read a stale size would
	// build a record of one size and declare another in the header.
	recordSize atomic.Uint32

	// policy is swapped, never mutated in place. SetPushServiceConfig can be
	// called again -- RegisterPushServiceType calls it too, and nothing stops a
	// future config reload from calling it once uniqush is serving -- while
	// /subscribe and every push are reading it. Publishing a whole new policy
	// through an atomic pointer means a reader sees one config or the other and
	// never a half-applied one, and never a map being written as it reads it.
	// srv/apns holds its equivalent gate in an atomic.Bool for the same reason.
	policy atomic.Pointer[EndpointPolicy]

	client  *http.Client
	errChan chan<- push.Error
}

// endpointPolicy returns the policy in force right now. The value it returns is
// immutable; a reconfiguration replaces it rather than editing it.
func (ps *pushService) endpointPolicy() *EndpointPolicy {
	return ps.policy.Load()
}

// requestTimeout is the deadline for one push request.
func (ps *pushService) requestTimeout() time.Duration {
	if timeout := ps.timeout.Load(); timeout > 0 {
		return time.Duration(timeout)
	}
	return defaultRequestTimeout
}

// maxPayloadSize is the largest plaintext that fits in one record.
func (ps *pushService) maxPayloadSize() int {
	return int(ps.recordSize.Load()) - contentCodingHeaderSize - gcmTagSize - paddingDelimiterSize
}

var _ push.PushServiceType = &pushService{}

// NewPushService creates a Web Push push service registered under the given name.
func NewPushService(name string) push.PushServiceType {
	service := &pushService{
		name:   name,
		client: newHTTPClient(),
	}
	service.recordSize.Store(defaultRecordSize)
	service.policy.Store(NewEndpointPolicy())
	return service
}

// newHTTPClient builds the shared client.
//
// webpush-go allocates a fresh zero-value http.Client per call when none is
// supplied, which means no timeout and a new connection pool for every single
// push. Supplying one is not optional for a long-lived server.
func newHTTPClient() *http.Client {
	return &http.Client{
		// No client-level Timeout: every push carries a context with the
		// configured deadline, and a second timeout fixed at construction
		// would silently cap a longer request_timeout set later.
		Timeout: 0,
		// The UnifiedPush spec: "Redirects MUST NOT be followed on push
		// endpoints." Go's default follows up to 10, which would also sidestep
		// the SSRF checks in EndpointPolicy, since only the first URL is vetted.
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Transport: &http.Transport{
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   4, // many distinct hosts, few requests each
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			ForceAttemptHTTP2:     true,
		},
	}
}

func (ps *pushService) Name() string {
	return ps.name
}

func (ps *pushService) Finalize() {
	ps.client.CloseIdleConnections()
}

func (ps *pushService) SetErrorReportChan(errChan chan<- push.Error) {
	ps.errChan = errChan
}

// SetPushServiceConfig reads the optional config section named after this push
// service, i.e. [webpush] or [unifiedpush] in uniqush-push.conf. A server that
// registers both names configures them separately, since they are two
// registrations of this type.
//
// allow_private_addresses and allowed_hosts exist for self-hosted push servers,
// which are a first-class UnifiedPush use case and may legitimately live on a
// private network. record_size trades egress against client compatibility; see
// defaultRecordSize. request_timeout is how long one push server has to answer,
// which matters more here than for the vendor backends: the endpoint is chosen
// by whoever called /subscribe, so its latency is not something uniqush can
// assume anything about.
func (ps *pushService) SetPushServiceConfig(c *push.PushServiceConfig) {
	if c == nil {
		return
	}
	// Both of the next two are written unconditionally, including when the
	// option is absent or unparseable. They relax the endpoint policy, so a
	// missing option has to mean "closed" rather than "leave whatever was there":
	// SetPushServiceConfig runs again whenever the push service manager
	// reconfigures, and only writing on success would leave a gate an earlier
	// config had opened still open after the line was deleted or corrupted.
	// Deleting a line has to undo it. srv/apns does the same for
	// allow_non_apple_endpoints.
	policy := NewEndpointPolicy()

	allow, err := c.GetBool("allow_private_addresses")
	policy.AllowPrivateAddresses = err == nil && allow

	// SetAllowedHosts lowercases entries, since DNS hostnames are
	// case-insensitive and a mixed-case config entry would never match. An empty
	// or all-blank list clears the allow-list, which is what an absent option
	// should mean.
	hosts, err := c.GetString("allowed_hosts")
	if err != nil {
		hosts = ""
	}
	policy.SetAllowedHosts(strings.Split(hosts, ","))

	// Published only once it is fully built, so no reader ever sees a policy
	// with the allow-list of one config and the private-address setting of
	// another.
	ps.policy.Store(policy)

	// Written unconditionally, so that deleting the option restores the default
	// rather than leaving whatever an earlier config set. An out-of-range or
	// unparseable value falls back to the default too: the alternative is a
	// server that starts up and then rejects every payload over some size
	// nobody chose, or one that emits records no client will accept.
	recordSize := uint32(defaultRecordSize)
	if size, err := c.GetInt("record_size"); err == nil && size >= minRecordSize && size <= maxRecordSize {
		recordSize = uint32(size)
	}
	ps.recordSize.Store(recordSize)

	// Unconditional for the same reason, and bounded because a push server on
	// the far side of the internet is not uniqush's to wait on indefinitely.
	ps.timeout.Store(int64(c.GetSeconds(optionRequestTimeout, defaultRequestTimeout, minRequestTimeout, maxRequestTimeout)))
}

// optionRequestTimeout is the uniqush.conf option, in this push service's own
// section, holding a per-push timeout in seconds.
const optionRequestTimeout = "request_timeout"

// BuildPushServiceProviderFromMap reads the VAPID identity for a service.
//
// VAPID (RFC 8292) lets a push server attribute pushes to us and lets a
// subscriber restrict their subscription to us. Some push servers refuse
// registrations without it, so it is required rather than optional.
func (ps *pushService) BuildPushServiceProviderFromMap(kv map[string]string, psp *push.PushServiceProvider) error {
	service, ok := kv["service"]
	if !ok || service == "" {
		return errors.New("NoService")
	}
	psp.FixedData["service"] = service

	publicKey, ok := kv["vapidpublickey"]
	if !ok || publicKey == "" {
		return errors.New("NoVAPIDPublicKey")
	}
	if err := validateVAPIDKey("vapidpublickey", publicKey, 65); err != nil {
		return err
	}
	// The public key is part of the service's identity: changing it invalidates
	// every subscription made against it, so it belongs in FixedData.
	psp.FixedData["vapidpublickey"] = publicKey

	privateKey, ok := kv["vapidprivatekey"]
	if !ok || privateKey == "" {
		return errors.New("NoVAPIDPrivateKey")
	}
	if err := validateVAPIDKey("vapidprivatekey", privateKey, 32); err != nil {
		return err
	}
	// VolatileData so it is not part of the PSP's hashed identity, and so it is
	// not echoed back by /psps.
	psp.VolatileData["vapidprivatekey"] = privateKey

	subscriber, ok := kv["subscriber"]
	if !ok || subscriber == "" {
		return errors.New("NoSubscriber")
	}
	// webpush-go prepends "mailto:" to anything that is not an https URL, so a
	// pre-formed mailto: URI becomes "mailto:mailto:...". Take the bare form.
	subscriber = strings.TrimPrefix(subscriber, "mailto:")
	if subscriber == "" {
		return errors.New("NoSubscriber")
	}
	psp.FixedData["subscriber"] = subscriber

	return nil
}

// validateVAPIDKey checks a key decodes as raw-url base64 to the expected length.
// Catching this at /addpsp is much kinder than a signature failure per push.
func validateVAPIDKey(field, value string, expectedLen int) error {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		// Tolerate padded input, which some tools emit.
		decoded, err = base64.URLEncoding.DecodeString(value)
		if err != nil {
			return fmt.Errorf("%s is not valid raw-url base64: %v", field, err)
		}
	}
	if len(decoded) != expectedLen {
		return fmt.Errorf("%s decodes to %d bytes, expected %d", field, len(decoded), expectedLen)
	}
	return nil
}

// BuildDeliveryPointFromMap reads one Web Push subscription: the endpoint URL
// plus the two key materials the client's connector library generated.
func (ps *pushService) BuildDeliveryPointFromMap(kv map[string]string, dp *push.DeliveryPoint) error {
	if err := dp.AddCommonData(kv); err != nil {
		return err
	}

	endpoint, ok := kv["endpoint"]
	if !ok || endpoint == "" {
		return errors.New("NoEndpoint")
	}
	if err := ps.endpointPolicy().ValidateSyntax(endpoint); err != nil {
		return fmt.Errorf("invalid delivery point: %v", err)
	}
	dp.FixedData["endpoint"] = endpoint

	p256dh, ok := kv["p256dh"]
	if !ok || p256dh == "" {
		return errors.New("NoP256dh")
	}
	if err := validateSubscriptionKey("p256dh", p256dh, 65); err != nil {
		return fmt.Errorf("invalid delivery point: %v", err)
	}
	dp.FixedData["p256dh"] = p256dh

	auth, ok := kv["auth"]
	if !ok || auth == "" {
		return errors.New("NoAuth")
	}
	if err := validateSubscriptionKey("auth", auth, 16); err != nil {
		return fmt.Errorf("invalid delivery point: %v", err)
	}
	dp.FixedData["auth"] = auth

	return nil
}

// validateSubscriptionKey accepts either base64 alphabet, since connector
// libraries differ, and checks the decoded length.
func validateSubscriptionKey(field, value string, expectedLen int) error {
	decoded, err := decodeAnyBase64(value)
	if err != nil {
		return fmt.Errorf("%s is not valid base64: %v", field, err)
	}
	if len(decoded) != expectedLen {
		return fmt.Errorf("%s decodes to %d bytes, expected %d", field, len(decoded), expectedLen)
	}
	return nil
}

func decodeAnyBase64(value string) ([]byte, error) {
	// Pad to a multiple of 4 so the unpadded forms decode too.
	padded := value
	if remainder := len(padded) % 4; remainder != 0 {
		padded += "===="[:4-remainder]
	}
	if decoded, err := base64.StdEncoding.DecodeString(padded); err == nil {
		return decoded, nil
	}
	return base64.URLEncoding.DecodeString(padded)
}

// Preview returns the plaintext that would be encrypted and sent.
//
// It deliberately does not return the ciphertext: RFC 8291 encryption is
// randomised per message and keyed to a specific subscriber, so the bytes on
// the wire are both non-reproducible and useless for debugging.
func (ps *pushService) Preview(notif *push.Notification) ([]byte, push.Error) {
	payload, err := toWebPushPayload(notif)
	if err != nil {
		return nil, err
	}
	return payload, nil
}

// toWebPushPayload builds the message body.
//
// Web Push does not define the payload; it is an opaque blob handed to the
// application on the device. uniqush.payload.webpush is passed through verbatim
// so callers can send whatever their app expects. Otherwise the notification's
// own fields are marshalled as JSON, which is what most apps want.
func toWebPushPayload(notif *push.Notification) ([]byte, push.Error) {
	if raw, ok := notif.Data[payloadKey]; ok && raw != "" {
		return []byte(raw), nil
	}

	payload := make(map[string]string, len(notif.Data))
	for key, value := range notif.Data {
		// Strip uniqush's own control parameters; they are not for the device.
		if strings.HasPrefix(key, "uniqush.") {
			continue
		}
		payload[key] = value
	}
	if len(payload) == 0 {
		return nil, push.NewBadNotificationWithDetails("empty payload")
	}

	encoded, err := util.MarshalJSONUnescaped(payload)
	if err != nil {
		return nil, push.NewBadNotificationWithDetails(fmt.Sprintf("could not encode payload: %v", err))
	}
	return encoded, nil
}

// Push sends the notification to every delivery point read from dpQueue.
//
// Web Push has no multicast: one subscriber is one HTTP request. A bounded
// worker pool keeps that from becoming an unbounded goroutine fan-out across
// arbitrary third-party hosts.
func (ps *pushService) Push(psp *push.PushServiceProvider, dpQueue <-chan *push.DeliveryPoint, resQueue chan<- *push.Result, notif *push.Notification) {
	defer close(resQueue)

	payload, payloadErr := toWebPushPayload(notif)
	if payloadErr == nil && len(payload) > ps.maxPayloadSize() {
		payloadErr = push.NewBadNotificationWithDetails(
			fmt.Sprintf("payload is too large: %d > %d", len(payload), ps.maxPayloadSize()))
	}
	if payloadErr != nil {
		// Drain dpQueue so the caller is not blocked, then report once.
		go func() {
			for range dpQueue { //nolint:revive // draining is the point
			}
		}()
		resQueue <- &push.Result{
			Provider:    psp,
			Content:     notif,
			Destination: nil,
			Err:         payloadErr,
		}
		return
	}

	options, optErr := ps.optionsForPSP(psp)
	if optErr != nil {
		go func() {
			for range dpQueue { //nolint:revive // draining is the point
			}
		}()
		resQueue <- &push.Result{Provider: psp, Content: notif, Err: optErr}
		return
	}

	wg := new(sync.WaitGroup)
	semaphore := make(chan struct{}, maxConcurrentPushes)

	for dp := range dpQueue {
		wg.Add(1)
		semaphore <- struct{}{}
		go func(dp *push.DeliveryPoint) {
			defer wg.Done()
			defer func() { <-semaphore }()
			resQueue <- ps.pushOne(psp, dp, notif, payload, options)
		}(dp)
	}
	wg.Wait()
}

// optionsForPSP assembles the per-service webpush options.
func (ps *pushService) optionsForPSP(psp *push.PushServiceProvider) (*webpush.Options, push.Error) {
	publicKey, ok := psp.FixedData["vapidpublickey"]
	if !ok || publicKey == "" {
		return nil, push.NewBadPushServiceProviderWithDetails(psp, "NoVAPIDPublicKey")
	}
	privateKey, ok := psp.VolatileData["vapidprivatekey"]
	if !ok || privateKey == "" {
		return nil, push.NewBadPushServiceProviderWithDetails(psp, "NoVAPIDPrivateKey")
	}
	subscriber, ok := psp.FixedData["subscriber"]
	if !ok || subscriber == "" {
		return nil, push.NewBadPushServiceProviderWithDetails(psp, "NoSubscriber")
	}
	return &webpush.Options{
		HTTPClient:      ps.client,
		Subscriber:      subscriber,
		VAPIDPublicKey:  publicKey,
		VAPIDPrivateKey: privateKey,
		TTL:             defaultTTL,
		RecordSize:      ps.recordSize.Load(),
		Urgency:         webpush.UrgencyNormal,
	}, nil
}

// pushOne delivers to a single subscription.
func (ps *pushService) pushOne(psp *push.PushServiceProvider, dp *push.DeliveryPoint, notif *push.Notification, payload []byte, options *webpush.Options) *push.Result {
	result := &push.Result{
		Provider:    psp,
		Content:     notif,
		Destination: dp,
	}

	endpoint := dp.FixedData["endpoint"]

	// Re-check immediately before connecting. Checking only at /subscribe time
	// is defeated by DNS rebinding.
	if err := ps.endpointPolicy().ValidateForSend(endpoint); err != nil {
		result.Err = push.NewBadDeliveryPointWithDetails(dp, err.Error())
		return result
	}

	subscription := &webpush.Subscription{
		Endpoint: endpoint,
		Keys: webpush.Keys{
			P256dh: dp.FixedData["p256dh"],
			Auth:   dp.FixedData["auth"],
		},
	}

	// webpush-go wraps the payload in a bytes.Buffer and appends the RFC 8188
	// padding delimiter and padding to it, which writes into the caller's
	// backing array. Handing the same slice to concurrent sends is therefore a
	// data race that corrupts the plaintext, so give each send its own copy.
	// `go test -race` catches this; a production server would see intermittently
	// mangled notifications.
	message := make([]byte, len(payload))
	copy(message, payload)

	ctx, cancel := context.WithTimeout(context.Background(), ps.requestTimeout())
	defer cancel()

	response, err := webpush.SendNotificationWithContext(ctx, message, subscription, options)
	if err != nil {
		result.Err = push.NewConnectionError(err)
		return result
	}
	// webpush-go returns the raw response and neither inspects the status nor
	// closes the body. Both are ours to do. The nil check is not paranoia about
	// net/http, which always sets a body, but about the transports tests and
	// middleware substitute for it: a nil body here would panic on a path that
	// only runs when a push has already failed.
	if response.Body != nil {
		defer response.Body.Close()
	}

	switch classifyStatus(response.StatusCode) {
	case outcomeSuccess:
		result.MsgID = response.Header.Get("Location")
		return result
	case outcomeUnsubscribe:
		result.Err = push.NewUnsubscribeUpdate(psp, dp)
		return result
	case outcomeBadNotification:
		result.Err = push.NewBadNotificationWithDetails(
			fmt.Sprintf("push server rejected the request with HTTP %d%s", response.StatusCode, describeBody(response)))
		return result
	default:
		delay := retryAfter(response.Header, time.Now())
		if delay == 0 {
			delay = defaultRetryAfter
		}
		result.Err = push.NewRetryErrorWithReason(psp, dp, notif, delay,
			fmt.Errorf("push server returned HTTP %d%s", response.StatusCode, describeBody(response)))
		return result
	}
}

// maxErrorBodyBytes is how much of a failed response to quote. Push servers say
// why in a sentence or a small JSON object; nobody needs more than this, and the
// body is remote input going into a log line.
const maxErrorBodyBytes = 256

// describeBody quotes the start of an error response, ready to append to a
// message. Push servers explain their refusals in the body -- ntfy answers
// {"code":40301,"http":403,"error":"forbidden"}, Mozilla autopush names the
// header it did not like -- and a status code alone leaves an operator guessing
// between a rate limit, a rejected VAPID key and a topic that does not exist.
//
// The result ends up in log lines and in API responses, so this is the boundary
// where remote bytes are made safe to embed: control characters go, and the rest
// is flattened to one line. A push server could otherwise put a newline and a
// plausible-looking prefix in its body and forge a log record.
func describeBody(response *http.Response) string {
	// A response with no body is legal for an http.Client to return, and this
	// runs on a path where something has already gone wrong. Panicking here
	// would turn a failed push into a failed server.
	if response.Body == nil {
		return ""
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxErrorBodyBytes))
	if err != nil || len(body) == 0 {
		return ""
	}
	printable := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return ' '
		}
		if !unicode.IsPrint(r) {
			return -1
		}
		return r
	}, string(body))
	// Collapse the runs of spaces the mapping above can leave behind.
	flattened := strings.Join(strings.Fields(printable), " ")
	if flattened == "" {
		return ""
	}
	return fmt.Sprintf(": %s", flattened)
}

// GenerateVAPIDKeys returns a fresh VAPID keypair, raw-url base64 encoded.
func GenerateVAPIDKeys() (privateKey, publicKey string, err error) {
	return webpush.GenerateVAPIDKeys()
}
