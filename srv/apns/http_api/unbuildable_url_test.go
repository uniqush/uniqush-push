package http_api //nolint:revive

import (
	"net/http"
	"testing"
	"time"

	"github.com/uniqush/uniqush-push/push"
	"github.com/uniqush/uniqush-push/srv/apns/common"
)

// TestAnUnbuildableURLDoesNotWedgeTheBatch guards a hang rather than a wrong
// answer.
//
// sendRequests sizes its WaitGroup for every device up front and relies on
// sendRequest to count each one off. The one path that skips sendRequest is a
// URL http.NewRequest will not accept -- and it used to skip the Done with it.
// The arithmetic then never reaches zero: wg.Wait blocks forever, the deferred
// close of ErrChan never runs, and the goroutine in push_service.go ranging
// over that channel blocks too. The push never completes and never fails; two
// goroutines are pinned for the life of the process.
//
// Nothing else can see this. A test that checks the errors sent on ErrChan
// reads them fine -- they are sent before the wait -- and then hangs on the
// close, which a test without a timeout reports as the whole package timing
// out ten minutes later rather than as this function's fault.
//
// Reachable because a provider read back from the database never passes through
// the builder that validates its endpoint, so the stored value can be anything.
func TestAnUnbuildableURLDoesNotWedgeTheBatch(t *testing.T) {
	previous := common.AllowsNonAppleEndpoints()
	common.SetAllowNonAppleEndpoints(true)
	defer common.SetAllowNonAppleEndpoints(previous)

	processor := newHTTPRequestProcessor()
	processor.clientFactory = func(*http.Transport) HTTPClient { return &countingClient{} }

	psp := buildCacheTestPSP(t, "https://relay.example", "")
	// Written straight into VolatileData, which is what unserializing a stored
	// provider does. A DEL control character survives url.Parse but is refused
	// by http.NewRequest, which is exactly the shape this path exists for.
	psp.VolatileData[common.EndpointKey] = "https://relay.example\x7f"

	errChan := make(chan push.Error, 8)
	request := &common.PushRequest{
		PSP:       psp,
		Devtokens: [][]byte{{0x01}, {0x02}, {0x03}},
		Payload:   []byte(`{"aps":{}}`),
		ErrChan:   errChan,
		ResChan:   make(chan *common.APNSResult, 8),
	}

	done := make(chan int, 1)
	go func() {
		go processor.sendRequests(request)
		count := 0
		// Ranging to completion: the close is the assertion. Draining without
		// waiting for it would pass against the bug.
		for range errChan {
			count++
		}
		done <- count
	}()

	select {
	case count := <-done:
		if count != len(request.Devtokens) {
			t.Errorf("Expected one error per device (%d), got %d", len(request.Devtokens), count)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("sendRequests never finished: the WaitGroup was sized for every device but a " +
			"device whose URL could not be built never counted itself off, so wg.Wait blocks " +
			"forever and ErrChan is never closed.")
	}
}
