package http_api //nolint:revive

import (
	"net/http"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/uniqush/uniqush-push/push"
)

// Concurrency coverage for the client cache.
//
// Everything the borrow/retire machinery does is about two pushes overlapping:
// inFlight counts borrowers so that a client superseded mid-push is closed by
// whoever finishes last rather than out from under them, and clientsLock is
// what keeps that count honest. Every other test in this package drives it from
// a single goroutine, in order, one call at a time -- which is the one shape
// where none of that matters.
//
// The gap is not theoretical. tryBorrow mutates entry.inFlight, so taking a
// read lock there instead of a write lock is a data race; the whole suite
// passes with that change in place, because nothing ever calls it from two
// goroutines. These tests run under -race and would not.

// TestClientCacheSurvivesConcurrentBorrowAndRetire hammers the cache from many
// goroutines while the destination moves underneath them.
//
// The repointing is what makes it interesting: it forces retireSupersededClient
// to run while borrows are outstanding, which is exactly the interleaving the
// inFlight count exists for and the one a sequential test cannot produce.
func TestClientCacheSurvivesConcurrentBorrowAndRetire(t *testing.T) {
	processor := newHTTPRequestProcessor()

	var issued []*countingClient
	var issuedLock sync.Mutex
	processor.clientFactory = func(*http.Transport) HTTPClient {
		client := &countingClient{}
		issuedLock.Lock()
		issued = append(issued, client)
		issuedLock.Unlock()
		return client
	}

	// Two destinations for one provider name, so alternating between them mints
	// and retires clients continuously.
	destinations := []*push.PushServiceProvider{
		buildCacheTestPSP(t, "https://one.example.com", ""),
		buildCacheTestPSP(t, "https://two.example.com", ""),
	}

	const goroutines = 16
	const iterations = 40

	var failures atomic.Int64
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				// Alternating on both g and i so the goroutines disagree about
				// which destination is current, which is what produces
				// overlapping borrows of a client another goroutine is retiring.
				psp := destinations[(g+i)%len(destinations)]
				client, release, err := processor.GetClient(psp)
				if err != nil {
					failures.Add(1)
					return
				}
				if client == nil {
					failures.Add(1)
					release()
					return
				}
				release()
			}
		}(g)
	}
	wg.Wait()

	if got := failures.Load(); got != 0 {
		t.Fatalf("GetClient failed %d times under concurrent use", got)
	}

	// Every borrow is matched by a release, so nothing should still be held and
	// Finalize should be able to close everything it has.
	processor.Finalize()

	processor.clientsLock.Lock()
	remaining := len(processor.clients)
	processor.clientsLock.Unlock()
	if remaining != 0 {
		t.Errorf("Finalize left %d client(s) in the cache", remaining)
	}

	// Balanced accounting: a double release drives inFlight negative and a
	// missed one pins a retired client forever, and neither shows up as an
	// error at the time. Every client that was ever issued should be closed by
	// now -- retired ones when their last borrower left, the rest at Finalize.
	issuedLock.Lock()
	defer issuedLock.Unlock()
	if len(issued) < 2 {
		t.Fatalf("Expected the repointing to mint at least two clients, got %d", len(issued))
	}
	for i, client := range issued {
		if !client.closed {
			t.Errorf("Client %d of %d was never released; a borrow outlived its release, "+
				"so inFlight never returned to zero.", i, len(issued))
		}
	}
}

// TestReleaseIsCountedUnderConcurrentBorrows checks the count itself rather
// than its consequences.
//
// A retired client must be closed exactly once, by whichever borrower happens
// to be last. With many borrowers racing to release, an unsynchronised inFlight
// decrement loses updates, and the client is either closed early -- while
// another goroutine is still using it -- or never closed at all.
func TestReleaseIsCountedUnderConcurrentBorrows(t *testing.T) {
	processor := newHTTPRequestProcessor()

	client := &countingClient{}
	processor.clientFactory = func(*http.Transport) HTTPClient { return client }

	psp := buildCacheTestPSP(t, "https://one.example.com", "")

	const borrowers = 32
	releases := make([]func(), 0, borrowers)
	for i := 0; i < borrowers; i++ {
		_, release, err := processor.GetClient(psp)
		if err != nil {
			t.Fatalf("GetClient: %v", err)
		}
		releases = append(releases, release)
	}

	// Retired while every borrow is still outstanding, so not one of them may
	// close it and the last one must.
	processor.clientsLock.Lock()
	processor.clients[clientCacheKey(psp)].retired = true
	processor.clientsLock.Unlock()

	if client.closed {
		t.Fatal("The client was closed while 32 borrowers still held it")
	}

	var wg sync.WaitGroup
	for _, release := range releases {
		wg.Add(1)
		go func(release func()) {
			defer wg.Done()
			release()
		}(release)
	}
	wg.Wait()

	if !client.closed {
		t.Error("A retired client with every borrow returned was never closed; " +
			"the inFlight count did not reach zero, which means a decrement was lost.")
	}

	processor.clientsLock.Lock()
	entry := processor.clients[clientCacheKey(psp)]
	processor.clientsLock.Unlock()
	if entry != nil && entry.inFlight != 0 {
		t.Errorf("inFlight settled at %d, expected 0", entry.inFlight)
	}
}

// TestAClientBuiltDuringFinalizeIsNotCached covers the one window where a
// client can outlive shutdown.
//
// GetClient builds its TLS configuration outside the cache lock, on purpose:
// that step reads credential files, and holding the cache shut across file I/O
// would serialise the first push of every provider in the process. The cost is
// a gap between deciding to build a client and caching it -- and Finalize can
// complete inside that gap.
//
// What used to happen then: Finalize walked the map, closed what it found and
// returned; the in-progress GetClient took the lock afterwards and inserted a
// brand-new client into a cache that shutdown had already been through. Nothing
// marked it retired and nothing would ever look at it again, so its connections
// survived for the life of the process -- the exact leak Finalize exists to
// prevent, reached by arriving one instant too late.
//
// Staged with a seam because it cannot be staged any other way: clientFactory is
// called while the lock is held, so blocking there would deadlock the Finalize
// this test needs to run.
func TestAClientBuiltDuringFinalizeIsNotCached(t *testing.T) {
	processor := newHTTPRequestProcessor()

	var issued []*countingClient
	var issuedLock sync.Mutex
	processor.clientFactory = func(*http.Transport) HTTPClient {
		client := &countingClient{}
		issuedLock.Lock()
		issued = append(issued, client)
		issuedLock.Unlock()
		return client
	}

	reached := make(chan struct{})
	proceed := make(chan struct{})
	var once sync.Once
	betweenBuildingAndCaching = func() {
		once.Do(func() {
			close(reached)
			<-proceed
		})
	}
	t.Cleanup(func() { betweenBuildingAndCaching = nil })

	psp := buildCacheTestPSP(t, "https://one.example.com", "")

	type borrow struct {
		release func()
		err     error
	}
	done := make(chan borrow, 1)
	go func() {
		_, release, err := processor.GetClient(psp)
		done <- borrow{release: release, err: err}
	}()

	// The builder is now past its TLS configuration and has not yet taken the
	// lock. Shut down underneath it.
	<-reached
	processor.Finalize()
	close(proceed)

	result := <-done
	if result.err != nil {
		t.Fatalf("GetClient failed: %v", result.err)
	}

	// Not cached: Finalize has already been through the map, so an entry added
	// now would never be visited again.
	processor.clientsLock.Lock()
	cached := len(processor.clients)
	processor.clientsLock.Unlock()
	if cached != 0 {
		t.Errorf("A client built while Finalize was running was inserted into the cache "+
			"(%d entr%s).\nShutdown has already walked the map, so nothing will ever close it.",
			cached, map[bool]string{true: "y", false: "ies"}[cached == 1])
	}

	// The push that asked for it still gets a working client -- failing it would
	// turn a shutdown race into a lost notification -- but returning the borrow
	// closes it.
	issuedLock.Lock()
	client := issued[len(issued)-1]
	issuedLock.Unlock()

	if client.closes != 0 {
		t.Errorf("The client was closed before its push finished (%d close(s))", client.closes)
	}
	result.release()
	if client.closes == 0 {
		t.Error("Returning the borrow did not close a client built during shutdown, so its " +
			"connections outlive the process's use of them")
	}
}
