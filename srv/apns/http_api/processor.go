// Package http_api implements a client for the new APNs HTTP/2 API (over an encrypted HTTP/2 connection to APNs)
package http_api //nolint:revive

import (
	"bytes"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/http2"

	"github.com/uniqush/uniqush-push/push"
	"github.com/uniqush/uniqush-push/srv/apns/common"
)

// HTTPClient is a mockable interface for the parts of http.Client used by the APNs HTTP2 module.
// The underlying implementation contains a connection pool, to tolerate spurious network errors.
type HTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

// ClientFactory is an abstraction to create HTTPClient instances (one for each push service provider). The value is overridden for testing.
type ClientFactory func(*http.Transport) HTTPClient

// HTTPPushRequestProcessor sends push notification requests to APNs using HTTP API
type HTTPPushRequestProcessor struct {
	clients       map[string]HTTPClient
	clientsLock   sync.RWMutex
	clientFactory ClientFactory // can be overridden by test
}

// NewRequestProcessor returns a new HTTPPushProcessor using net/http DefaultClient connection pool
func NewRequestProcessor() common.PushRequestProcessor {
	return &HTTPPushRequestProcessor{
		clients:       make(map[string]HTTPClient),
		clientFactory: defaultClientFactory,
	}
}

// AddRequest will asynchronously process the request to send a push notification to APNs over HTTP/2
func (prp *HTTPPushRequestProcessor) AddRequest(request *common.PushRequest) {
	go prp.sendRequests(request)
}

// GetMaxPayloadSize will return the max JSON payload size for HTTP/2 pushes (which is larger than the binary API)
func (prp *HTTPPushRequestProcessor) GetMaxPayloadSize() int {
	return 4096
}

// clientCacheKey identifies the client a provider needs.
//
// Deliberately not psp.Name() alone. A name hashes only FixedData, but the
// endpoint, the CA bundle and skipverify all live in VolatileData -- they have
// to, so that changing one does not strand every existing subscription. Keying
// on the name alone would mean a provider updated to point somewhere else kept
// using a cached client still pointed at the old destination, with the old
// trust settings, until uniqush was restarted.
//
// The CA is keyed by the digest of its contents, not by its pathname. Rotating
// a bundle in place -- writing the new authority over the old file and
// re-running /addpsp -- does not change the path, so a cache keyed on it would
// go on using a client that trusts the retired CA and not the new one, until
// the process restarted. Reading the file to hash it costs one small read per
// push batch, against a network round trip to Apple.
//
// skipverify is taken from ShouldSkipVerify rather than from the raw setting, so
// that two providers differing only in a stored flag that is being ignored share
// one client instead of pointlessly building two.
func clientCacheKey(psp *push.PushServiceProvider) string {
	return strings.Join([]string{
		psp.Name(),
		common.ResolveEndpoint(psp),
		common.CACertFingerprint(psp.VolatileData[common.CACertKey]),
		strconv.FormatBool(common.ShouldSkipVerify(psp)),
	}, "\x00")
}

// GetClient will return the only HTTP client instance for the given psp. That instance uses the credentials and endpoint associated with the given psp.
func (prp *HTTPPushRequestProcessor) GetClient(psp *push.PushServiceProvider) (HTTPClient, error) {
	pspName := clientCacheKey(psp)
	if client := prp.TryGetClient(pspName); client != nil {
		return client, nil
	}
	tlsClientConfig, err := createTLSConfig(psp)
	if err != nil {
		return nil, fmt.Errorf("GetClient failed, couldn't create TLS config: %v", err)
	}
	prp.clientsLock.Lock()
	defer prp.clientsLock.Unlock()
	if client, ok := prp.clients[pspName]; ok {
		// Maybe something else locked before this goroutine did.
		return client, nil
	}
	transport := &http.Transport{
		// The same as GCM.
		// TODO: Make the maximum number of idle connections configurable.
		// Note: It's likely that fewer idle clients should be needed than GCM, since HTTP2 allows multiple in-flight requests
		// Note: Do not set IdleTimeout, it may be a cause of errors in setups where pushes are infrequent.
		MaxIdleConns:          20,
		MaxIdleConnsPerHost:   20,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		// Because TLSClientConfig is provided, we have to manually configure this client for http2 support.
		TLSClientConfig: tlsClientConfig,
	}

	err = http2.ConfigureTransport(transport)
	if err != nil {
		return nil, fmt.Errorf("GetClient failed, couldn't configure for http2: %v", err)
	}

	client := prp.clientFactory(transport)
	prp.clients[pspName] = client
	return client, nil
}

func defaultClientFactory(transport *http.Transport) HTTPClient {
	return &http.Client{
		Transport: transport,
		Timeout:   20 * time.Second,
	}
}

// createTLSConfig builds the TLS settings for one provider's connections.
//
// Verification is on by default and the two ways to change that are both
// explicit. A CA bundle is the one to prefer when testing: it still checks the
// certificate and the hostname, so a simulator has to present a certificate the
// operator actually issued, rather than any certificate at all.
//
// The InsecureSkipVerify branch is guarded by common.ShouldSkipVerify rather
// than by the stored setting. /addpsp refuses the combination at registration
// time, but a provider loaded from the database never goes through /addpsp's
// builder, so the rule has to hold here too or a stale flag from the
// binary-protocol era would silently downgrade a connection to Apple.
func createTLSConfig(psp *push.PushServiceProvider) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(psp.FixedData["cert"], psp.FixedData["key"])
	if err != nil {
		return nil, push.NewBadPushServiceProviderWithDetails(psp, err.Error())
	}

	conf := &tls.Config{
		Certificates: []tls.Certificate{cert},
		// APNs has required TLS 1.2 since 2018, so nothing is lost by refusing
		// to negotiate lower with anything standing in for it.
		MinVersion: tls.VersionTLS12,
	}

	if caCertPath := psp.VolatileData[common.CACertKey]; caCertPath != "" {
		pool, caErr := common.LoadCACert(caCertPath)
		if caErr != nil {
			return nil, push.NewBadPushServiceProviderWithDetails(psp, caErr.Error())
		}
		conf.RootCAs = pool
	}

	// Re-checked here rather than trusted from /addpsp, because a provider read
	// back from the database never passes through the builder that validates it.
	// ShouldSkipVerify refuses to disable verification for a destination that
	// resolves to Apple, whatever the stored setting says.
	if common.ShouldSkipVerify(psp) {
		// For a local simulator whose certificate is generated per run, where
		// pinning a CA is more ceremony than the test is worth.
		conf.InsecureSkipVerify = true //nolint:gosec // refused for api*.push.apple.com; see common.ShouldSkipVerify
	}

	return conf, nil
}

// TryGetClient will
func (prp *HTTPPushRequestProcessor) TryGetClient(pspName string) HTTPClient {
	prp.clientsLock.RLock()
	defer prp.clientsLock.RUnlock()
	if client, exists := prp.clients[pspName]; exists {
		return client
	}
	return nil
}

// Finalize will shut down all of the connections owned by HTTP/2 clients for each PSP.
func (prp *HTTPPushRequestProcessor) Finalize() {
	prp.clientsLock.Lock()
	for _, client := range prp.clients {
		if httpClient, isClient := client.(*http.Client); isClient {
			switch transport := httpClient.Transport.(type) {
			case *http.Transport:
				transport.CloseIdleConnections()
			default:
			}
		}
	}
}

// SetErrorReportChan will set the report chan used for asynchronous feedback that is not associated with a request. (not needed when using APNs's HTTP/2 API, but needed for the binary API)
func (prp *HTTPPushRequestProcessor) SetErrorReportChan(errChan chan<- push.Error) {}

// SetPushServiceConfig is called during initialization to provide the unserialized contents of uniqush.conf. (does nothing for cloud messaging)
func (prp *HTTPPushRequestProcessor) SetPushServiceConfig(c *push.PushServiceConfig) {}

// sendRequests will send a push to one or more device tokens. It will send the response over ResChan or ErrChan.
func (prp *HTTPPushRequestProcessor) sendRequests(request *common.PushRequest) {
	defer close(request.ErrChan)

	bundleid, ok := request.PSP.VolatileData["bundleid"]
	if !ok || bundleid == "" {
		for range request.Devtokens {
			request.ErrChan <- push.NewError("Must add bundleid to PSP by calling /addpsp again")
		}
		return
	}

	wg := new(sync.WaitGroup)
	wg.Add(len(request.Devtokens))

	pushType := request.PushType
	if pushType == "" {
		pushType = common.DefaultPushType
	}

	// Header names are written lowercase deliberately. HTTP/2 requires
	// lowercase field names on the wire, and using http.Header.Set here would
	// canonicalise to "Apns-Topic" and create a second, duplicate entry
	// alongside the lowercase one.
	baseHeader := http.Header{
		"apns-expiration": []string{fmt.Sprint(request.Expiry)},
		// Priority is not a free choice: a background push must use 5, and
		// APNs rejects 10 with 400 BadPriority.
		"apns-priority": []string{common.PriorityForPushType(pushType)},
		// Required on watchOS 6+, and strongly recommended everywhere. Omitting
		// it on a background push to iOS 13+ makes APNs return 200 and then
		// silently drop the notification.
		"apns-push-type": []string{pushType},
		// This is kept in VolatileData. A PSP may need to be updated first in /addpsp to use this,
		// by setting bundleid to the bundle id of the app.
		"apns-topic": []string{bundleid},
	}

	psp := request.PSP
	http2UrlHost := common.ResolveEndpoint(psp)
	client, err := prp.GetClient(psp)
	if err != nil {
		for range request.Devtokens {
			request.ErrChan <- push.NewErrorf("Could not create a client: %v", err)
		}
		return
	}

	for i, token := range request.Devtokens {
		msgID := request.GetID(i)

		url := fmt.Sprintf("%s/3/device/%s", http2UrlHost, hex.EncodeToString(token))
		httpRequest, err := http.NewRequest("POST", url, bytes.NewReader(request.Payload))
		if err != nil {
			request.ErrChan <- push.NewError(err.Error())
			continue
		}
		// Clone rather than share: apns-id differs per device token. If apns-id
		// is omitted APNs generates one, but then it only exists in a response
		// we do not persist, which makes supporting a "did this push arrive"
		// question impossible.
		httpRequest.Header = baseHeader.Clone()
		if apnsID, idErr := newAPNSID(); idErr == nil {
			httpRequest.Header["apns-id"] = []string{apnsID}
		}

		go prp.sendRequest(wg, client, httpRequest, msgID, request.ErrChan, request.ResChan)
	}

	wg.Wait()
}

func (prp *HTTPPushRequestProcessor) sendRequest(wg *sync.WaitGroup, client HTTPClient, request *http.Request, messageID uint32, errChan chan<- push.Error, resChan chan<- *common.APNSResult) {
	defer wg.Done()

	response, err := client.Do(request)
	if err != nil {
		errChan <- push.NewConnectionError(err)
		return
	}

	defer response.Body.Close()

	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		errChan <- push.NewError(err.Error())
		return
	}

	prp.handlePushResponseBody(response, responseBody, messageID, errChan, resChan)
}

// newAPNSID returns a random RFC 4122 version 4 UUID for the apns-id header.
// APNs requires canonical form: 32 lowercase hex digits in 8-4-4-4-12 groups.
func newAPNSID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

// permanentTokenFailureReasons are the APNs `reason` values that mean this
// device token will never work again, so the subscription should be dropped.
//
// Apple's own do-not-retry list is broader (it also includes Forbidden and
// PayloadTooLarge), but those indicate a broken provider config or an oversized
// payload rather than a dead token, and unsubscribing on them would silently
// destroy good subscriptions.
//
// https://developer.apple.com/documentation/usernotifications/handling-notification-responses-from-apns
var permanentTokenFailureReasons = map[string]bool{
	// 400: the token is malformed, or belongs to the other environment
	// (sandbox token sent to production or vice versa).
	"BadDeviceToken": true,
	// 400: the token is valid but registered against a different topic.
	"DeviceTokenNotForTopic": true,
	// 410: the app was uninstalled or otherwise unregistered.
	"Unregistered": true,
	// 410: the token expired.
	"ExpiredToken": true,
}

// handle the response body of an HTTP/2 push attempt to APNs.
func (prp *HTTPPushRequestProcessor) handlePushResponseBody(response *http.Response, responseBody []byte, messageID uint32, errChan chan<- push.Error, resChan chan<- *common.APNSResult) {
	if len(responseBody) == 0 {
		// A successful push returns 200 with an empty body.
		if response.StatusCode == http.StatusOK {
			resChan <- &common.APNSResult{
				MsgID:  messageID,
				Status: common.Status0Success,
			}
			return
		}
		// A 410 with no body still means the token is dead. Apple always sends
		// a reason with it, but do not depend on that to avoid leaking a
		// subscription that will never be deliverable again.
		if response.StatusCode == http.StatusGone {
			resChan <- &common.APNSResult{
				MsgID:  messageID,
				Status: common.Status8Unsubscribe,
			}
			return
		}
		errChan <- push.NewErrorf("Unknown error. No response body, HTTP status code is %d", response.StatusCode)
		return
	}

	apnsError := new(APNSErrorResponse)
	if err := json.Unmarshal(responseBody, apnsError); err != nil {
		errChan <- push.NewErrorf("Could not parse APNs error response (HTTP %d): %v", response.StatusCode, err)
		return
	}

	if permanentTokenFailureReasons[apnsError.Reason] {
		// TODO: A 410 also carries a `timestamp` (milliseconds since the epoch)
		// recording when APNs last saw the token as invalid. Apple's guidance is
		// to keep the subscription if the device re-registered the same token
		// after that moment. Acting on it needs a reliable per-delivery-point
		// registration time, which uniqush does not track consistently yet, so
		// for now the token is dropped unconditionally. See APNSErrorResponse.
		resChan <- &common.APNSResult{
			MsgID:  messageID,
			Status: common.Status8Unsubscribe,
		}
		return
	}

	errChan <- push.NewBadNotificationWithDetails(apnsError.Reason)
}
