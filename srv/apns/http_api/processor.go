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

	// tokens caches one signed provider JWT per signing key, for providers
	// using token authentication rather than a certificate. See token.go.
	tokens     map[string]*providerToken
	tokensLock sync.RWMutex

	// now is overridden by tests that need to move through the token refresh
	// window without waiting for it.
	now func() time.Time
}

// NewRequestProcessor returns a new HTTPPushProcessor using net/http DefaultClient connection pool
func NewRequestProcessor() common.PushRequestProcessor {
	return &HTTPPushRequestProcessor{
		clients:       make(map[string]*cachedClient),
		currentKeys:   make(map[string]string),
		clientFactory: defaultClientFactory,
		tokens:        make(map[string]*providerToken),
		now:           time.Now,
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

// SetClock overrides the processor's idea of the current time.
//
// Only the provider token schedule reads it. That schedule has to sit between
// Apple's 20-minute mint floor and its 1-hour expiry, and neither boundary can
// be explored in real time, so the alternative to injecting a clock is leaving
// the two failure modes that take a provider completely offline untested.
func (prp *HTTPPushRequestProcessor) SetClock(now func() time.Time) {
	prp.tokensLock.Lock()
	defer prp.tokensLock.Unlock()
	prp.now = now
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
	conf := &tls.Config{
		// APNs has required TLS 1.2 since 2018, so nothing is lost by refusing
		// to negotiate lower with anything standing in for it.
		MinVersion: tls.VersionTLS12,
	}

	// Token authentication proves identity per request, in a JWT, so the
	// connection carries no client certificate at all. Presenting one would not
	// be harmful, but there is no certificate to present: the whole point of a
	// signing key is that the operator does not have one.
	if !common.UsesTokenAuth(psp) {
		cert, err := tls.LoadX509KeyPair(psp.FixedData["cert"], psp.FixedData["key"])
		if err != nil {
			return nil, push.NewBadPushServiceProviderWithDetails(psp, err.Error())
		}
		conf.Certificates = []tls.Certificate{cert}
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

	// The previous bucket's token, used only if Apple refuses the current one,
	// the bucket it belongs to, and the bucket the token actually sent belongs
	// to.
	var fallbackAuthorization string
	var fallbackBucket time.Time
	var signingBucket time.Time

	// The signing key's cache entry, resolved once for the whole batch.
	//
	// Resolving it means reading the .p8 off disk and parsing it, so it is done
	// here and handed down rather than looked up again per device. It used to be
	// looked up again per device, by way of the note* helpers that every
	// response called: a hundred-device batch read and parsed the key a hundred
	// and five times, on the push path, for an entry that cannot change within a
	// batch. nil for certificate auth, which has no token.
	var token *providerToken

	// Signed once per push rather than once per device. A provider token
	// authenticates the team, so every device in this request shares it, and
	// minting one each time would trip Apple's limit of one token per 20
	// minutes per key.
	if common.UsesTokenAuth(psp) {
		cached, tokenErr := prp.providerTokenFor(psp)
		if tokenErr == nil {
			token = cached
		}
		now := prp.currentTime()
		var signed string
		var bucket time.Time
		if tokenErr == nil {
			signed, bucket, tokenErr = token.token(now)
		}
		if tokenErr != nil {
			for range request.Devtokens {
				request.ErrChan <- push.NewBadPushServiceProviderWithDetails(psp, tokenErr.Error())
			}
			return
		}
		baseHeader["authorization"] = []string{authorizationHeader(signed)}
		signingBucket = bucket
	}

	// resolveTokens re-reads the token to send and the one to fall back to.
	//
	// Called again after a probe, because the answer can have changed: a refusal
	// installs the memo, and the memo steers token() to the previous bucket.
	resolveTokens := func(now time.Time) {
		if signed, bucket, err := token.token(now); err == nil {
			baseHeader["authorization"] = []string{authorizationHeader(signed)}
			signingBucket = bucket
		}

		// The previous bucket's token, carried in case Apple refuses this one
		// with TooManyProviderTokenUpdates -- which happens when it observed a
		// different token less than 20 minutes ago, and is expected whenever a
		// bucket's first push lands late. Empty once the older token has
		// expired, which the bucket length is chosen to prevent, or when that
		// bucket is itself one Apple has just refused.
		//
		// Both tokens are cached per bucket, so asking for this one does not
		// evict the one just signed: with a single slot the two calls fought
		// each other and every batch paid for two signatures.
		//
		// Its bucket is carried too. A fallback that Apple accepts is an
		// observation about *that* bucket, and recording it against the bucket
		// that was just refused would mark a token Apple has never taken as
		// confirmed -- see sendRequest.
		fallbackAuthorization = ""
		fallbackBucket = time.Time{}
		fallback, bucket, err := token.previousToken(now)
		if err != nil || fallback == "" {
			return
		}
		// Never a fallback onto the bucket already being sent. While the memo is
		// in force token() returns the previous bucket, and previousToken()
		// returns that same bucket -- so registering it here would arm a retry
		// with byte-identical bytes. A 429 would then be re-sent unchanged,
		// once per device, and the second refusal would look like a second
		// piece of information when it is the first one repeated.
		if bucket.Equal(signingBucket) {
			return
		}
		fallbackAuthorization = authorizationHeader(fallback)
		fallbackBucket = bucket
	}

	if token != nil {
		resolveTokens(prp.currentTime())
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

	// buildDeviceRequest prepares the request for one device token, or reports
	// the failure and returns false.
	buildDeviceRequest := func(i int, token []byte) (*http.Request, uint32, *push.DeliveryPoint, bool) {
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
			return nil, 0, nil, false
		}
		// Clone rather than share: apns-id differs per device token. If apns-id
		// is omitted APNs generates one, but then it only exists in a response
		// we do not persist, which makes supporting a "did this push arrive"
		// question impossible.
		httpRequest.Header = baseHeader.Clone()
		if apnsID, idErr := newAPNSID(); idErr == nil {
			httpRequest.Header["apns-id"] = []string{apnsID}
		}

		// The delivery point this push is for, when the caller supplied one.
		// A RetryError cannot be built without it, so a push whose DPList is
		// short degrades to a plain error rather than a panic.
		var deliveryPoint *push.DeliveryPoint
		if i < len(request.DPList) {
			deliveryPoint = request.DPList[i]
		}
		return httpRequest, request.GetID(i), deliveryPoint, true
	}

	// The first push of a bucket goes on its own, and the rest wait for it.
	//
	// Whether Apple will accept this bucket's token is only knowable by asking,
	// and at a bucket boundary the answer can be no. Releasing the whole batch
	// at once means every device in it asks the same question simultaneously and
	// gets the same refusal: N round trips, N 429s counted against the provider,
	// and N fallback retries, for one piece of information. A batch of a
	// thousand devices turns a single unavoidable refusal into a thousand.
	//
	// So a batch whose bucket has not been confirmed sends one request first and
	// reads the answer. Every later batch in the same bucket skips this
	// entirely, which is what keeps the cost at one probe per bucket rather than
	// one per batch.
	// Skipped for certificate auth, which has no bucket, and for a bucket
	// already known good -- there is nothing left to learn in either case.
	needsProbe := token != nil && !signingBucket.IsZero() &&
		!token.isConfirmed(signingBucket) &&
		len(request.Devtokens) > 1

	first := 0
	if needsProbe {
		// One prober per bucket, across every batch in this process.
		//
		// Checking isConfirmed alone is not enough. AddRequest starts each batch
		// in its own goroutine, so several can read an unconfirmed bucket before
		// any of them has an answer to record: each would then send its own
		// probe, and a boundary that costs one refusal would cost one per
		// concurrent batch. The memo suppressed the second probe a batch would
		// make and never the first one every other batch makes.
		probing, done := token.claimProbe(signingBucket, prp.currentTime())

		if probing {
			// Published even if the probe cannot be built, so nobody waits on an
			// answer that is never coming.
			defer func() { token.finishProbe(signingBucket, prp.currentTime()) }()

			// Device 0 is this batch's probe, and it is spoken for either way.
			//
			// Set before the build rather than after it succeeds. On the failure
			// path buildDeviceRequest has already reported the error and counted
			// the device off the WaitGroup, so leaving first at 0 would send the
			// loop below over device 0 a second time: a second error for one
			// notification, and a second Done for one Add. The counter goes
			// negative on the batch's last device and the WaitGroup panics,
			// taking the process down -- reachable from nothing worse than a
			// stored endpoint that will not build a URL.
			first = 1

			if httpRequest, msgID, deliveryPoint, ok := buildDeviceRequest(0, request.Devtokens[0]); ok {
				// Inline, not in a goroutine: the point is to have the answer
				// before the rest go out. wg was already sized for every device,
				// so this consumes one of those counts rather than adding
				// another.
				prp.sendRequest(wg, client, httpRequest, msgID, request, token, deliveryPoint,
					fallbackAuthorization, fallbackBucket, signingBucket)
			}
			token.finishProbe(signingBucket, prp.currentTime())
		} else {
			// Someone else is asking. Wait for their answer rather than sending
			// the same question, then re-read: a refusal will have installed the
			// memo, which moves this batch onto the token Apple accepted.
			//
			// Bounded, because the prober is one request to Apple and anything
			// can happen to it. Expiring here means proceeding on whatever the
			// cache says, which is no worse than never having waited.
			select {
			case <-done:
			case <-time.After(probeWaitLimit):
			}
		}

		// Re-resolve either way. For the prober the answer may have just
		// changed; for a waiter it may have changed while they waited. Off the
		// same cache entry, so neither costs a further read of the key.
		resolveTokens(prp.currentTime())
	}

	for i := first; i < len(request.Devtokens); i++ {
		httpRequest, msgID, deliveryPoint, ok := buildDeviceRequest(i, request.Devtokens[i])
		if !ok {
			continue
		}
		go prp.sendRequest(wg, client, httpRequest, msgID, request, token, deliveryPoint,
			fallbackAuthorization, fallbackBucket, signingBucket)
	}

	wg.Wait()
}

func (prp *HTTPPushRequestProcessor) sendRequest(wg *sync.WaitGroup, client HTTPClient, httpRequest *http.Request,
	messageID uint32, request *common.PushRequest, token *providerToken, deliveryPoint *push.DeliveryPoint,
	fallbackAuthorization string, fallbackBucket, signingBucket time.Time) {
	defer wg.Done()

	errChan := request.ErrChan

	response, responseBody, err := doRequest(client, httpRequest, request.Payload)
	if err != nil {
		errChan <- err
		return
	}

	// The bucket of the token this response is actually about. It changes when
	// the fallback is used, which is the whole point of tracking it separately.
	acceptedBucket := signingBucket

	// TooManyProviderTokenUpdates means Apple saw a different token from this
	// key too recently -- most often because this bucket's first push landed
	// late and the boundary followed shortly after. The token Apple did see is
	// the previous bucket's, it is still valid, and deterministic signing means
	// this process can reproduce it even if another instance sent it. Retrying
	// with it succeeds immediately rather than failing for up to 20 minutes.
	// Parsed once and carried, rather than re-unmarshalled at each decision.
	// Three passes over the same small body is not a cost worth worrying about;
	// three chances for the checks to disagree about what the response said is.
	reason := reasonOf(response, responseBody)

	if fallbackAuthorization != "" && reason == reasonTooManyProviderTokenUpdates {
		// Remember the refusal, so the rest of this bucket's pushes go straight
		// to the token Apple accepted instead of each paying for the discovery.
		//
		// Recorded against the bucket this request was signed for, not against
		// the clock now. A request signed just before a boundary can have its
		// 429 arrive just after one, and reading the clock here would blame a
		// bucket Apple has never been shown -- sending every later push straight
		// to the token that was actually rejected. The response time is still
		// what dates the refusal, since that is when the floor starts running.
		if token != nil {
			token.noteRefused(signingBucket, prp.currentTime())
		}

		retry := httpRequest.Clone(httpRequest.Context())
		retry.Body = io.NopCloser(bytes.NewReader(request.Payload))
		retry.Header["authorization"] = []string{fallbackAuthorization}

		retryResponse, retryBody, retryErr := doRequest(client, retry, request.Payload)
		if retryErr != nil {
			// Reported, not swallowed.
			//
			// Discarding it left the caller holding the *original* 429, which
			// then went through the classifier and came out as
			// TooManyProviderTokenUpdates: a twenty-minute backoff for what was
			// actually a connection failure on the second attempt. The first
			// attempt's transport errors are surfaced immediately a few lines
			// above, and there is no reason for the retry's to be treated
			// differently -- least of all by being relabelled as something with
			// a much longer delay.
			errChan <- retryErr
			return
		}
		response, responseBody = retryResponse, retryBody
		reason = reasonOf(response, responseBody)

		// Whatever the retry says, it says it about the fallback's bucket.
		acceptedBucket = fallbackBucket
	}

	// Anything that is not a mint-floor refusal means Apple took the token --
	// even a rejection of the device or the payload, which it could only reach
	// after authenticating. Recording that lets later batches in this bucket go
	// out together instead of each probing first.
	//
	// Recorded against the bucket that was actually accepted, which after a
	// fallback is the *previous* bucket rather than the one just refused.
	// Confirming the refused bucket instead looked harmless -- the push had
	// succeeded either way -- and quietly disarmed the probe: once the memo
	// expired inside that same bucket, isConfirmed answered yes for a token
	// Apple had never taken, so the next batch went out in full against it and
	// every device in it paid for the same refusal. That is precisely the
	// thundering herd probeBeforeReleasingBatch exists to prevent.
	//
	// Confirming the fallback's bucket is also what makes the memo and the probe
	// agree. While the memo holds, token() hands out the previous bucket, so
	// signingBucket *is* that bucket on every later batch and the probe is
	// correctly skipped; when the memo lapses and the current bucket comes back,
	// it is unconfirmed again and costs exactly one more probe.
	if token != nil && reason != reasonTooManyProviderTokenUpdates {
		if !acceptedBucket.IsZero() {
			token.noteAccepted(acceptedBucket)
		}
	}

	prp.handlePushResponseBody(response, responseBody, messageID, request, deliveryPoint)
}

// doRequest sends one request and reads its body.
func doRequest(client HTTPClient, httpRequest *http.Request, payload []byte) (*http.Response, []byte, push.Error) {
	if httpRequest.Body == nil && payload != nil {
		httpRequest.Body = io.NopCloser(bytes.NewReader(payload))
	}
	response, err := client.Do(httpRequest)
	if err != nil {
		return nil, nil, push.NewConnectionError(err)
	}
	defer response.Body.Close()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, nil, push.NewError(err.Error())
	}
	return response, body, nil
}

// reasonOf reads the APNs failure reason out of a response, or "" if there is
// none to read.
func reasonOf(response *http.Response, body []byte) string {
	if response == nil || response.StatusCode == http.StatusOK || len(body) == 0 {
		return ""
	}
	apnsError := new(APNSErrorResponse)
	if err := json.Unmarshal(body, apnsError); err != nil {
		return ""
	}
	return apnsError.Reason
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

// providerFailureReasons are the reasons that describe the provider's
// credentials or configuration rather than the notification or the device.
//
// These used to be reported as BadNotification, which is actively misleading:
// it points an operator at the payload when the problem is the .p8, the topic,
// or a clock. Reporting them against the provider is what FCM already does for
// THIRD_PARTY_AUTH_ERROR.
//
// https://developer.apple.com/documentation/usernotifications/handling-notification-responses-from-apns
var providerFailureReasons = map[string]string{
	// 403: the token's iat is more than an hour old. Apple ages it on its own
	// clock, so this can mean the host's clock has drifted rather than that
	// uniqush failed to refresh.
	"ExpiredProviderToken": "the provider token has expired; check the server's clock if this persists",
	// 403: signed by a key Apple does not recognise for this team, or the kid
	// header does not match the key.
	"InvalidProviderToken": "the provider token was rejected; check authkey, keyid and teamid",
	// Apple's answer when a request carries neither an acceptable client
	// certificate nor a provider token, so it reaches both kinds of provider and
	// means something different in each.
	//
	// For a certificate provider it is the ordinary case: no token was ever
	// meant to be sent, and Apple is saying the certificate did not authenticate
	// the connection either.
	//
	// For a token provider it cannot be a local signing failure -- a key that
	// will not load returns BadPushServiceProvider before anything is sent, and
	// any request that *is* sent has had the header set on it -- so the header
	// went missing in transit, stripped by something between uniqush and APNs,
	// or the request reached something that is not APNs at all.
	//
	// The message has to cover both, because this code cannot tell which
	// provider it is describing. An earlier version said "although one was
	// sent", which is simply false on the certificate path and sends an operator
	// looking for a proxy when their certificate is the problem.
	"MissingProviderToken": "APNs accepted neither a client certificate nor an authorization " +
		"header; check the certificate, or whether something between uniqush and APNs is " +
		"stripping the header",
	// 403: the certificate is not one Apple accepts for this topic.
	"BadCertificate": "the certificate is not valid for APNs",
	// 403: a sandbox certificate used against production, or the reverse.
	"BadCertificateEnvironment": "the certificate is for the other environment; check the endpoint or sandbox setting",
	"Forbidden":                 "APNs refused the credentials for this topic",
	"BadTopic":                  "the bundleid is not a topic this credential may send to",
	"TopicDisallowed":           "this credential may not send to that topic",
}

// reasonTooManyProviderTokenUpdates is Apple's answer when it saw a different
// token for this key less than 20 minutes ago.
//
// Deliberately not in providerFailureReasons. It is transient -- the floor
// always clears -- and it is recovered from before it ever reaches here, by
// retrying with the previous bucket's token; see sendRequest. Classifying it as
// a provider failure would have made every push fail for up to 20 minutes,
// which is exactly what the deterministic scheme exists to avoid. It stays in
// retryableReasons so that a provider without a usable fallback still backs off
// rather than dropping the notification.
const reasonTooManyProviderTokenUpdates = "TooManyProviderTokenUpdates"

// retryableReasons are transient conditions on Apple's side, with how long to
// wait before trying again.
//
// APNs had no retry handling at all before this: every one of these was
// reported as a bad notification and dropped. Apple does not send Retry-After,
// so the delays are ours.
var retryableReasons = map[string]time.Duration{
	// 429: this device is being pushed to too often.
	"TooManyRequests": 10 * time.Second,
	// 429: a new token arrived inside Apple's 20-minute floor, and the fallback
	// in sendRequest could not recover it. Backing off is the only option, and
	// the floor is what sets the delay.
	reasonTooManyProviderTokenUpdates: tokenMintFloor,
	// 500: Apple's own fault, and worth one more attempt.
	"InternalServerError": 10 * time.Second,
	// 503: the service is unavailable or the server is shutting down. Apple's
	// guidance is to back off and retry.
	"ServiceUnavailable": 30 * time.Second,
	"Shutdown":           30 * time.Second,
}

// interpretAPNSError turns an APNs failure reason into the right uniqush error.
//
// Returning nil means the reason is not one of these classes and the caller
// should fall back to reporting it against the notification.
func interpretAPNSError(reason string, request *common.PushRequest, deliveryPoint *push.DeliveryPoint) push.Error {
	if detail, isProviderProblem := providerFailureReasons[reason]; isProviderProblem {
		return push.NewBadPushServiceProviderWithDetails(request.PSP,
			fmt.Sprintf("APNs rejected the provider: %s (%s)", reason, detail))
	}

	if after, isRetryable := retryableReasons[reason]; isRetryable {
		// A RetryError needs all three to be re-sent. Without them the retry
		// would be dropped silently by the backend, so say what happened
		// instead of pretending it is being handled.
		if request.PSP == nil || deliveryPoint == nil || request.Notification == nil {
			return push.NewErrorf("APNs returned %s, which is retryable, but this push cannot be retried", reason)
		}
		return push.NewRetryErrorWithReason(request.PSP, deliveryPoint, request.Notification, after,
			fmt.Errorf("APNs returned %s", reason))
	}

	return nil
}

// handle the response body of an HTTP/2 push attempt to APNs.
func (prp *HTTPPushRequestProcessor) handlePushResponseBody(response *http.Response, responseBody []byte,
	messageID uint32, request *common.PushRequest, deliveryPoint *push.DeliveryPoint) {
	errChan, resChan := request.ErrChan, request.ResChan

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

	if interpreted := interpretAPNSError(apnsError.Reason, request, deliveryPoint); interpreted != nil {
		errChan <- interpreted
		return
	}

	// Whatever is left really is about this notification: BadPriority,
	// PayloadTooLarge, InvalidPushType and the rest.
	errChan <- push.NewBadNotificationWithDetails(apnsError.Reason)
}
