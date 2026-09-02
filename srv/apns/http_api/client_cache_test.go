package http_api //nolint:revive

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/uniqush/uniqush-push/push"
	"github.com/uniqush/uniqush-push/srv/apns/apnstest"
	"github.com/uniqush/uniqush-push/srv/apns/common"
)

// Tests for the HTTP client cache, which is keyed on a provider's whole
// destination rather than on its name.
//
// The cache exists so that a push does not rebuild a TLS stack per request.
// Getting the key wrong is not a performance bug: too coarse and a provider
// keeps talking to a destination it was moved off, or with trust settings it
// was moved away from; too fine and clients accumulate. Both are silent.

// buildCacheTestPSP makes a provider with the given endpoint and CA settings.
//
// Built through the manager rather than by hand, because Name() needs the
// registered push service type and panics without it. Only VolatileData is
// adjusted afterwards, so every provider these tests build shares one name --
// which is the point: the name stays put while the destination moves.
func buildCacheTestPSP(t *testing.T, endpoint, caCert string) *push.PushServiceProvider {
	t.Helper()

	// The endpoint and CA go through the builder rather than being written into
	// VolatileData afterwards. The builder is what records the credential
	// revision, and it can only cover files it was told about -- mutating the
	// provider behind its back would leave the revision describing a
	// configuration that no longer exists, which is the failure this helper is
	// used to look for.
	psp, err := push.GetPushServiceManager().BuildPushServiceProviderFromMap(map[string]string{
		"service":          mockServiceName,
		"pushservicetype":  "apns",
		"cert":             "../apns-test/localhost.cert",
		"key":              "../apns-test/localhost.key",
		"addr":             "gateway.push.apple.com:2195",
		"skipverify":       "true",
		"bundleid":         bundleID,
		common.EndpointKey: endpoint,
		common.CACertKey:   caCert,
	})
	if err != nil {
		t.Fatalf("Could not build a provider: %v", err)
	}
	return psp
}

// TestClientCacheKeyDistinguishesAnUnreadableCAFromNoCA is the regression test
// for a silent downgrade of the trust store.
//
// FileFingerprint returns "" both for a file it cannot read and for no file
// at all. When the key carried only the digest, those two cases collided: a
// provider whose CA had been deleted, renamed or made unreadable produced the
// same key as a provider configured with no CA. If a system-roots client for
// that provider was already cached -- which is the normal state before a CA is
// added -- GetClient returned it on the fast path, before createTLSConfig had
// any chance to report the read failure.
//
// The result was uniqush quietly verifying Apple against the system trust store
// while the operator believed it was pinned to their bundle. A pinning failure
// that presents as success is worse than an outage, so this is asserted at the
// level of the key rather than left to the behaviour of the cache.
func TestClientCacheKeyDistinguishesAnUnreadableCAFromNoCA(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "not-created.pem")

	noCA := clientCacheKey(buildCacheTestPSP(t, "", ""))
	unreadableCA := clientCacheKey(buildCacheTestPSP(t, "", missing))

	if noCA == unreadableCA {
		t.Error("A provider with an unreadable CA keys the same as one with no CA at all.\n" +
			"A cached system-roots client would be handed back before the read failure could be " +
			"reported, so uniqush would fall back to the system trust store exactly when the " +
			"operator had asked for something narrower.")
	}
}

// TestClientCacheKeyChangesWhenACARotatesInPlace is the reason the digest is in
// the key at all. Rotating a bundle by writing over the file does not change
// its path, so a key built from the path alone would keep the retired CA.
func TestClientCacheKeyChangesWhenACARotatesInPlace(t *testing.T) {
	caPath := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caPath, []byte("-----BEGIN CERTIFICATE-----\nold\n"), 0o600); err != nil {
		t.Fatalf("Could not write the CA: %v", err)
	}
	before := clientCacheKey(buildCacheTestPSP(t, "", caPath))

	if err := os.WriteFile(caPath, []byte("-----BEGIN CERTIFICATE-----\nnew\n"), 0o600); err != nil {
		t.Fatalf("Could not rotate the CA: %v", err)
	}
	after := clientCacheKey(buildCacheTestPSP(t, "", caPath))

	if before == after {
		t.Error("Rotating a CA in place did not change the cache key, so uniqush would go on " +
			"trusting the retired authority until it was restarted")
	}
}

// writeCredentialPair copies the test certificate and key into dir, so a test
// can rotate them without disturbing the shared fixtures.
func writeCredentialPair(t *testing.T, dir string) (certPath, keyPath string) {
	t.Helper()

	certPath = filepath.Join(dir, "apns.cert")
	keyPath = filepath.Join(dir, "apns.key")

	for _, pair := range [][2]string{
		{"../apns-test/localhost.cert", certPath},
		{"../apns-test/localhost.key", keyPath},
	} {
		contents, err := os.ReadFile(pair[0])
		if err != nil {
			t.Fatalf("Could not read %s: %v", pair[0], err)
		}
		if err := os.WriteFile(pair[1], contents, 0o600); err != nil {
			t.Fatalf("Could not write %s: %v", pair[1], err)
		}
	}
	return certPath, keyPath
}

// pspWithCredentialsAt builds a provider using a specific certificate pair.
func pspWithCredentialsAt(t *testing.T, certPath, keyPath string) *push.PushServiceProvider {
	t.Helper()

	psp, err := push.GetPushServiceManager().BuildPushServiceProviderFromMap(map[string]string{
		"service":         mockServiceName,
		"pushservicetype": "apns",
		"cert":            certPath,
		"key":             keyPath,
		"addr":            "gateway.push.apple.com:2195",
		"skipverify":      "true",
		"bundleid":        bundleID,
	})
	if err != nil {
		t.Fatalf("Could not build a provider: %v", err)
	}
	return psp
}

// TestClientCacheKeyChangesWhenTheCertificateRotatesInPlace is the regression
// test for the annual APNs certificate renewal.
//
// An APNs certificate expires every year. The operator writes the replacement
// over the old file and re-runs /addpsp -- which is the only way to do it: the
// cert and key paths live in FixedData, so pointing at a different path produces
// a different psp.Name() and would be rejected as a different provider.
//
// That means nothing observable about the provider changes. Same name, same
// endpoint, same CA, same paths. With only the CA fingerprinted, the cache key
// was identical before and after, so uniqush kept the cached TLS config and went
// on presenting the *expired* certificate until someone restarted it.
//
// The failure is delayed and silent: the renewal appears to succeed, and pushes
// keep working right up to the moment the old certificate expires, at which
// point every push stops for a reason that has nothing to do with anything
// changed that day.
func TestClientCacheKeyChangesWhenTheCertificateRotatesInPlace(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := writeCredentialPair(t, dir)

	before := clientCacheKey(pspWithCredentialsAt(t, certPath, keyPath))

	// The renewed certificate, at the same path. Contents stand in for a real
	// re-issue; only the digest matters here.
	if err := os.WriteFile(certPath, []byte("-----BEGIN CERTIFICATE-----\nrenewed\n"), 0o600); err != nil {
		t.Fatalf("Could not rotate the certificate: %v", err)
	}
	after := clientCacheKey(pspWithCredentialsAt(t, certPath, keyPath))

	if before == after {
		t.Error("Renewing the APNs certificate in place did not change the cache key.\n" +
			"uniqush would keep presenting the expired certificate until it was restarted, and " +
			"the breakage would surface only when the old certificate expired -- long after the " +
			"renewal that was supposed to prevent it.")
	}
}

// TestClientCacheKeyChangesWhenThePrivateKeyRotates covers the other half of the
// pair. A re-issue replaces both files, and a key that no longer matches its
// certificate fails the TLS handshake rather than being reported usefully.
func TestClientCacheKeyChangesWhenThePrivateKeyRotates(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := writeCredentialPair(t, dir)

	before := clientCacheKey(pspWithCredentialsAt(t, certPath, keyPath))

	if err := os.WriteFile(keyPath, []byte("-----BEGIN PRIVATE KEY-----\nrenewed\n"), 0o600); err != nil {
		t.Fatalf("Could not rotate the private key: %v", err)
	}
	after := clientCacheKey(pspWithCredentialsAt(t, certPath, keyPath))

	if before == after {
		t.Error("Rotating the private key in place did not change the cache key")
	}
}

// TestRotatedCertificateBuildsANewClient is the rotation case at the level that
// actually matters: the cache, not the key.
//
// The key tests above would still pass if GetClient ignored the key entirely.
// This one drives the real path -- push, rotate, push -- and asserts a new
// client was built and the old one retired.
func TestRotatedCertificateBuildsANewClient(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := writeCredentialPair(t, dir)

	processor := newHTTPRequestProcessor()
	var issued []*countingClient
	processor.clientFactory = func(*http.Transport) HTTPClient {
		client := &countingClient{}
		issued = append(issued, client)
		return client
	}

	_, release, err := processor.GetClient(pspWithCredentialsAt(t, certPath, keyPath))
	if err != nil {
		t.Fatalf("The first GetClient failed: %v", err)
	}
	release()

	// A genuinely different, genuinely loadable pair, generated rather than
	// faked: createTLSConfig calls tls.LoadX509KeyPair on the way to building
	// the client, so writing nonsense over the file would make this pass for the
	// wrong reason -- the client would fail to build at all.
	newCert, newKey, err := apnstest.GenerateClientCert(t.TempDir())
	if err != nil {
		t.Fatalf("Could not generate a replacement certificate: %v", err)
	}
	for _, pair := range [][2]string{{newCert, certPath}, {newKey, keyPath}} {
		contents, readErr := os.ReadFile(pair[0])
		if readErr != nil {
			t.Fatalf("Could not read the replacement %s: %v", pair[0], readErr)
		}
		// Written over the original paths, which is the whole point: the
		// provider's identity is unchanged.
		if writeErr := os.WriteFile(pair[1], contents, 0o600); writeErr != nil {
			t.Fatalf("Could not install the renewed credential: %v", writeErr)
		}
	}

	_, releaseRenewed, err := processor.GetClient(pspWithCredentialsAt(t, certPath, keyPath))
	if err != nil {
		t.Fatalf("GetClient after the renewal failed: %v", err)
	}
	releaseRenewed()

	if len(issued) != 2 {
		t.Fatalf("Expected the renewed certificate to build a second client, got %d client(s). "+
			"The cached TLS config still holds the retired certificate.", len(issued))
	}
	if !issued[0].closed {
		t.Error("The client holding the retired certificate was not released")
	}
}

// TestClientCacheFastPathDoesNotTouchTheFilesystem is the performance contract,
// asserted rather than assumed.
//
// An earlier version of clientCacheKey hashed the CA, the certificate and the
// private key on every call -- including the cache *hit* path, which is the fast
// path of every push. That is up to three synchronous file reads per provider
// per API request, spent to learn something that can only change at /addpsp,
// since a provider reaches a push by being loaded from the database rather than
// re-read from disk.
//
// Nothing about it would ever fail a test: the behaviour was correct, only
// needlessly slow, and it would have shown up as a throughput ceiling on a busy
// installation rather than as a bug. So the absence of the reads is pinned here,
// by removing the files and requiring the key to be computed anyway.
func TestClientCacheFastPathDoesNotTouchTheFilesystem(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := writeCredentialPair(t, dir)
	caPath := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(caPath, []byte("-----BEGIN CERTIFICATE-----\nca\n"), 0o600); err != nil {
		t.Fatalf("Could not write the CA: %v", err)
	}

	psp, err := push.GetPushServiceManager().BuildPushServiceProviderFromMap(map[string]string{
		"service":          mockServiceName,
		"pushservicetype":  "apns",
		"cert":             certPath,
		"key":              keyPath,
		"addr":             "gateway.push.apple.com:2195",
		"skipverify":       "true",
		"bundleid":         bundleID,
		common.CACertKey:   caPath,
		common.EndpointKey: "https://one.example.com",
	})
	if err != nil {
		t.Fatalf("Could not build a provider: %v", err)
	}

	before := clientCacheKey(psp)

	// Every credential file is now gone. A key built by reading them would
	// change -- each digest would collapse to the empty string -- while one
	// built from the revision the builder already recorded cannot.
	for _, path := range []string{certPath, keyPath, caPath} {
		if err := os.Remove(path); err != nil {
			t.Fatalf("Could not remove %s: %v", path, err)
		}
	}

	if after := clientCacheKey(psp); after != before {
		t.Error("The cache key changed when the credential files were removed, so it is still " +
			"reading them.\nThat puts up to three synchronous file reads on the fast path of " +
			"every push, for every provider, to learn something that can only change at /addpsp.")
	}
}

// countingClient records whether its connections were released.
type countingClient struct {
	closed bool
	// closes counts every attempt, not just whether one happened.
	//
	// A boolean cannot distinguish "closed while the push was still running"
	// from "closed when the last borrow was returned", and the difference is
	// the entire behaviour retirement exists for. A test asserting only that
	// closed became true passes even when the release-time close is deleted,
	// because Finalize had already set it.
	closes int
}

func (c *countingClient) Do(*http.Request) (*http.Response, error) {
	return nil, nil //nolint:nilnil // never called; this client exists to be counted and closed
}

// CloseIdleConnections is the capability closeIdleConnections looks for, and
// the one *http.Client provides in production.
func (c *countingClient) CloseIdleConnections() {
	c.closed = true
	c.closes++
}

var _ HTTPClient = &countingClient{}

// TestGetClientRetiresTheClientItReplaces covers the leak.
//
// The cache is keyed on the destination so that repointing a provider takes
// effect without a restart. That means every endpoint change or CA rotation
// mints a new entry, and the old one used to stay in the map indefinitely:
// these transports deliberately have no idle timeout, so nothing else ever
// reclaimed them. A provider repointed a few times a day accumulated live
// connections to destinations uniqush no longer used, until the process ended.
func TestGetClientRetiresTheClientItReplaces(t *testing.T) {
	processor := newHTTPRequestProcessor()

	var issued []*countingClient
	processor.clientFactory = func(*http.Transport) HTTPClient {
		client := &countingClient{}
		issued = append(issued, client)
		return client
	}

	// Same provider, moved to a different endpoint each time. The name is
	// unchanged -- only the destination moves -- which is exactly the case the
	// key was widened to handle.
	for _, endpoint := range []string{
		"https://one.example.com",
		"https://two.example.com",
		"https://three.example.com",
	} {
		client, release, err := processor.GetClient(buildCacheTestPSP(t, endpoint, ""))
		if err != nil {
			t.Fatalf("GetClient(%s) failed: %v", endpoint, err)
		}
		_ = client
		// Released immediately: these tests model a batch that has finished, so
		// a superseded client is expected to close at once rather than waiting.
		release()
	}

	if len(issued) != 3 {
		t.Fatalf("Expected three clients to be built, got %d", len(issued))
	}

	processor.clientsLock.RLock()
	cached := len(processor.clients)
	processor.clientsLock.RUnlock()

	if cached != 1 {
		t.Errorf("Expected one cached client after two moves, got %d: superseded clients are "+
			"retained until the process exits, along with their pooled connections", cached)
	}

	for i, client := range issued[:2] {
		if !client.closed {
			t.Errorf("Client %d was superseded but its connections were never released", i)
		}
	}
	if issued[2].closed {
		t.Error("The current client was closed; only superseded ones should be")
	}
}

// TestRetiringWaitsForAPushStillInFlight is the regression test for a leak that
// hid behind a method name.
//
// CloseIdleConnections does exactly what it says: in golang.org/x/net/http2 it
// closes the connections that are idle *at that moment*. It does not mark a busy
// connection to close once it drains. So retiring a client that had a push in
// flight closed nothing -- and the same line dropped the client from the map,
// losing the only handle anyone had on it. The connection and its read goroutine
// then survived until the process exited.
//
// Repointing a provider while it is being pushed to is not exotic: /addpsp and
// /push are separate HTTP endpoints and nothing serialises them.
func TestRetiringWaitsForAPushStillInFlight(t *testing.T) {
	processor := newHTTPRequestProcessor()

	var issued []*countingClient
	processor.clientFactory = func(*http.Transport) HTTPClient {
		client := &countingClient{}
		issued = append(issued, client)
		return client
	}

	// A batch borrows the client and has not finished with it.
	_, releaseFirst, err := processor.GetClient(buildCacheTestPSP(t, "https://one.example.com", ""))
	if err != nil {
		t.Fatalf("GetClient failed: %v", err)
	}

	// The provider is repointed while that batch is still running.
	_, releaseSecond, err := processor.GetClient(buildCacheTestPSP(t, "https://two.example.com", ""))
	if err != nil {
		t.Fatalf("GetClient after the move failed: %v", err)
	}
	defer releaseSecond()

	if len(issued) != 2 {
		t.Fatalf("Expected two clients, got %d", len(issued))
	}
	if issued[0].closed {
		t.Error("The superseded client was closed while a push was still using it.\n" +
			"Its connection cannot actually close yet, so this is a close that does nothing " +
			"while the last handle to the client is dropped.")
	}

	// The batch finishes. Now, and only now, is the connection idle and closable.
	releaseFirst()

	if !issued[0].closed {
		t.Error("The superseded client was never released once its last push finished, so its " +
			"connection and read goroutine survive until the process exits")
	}
	if issued[1].closed {
		t.Error("The current client was closed; only superseded ones should be")
	}
}

// TestFinalizeWaitsForPushesStillInFlight is the same contract at shutdown.
func TestFinalizeWaitsForPushesStillInFlight(t *testing.T) {
	processor := newHTTPRequestProcessor()

	var issued []*countingClient
	processor.clientFactory = func(*http.Transport) HTTPClient {
		client := &countingClient{}
		issued = append(issued, client)
		return client
	}

	_, release, err := processor.GetClient(buildCacheTestPSP(t, "https://one.example.com", ""))
	if err != nil {
		t.Fatalf("GetClient failed: %v", err)
	}

	processor.Finalize()
	// Finalize asks the client to close, which for one with a push in flight
	// releases nothing: CloseIdleConnections closes what is idle at that
	// moment. The borrow is what closes it afterwards.
	//
	// Counted rather than checked as a boolean, because Finalize has already
	// set closed. Asserting on that alone cannot fail: the release-time close
	// could be deleted, or the retired flag never consulted, and the test would
	// still pass on the close Finalize made while the client was busy.
	duringFinalize := issued[0].closes
	if duringFinalize == 0 {
		t.Fatal("Finalize did not ask the client to close at all")
	}

	release()

	if issued[0].closes <= duringFinalize {
		t.Errorf("Returning the last borrow did not close the retired client: %d close(s) at "+
			"Finalize and %d after the release.\n"+
			"Finalize cannot close a client with a push in flight, so the release is what has "+
			"to -- otherwise a shutdown during a push leaves the connection open for the life "+
			"of the process.", duringFinalize, issued[0].closes)
	}
}

// TestGetClientKeepsServingTheSameDestination is the other half: retiring must
// not turn the cache into a one-entry cache that rebuilds on every push.
func TestGetClientKeepsServingTheSameDestination(t *testing.T) {
	processor := newHTTPRequestProcessor()

	built := 0
	processor.clientFactory = func(*http.Transport) HTTPClient {
		built++
		return &countingClient{}
	}

	for i := 0; i < 4; i++ {
		_, release, err := processor.GetClient(buildCacheTestPSP(t, "https://one.example.com", ""))
		if err != nil {
			t.Fatalf("GetClient failed on call %d: %v", i, err)
		}
		release()
	}

	if built != 1 {
		t.Errorf("Expected one client for four pushes to one destination, got %d", built)
	}
}

// TestGetClientHandlesAMoveAndBack covers repointing a provider and then
// reverting it, which is what an operator does after a failed migration or a
// staging experiment.
//
// Worth its own case because the second move returns to a key that was already
// retired once. If retirement left the bookkeeping inconsistent -- a live entry
// in clients with no matching currentKeys, or the reverse -- this is where it
// shows up, as either a stale client being reused or an entry that can never be
// retired again.
func TestGetClientHandlesAMoveAndBack(t *testing.T) {
	processor := newHTTPRequestProcessor()

	var issued []*countingClient
	processor.clientFactory = func(*http.Transport) HTTPClient {
		client := &countingClient{}
		issued = append(issued, client)
		return client
	}

	const original = "https://one.example.com"
	const moved = "https://two.example.com"

	for _, endpoint := range []string{original, moved, original} {
		client, release, err := processor.GetClient(buildCacheTestPSP(t, endpoint, ""))
		if err != nil {
			t.Fatalf("GetClient(%s) failed: %v", endpoint, err)
		}
		_ = client
		// Released immediately: these tests model a batch that has finished, so
		// a superseded client is expected to close at once rather than waiting.
		release()
	}

	// Three builds: the original, the move, and the original again -- the first
	// client was retired when the provider moved, so coming back rebuilds it
	// rather than resurrecting a closed one.
	if len(issued) != 3 {
		t.Fatalf("Expected three clients across a move and back, got %d", len(issued))
	}

	processor.clientsLock.RLock()
	cached := len(processor.clients)
	current := processor.currentKeys[buildCacheTestPSP(t, original, "").Name()]
	processor.clientsLock.RUnlock()

	if cached != 1 {
		t.Errorf("Expected one cached client after moving and returning, got %d", cached)
	}
	if want := clientCacheKey(buildCacheTestPSP(t, original, "")); current != want {
		t.Error("The provider's recorded key does not match the destination it was last used with, " +
			"so its client can never be retired")
	}
	if issued[2].closed {
		t.Error("The client for the destination the provider was returned to has been closed")
	}
}

// TestFinalizeReleasesTheLock guards a deadlock.
//
// Finalize took the write lock and returned still holding it. Shutdown hid it,
// because the process was leaving anyway -- but any client-cache access after a
// Finalize blocked forever, which is what a test doing setup and teardown in
// one process does.
func TestFinalizeReleasesTheLock(t *testing.T) {
	processor := newHTTPRequestProcessor()
	processor.clientFactory = func(*http.Transport) HTTPClient { return &countingClient{} }

	_, releaseOne, err := processor.GetClient(buildCacheTestPSP(t, "https://one.example.com", ""))
	if err != nil {
		t.Fatalf("GetClient failed: %v", err)
	}
	releaseOne()
	processor.Finalize()

	// Deadlocks rather than fails if the lock was not released. The test
	// binary's own timeout is what reports it.
	done := make(chan struct{})
	go func() {
		defer close(done)
		processor.clientsLock.Lock()
		processor.clientsLock.Unlock() //nolint:staticcheck // proving the lock is free is the point
	}()

	<-done
}
