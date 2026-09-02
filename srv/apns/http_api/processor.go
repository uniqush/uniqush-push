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

// cachedClient is one pooled HTTP client, plus what is needed to retire it
// safely while pushes are using it.
type cachedClient struct {
	client HTTPClient
	// inFlight counts the batches currently borrowing this client. Guarded by
	// clientsLock.
	inFlight int
	// retired means no new borrower will be handed this client: it has been
	// superseded, or the processor is shutting down. Its connections are
	// released once the last borrower is done.
	retired bool
}

// HTTPPushRequestProcessor sends push notification requests to APNs using HTTP API
type HTTPPushRequestProcessor struct {
	clients     map[string]*cachedClient
	clientsLock sync.RWMutex
	// currentKeys maps a provider's name to the cache key it is currently
	// using, so that a provider whose destination changed can have its previous
	// client retired instead of left in the map. Guarded by clientsLock.
	currentKeys map[string]string
	// generation counts how many times this processor has been finalized.
	// Guarded by clientsLock.
	//
	// It exists because building a client is not atomic with caching it.
	// GetClient reads credential files to make a TLS configuration, and does so
	// *outside* the lock -- deliberately, since that is file I/O and holding the
	// cache shut across it would serialise every first push in the process. The
	// gap is the problem: Finalize can empty the map and return while a client
	// is being built, and the builder then inserts into a cache that shutdown
	// has already walked. That client is never marked retired, so nothing ever
	// closes its connections.
	//
	// Comparing the generation across the gap closes it. A client whose
	// generation no longer matches belongs to a lifetime that has ended: it is
	// still handed to its caller, because the push that asked for it is real and
	// in progress, but it is retired at birth and never cached, so releasing the
	// borrow closes it.
	generation    uint64
	clientFactory ClientFactory // can be overridden by test
}

// NewRequestProcessor returns a new HTTPPushProcessor using net/http DefaultClient connection pool
func NewRequestProcessor() common.PushRequestProcessor {
	return &HTTPPushRequestProcessor{
		clients:       make(map[string]*cachedClient),
		currentKeys:   make(map[string]string),
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
// The credential *files* are represented by the revision the builder recorded,
// not by hashing them here. Paths outlive the material they name: rotating a
// credential in place -- the annual APNs certificate renewal -- leaves every
// path identical and psp.Name() unchanged, so without something that tracks
// contents the cached client would go on presenting the retired certificate
// until uniqush restarted, and the failure would stay silent until the old
// certificate expired.
//
// Reading those files here instead would put up to three synchronous file reads
// on the fast path of every push, for every provider, to learn something that
// can only change at /addpsp -- a provider reaches a push by being loaded from
// the database, not by being re-read from disk. So the digest is taken once,
// when the builder has just validated the files, and stored in VolatileData;
// see common.CredentialRevision. This lookup stays in memory.
//
// A provider registered before this existed carries no revision, and keys on an
// empty string like every other such provider. That is the pre-existing
// behaviour -- its client lives until a restart -- and re-running /addpsp gives
// it one.
//
// skipverify is taken from ShouldSkipVerify rather than from the raw setting, so
// that two providers differing only in a stored flag that is being ignored share
// one client instead of pointlessly building two.
func clientCacheKey(psp *push.PushServiceProvider) string {
	return strings.Join([]string{
		psp.Name(),
		common.ResolveEndpoint(psp),
		psp.VolatileData[common.CACertKey],
		psp.VolatileData[common.CredentialRevisionKey],
		strconv.FormatBool(common.ShouldSkipVerify(psp)),
	}, "\x00")
}

// GetClient borrows the HTTP client for a provider.
//
// The returned release function must be called when the caller has finished
// with the client. Borrowing is what makes it safe to retire a superseded
// client: see retireSupersededClient for why simply closing it is not enough.
func (prp *HTTPPushRequestProcessor) GetClient(psp *push.PushServiceProvider) (HTTPClient, func(), error) {
	cacheKey := clientCacheKey(psp)
	entry, generation := prp.tryBorrow(cacheKey)
	if entry != nil {
		return entry.client, func() { prp.release(entry) }, nil
	}
	tlsClientConfig, err := createTLSConfig(psp)
	if err != nil {
		return nil, nil, fmt.Errorf("GetClient failed, couldn't create TLS config: %v", err)
	}
	// The window this hook exists to stage: everything above ran without the
	// lock, so a Finalize can complete here. nil outside the test that needs it.
	if betweenBuildingAndCaching != nil {
		betweenBuildingAndCaching()
	}
	prp.clientsLock.Lock()
	defer prp.clientsLock.Unlock()
	if entry, ok := prp.clients[cacheKey]; ok {
		// Maybe something else locked before this goroutine did.
		entry.inFlight++
		return entry.client, func() { prp.release(entry) }, nil
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
		return nil, nil, fmt.Errorf("GetClient failed, couldn't configure for http2: %v", err)
	}

	built := &cachedClient{client: prp.clientFactory(transport), inFlight: 1}

	// A Finalize ran while this was being built. Shutdown has already walked the
	// map, so caching this now would leave a client nothing will ever close.
	//
	// Handed to the caller regardless, and retired at birth: the push that asked
	// for it is real and already in progress, and failing it here would turn a
	// shutdown race into a lost notification. Releasing the borrow closes it.
	if prp.generation != generation {
		built.retired = true
		return built.client, func() { prp.release(built) }, nil
	}

	prp.retireSupersededClient(psp.Name(), cacheKey)
	prp.clients[cacheKey] = built
	prp.currentKeys[psp.Name()] = cacheKey
	return built.client, func() { prp.release(built) }, nil
}

// betweenBuildingAndCaching runs inside GetClient, after the TLS configuration
// has been built and before the cache lock is taken.
//
// A seam, nil in production. The race it stages -- Finalize completing while a
// client is mid-build -- lives entirely in that gap, and the gap exists because
// the build is deliberately done without the lock. There is no way to hold a
// goroutine there from outside: clientFactory is called under the lock, so
// blocking in it would deadlock the Finalize the test is trying to run.
var betweenBuildingAndCaching func()

// tryBorrow hands out a cached client and counts the borrow, or returns nil.
//
// It also reports the processor's current generation, so that a caller who has
// to build a client can tell whether a Finalize happened while it was building.
// Returned from here rather than read separately because this already takes the
// lock, and a second acquisition to read one integer would be the only cost on
// the path this function exists to make cheap.
func (prp *HTTPPushRequestProcessor) tryBorrow(cacheKey string) (*cachedClient, uint64) {
	prp.clientsLock.Lock()
	defer prp.clientsLock.Unlock()

	entry, ok := prp.clients[cacheKey]
	// A retired entry is normally removed from the map in the same breath as
	// being retired, so this should not find one. Checked anyway: handing out a
	// client that is on its way to being closed would produce a push failing
	// against a connection that was deliberately shut, which is a confusing
	// thing to debug and a cheap thing to rule out.
	if !ok || entry.retired {
		return nil, prp.generation
	}
	entry.inFlight++
	return entry, prp.generation
}

// release gives a borrowed client back, and closes it if it was retired while
// this caller was still using it.
func (prp *HTTPPushRequestProcessor) release(entry *cachedClient) {
	prp.clientsLock.Lock()
	defer prp.clientsLock.Unlock()

	entry.inFlight--
	if entry.retired && entry.inFlight <= 0 {
		closeIdleConnections(entry.client)
	}
}

// retireSupersededClient drops the client a provider was using before this one.
//
// The cache is keyed on the whole destination -- endpoint, CA, skipverify -- so
// that changing any of them takes effect without a restart. The cost is that
// each change mints a new entry, and without this the old one stays in the map
// with its transport and its pooled connections until Finalize. Transports here
// deliberately have no idle timeout (infrequent pushes otherwise lose their
// connection mid-flight), so nothing else would ever reclaim them: a provider
// repointed or rotated a few times a day would accumulate live connections to
// destinations uniqush no longer uses.
//
// CloseIdleConnections rather than anything more forceful, because a push may
// still be in flight on the retired client. Idle connections go now, in-flight
// requests finish and their connections close when they become idle.
//
// Callers must hold clientsLock for writing.
func (prp *HTTPPushRequestProcessor) retireSupersededClient(providerName, cacheKey string) {
	previous, ok := prp.currentKeys[providerName]
	if !ok || previous == cacheKey {
		return
	}

	stale, found := prp.clients[previous]
	if !found {
		return
	}

	// Removed from the map first, so nothing new can borrow it, and marked so
	// that whoever holds it last closes it.
	stale.retired = true
	delete(prp.clients, previous)

	// Closed here only if nobody is using it. CloseIdleConnections is exactly
	// what its name says: in golang.org/x/net/http2 it closes connections that
	// are idle *at that moment* and does not arrange for a busy one to close
	// when it drains. So calling it on a client with a push in flight does
	// nothing at all -- and since this was also the point where the client was
	// dropped from the map, that connection and its read goroutine would have
	// survived until the process exited, which is precisely the leak retiring
	// exists to prevent. Deferring to the last release is what closes it.
	if stale.inFlight <= 0 {
		closeIdleConnections(stale.client)
	}
}

// closeIdleConnections releases a client's pooled connections, if it is the
// kind of client that has any.
//
// Matched on the capability rather than on *http.Client wrapping an
// *http.Transport. That concrete pair is what production uses, but HTTPClient
// is an interface precisely so it can be substituted -- and a type switch on the
// concrete pair silently does nothing for every other implementation, so a test
// double could report that connections were released when nothing had been.
// *http.Client satisfies this interface directly and forwards to its transport.
func closeIdleConnections(client HTTPClient) {
	if closer, ok := client.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}

func defaultClientFactory(transport *http.Transport) HTTPClient {
	return &http.Client{
		Transport: transport,
		Timeout:   20 * time.Second,
		// Redirects are refused, not followed.
		//
		// APNs does not redirect, so nothing is lost -- and following one would
		// undo the endpoint policy entirely. Everything uniqush checks about a
		// destination happens once, before the first request: the scheme, the
		// host, the shape of the URL. A 307 or 308 replays the same request
		// somewhere the operator never named, carrying the device token in the
		// path, the notification in the body, and the apns-topic identifying the
		// app.
		//
		// The certificate is the worst of it. The redirected request goes out on
		// this same transport, whose TLSClientConfig carries the provider's APNs
		// client certificate, so the new host can simply ask for it during the
		// handshake -- from a server that only had to answer one push with a
		// Location header.
		//
		// ErrUseLastResponse rather than an error, so the 3xx comes back as an
		// ordinary response and handlePushResponseBody can report it against the
		// provider instead of surfacing as a transport failure with no detail.
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
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

// Finalize will shut down all of the connections owned by HTTP/2 clients for each PSP.
//
// The unlock is deferred rather than absent. It was absent: Finalize took the
// write lock and returned still holding it, so anything touching the client
// cache afterwards blocked forever. Shutdown mostly hid it, since the process
// was on its way out, but a Finalize followed by any further push -- which is
// what a test doing setup and teardown in one process does -- deadlocked.
func (prp *HTTPPushRequestProcessor) Finalize() {
	prp.clientsLock.Lock()
	defer prp.clientsLock.Unlock()

	for key, entry := range prp.clients {
		// Marked as well as closed: a push still in flight at shutdown holds a
		// borrow, and its release is what finally closes that connection.
		entry.retired = true
		closeIdleConnections(entry.client)
		delete(prp.clients, key)
	}
	prp.currentKeys = make(map[string]string)

	// Anything still being built belongs to the lifetime that just ended. See
	// the generation field.
	prp.generation++
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
	// Re-checked here, not only at /addpsp, because a provider loaded from the
	// database never passes through the builder. See common.CheckEndpointAllowed.
	if err := common.CheckEndpointAllowed(psp); err != nil {
		for range request.Devtokens {
			request.ErrChan <- push.NewBadPushServiceProviderWithDetails(psp, err.Error())
		}
		return
	}

	http2UrlHost := common.ResolveEndpoint(psp)
	client, releaseClient, err := prp.GetClient(psp)
	if err != nil {
		for range request.Devtokens {
			request.ErrChan <- push.NewErrorf("Could not create a client: %v", err)
		}
		return
	}
	// Held for the whole batch, released once every request has finished below.
	// This is what keeps a client that is superseded mid-push alive until its
	// last request drains, rather than being dropped while still in use.
	defer releaseClient()

	for i, token := range request.Devtokens {
		msgID := request.GetID(i)

		url := fmt.Sprintf("%s/3/device/%s", http2UrlHost, hex.EncodeToString(token))
		httpRequest, err := http.NewRequest("POST", url, bytes.NewReader(request.Payload))
		if err != nil {
			// Counted off explicitly. wg was sized for every device up front,
			// and sendRequest -- which is what normally calls Done -- is never
			// reached on this path. Without this the WaitGroup never reaches
			// zero: wg.Wait below blocks forever, the deferred close of
			// ErrChan never runs, and the goroutine in push_service.go ranging
			// over it blocks with it. One unbuildable URL wedges the push and
			// leaks two goroutines, silently and permanently.
			wg.Done()
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
	// Redirects are deliberately not followed -- see defaultClientFactory -- so
	// a 3xx arrives here as the response rather than as whatever the next hop
	// would have said. Reported specifically, because the alternative is the
	// "Unknown error, no response body" below, which tells an operator nothing
	// about what actually happened or why uniqush declined to go along with it.
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		errChan <- push.NewErrorf("APNs replied with HTTP %d redirecting to %q, which uniqush "+
			"does not follow: the next hop would receive the device token, the notification and "+
			"this provider's client certificate",
			response.StatusCode, response.Header.Get("Location"))
		return
	}

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
