package http_api //nolint:revive

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/uniqush/uniqush-push/push"
	"github.com/uniqush/uniqush-push/srv/apns/common"
)

// Redirects are refused rather than followed.
//
// Everything uniqush checks about a destination happens once, before the first
// request: the scheme, the host, the shape of the URL. A redirect replays that
// request somewhere nobody named -- with the device token in the path, the
// notification in the body, the apns-topic naming the app, and, because the new
// request goes out on the same transport, an APNs client certificate available
// for the asking during the handshake.
//
// APNs does not redirect, so refusing costs nothing and closes the gap between
// "this endpoint was authorised" and "this is where the push went".

// TestTheProductionClientDoesNotFollowRedirects drives the real client factory,
// not a double.
//
// The behaviour lives in defaultClientFactory, which every other test in this
// package replaces -- so nothing else can see it. This one builds the client the
// same way GetClient does and points it at a server that tries to send it
// somewhere else.
func TestTheProductionClientDoesNotFollowRedirects(t *testing.T) {
	var elsewhereHits atomic.Int64
	elsewhere := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		elsewhereHits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer elsewhere.Close()

	for _, status := range []int{
		http.StatusMovedPermanently,  // 301
		http.StatusFound,             // 302
		http.StatusSeeOther,          // 303
		http.StatusTemporaryRedirect, // 307, the one that replays the body
		http.StatusPermanentRedirect, // 308
	} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			before := elsewhereHits.Load()

			redirector := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", elsewhere.URL+r.URL.Path)
				w.WriteHeader(status)
			}))
			defer redirector.Close()

			// Built the way GetClient builds it, so this exercises the client
			// production actually uses.
			transport := &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // a local test server with a per-run certificate
			}
			client := defaultClientFactory(transport)

			request, err := http.NewRequest(http.MethodPost,
				redirector.URL+"/3/device/"+strings.Repeat("ab", 32),
				strings.NewReader(`{"aps":{"alert":"redirect probe"}}`))
			if err != nil {
				t.Fatalf("Could not build the request: %v", err)
			}

			response, err := client.Do(request)
			if err != nil {
				t.Fatalf("Request failed: %v", err)
			}
			defer response.Body.Close()

			// The redirect comes back as the response instead of being chased.
			if response.StatusCode != status {
				t.Errorf("Expected the %d to be returned unfollowed, got HTTP %d", status, response.StatusCode)
			}
			if hits := elsewhereHits.Load() - before; hits != 0 {
				t.Errorf("The client followed the redirect: the destination nobody authorised was "+
					"contacted %d time(s).\nIt would have received the device token, the "+
					"notification, and the chance to ask for this provider's client certificate.", hits)
			}
		})
	}
}

// TestARedirectIsReportedRatherThanSwallowed checks the other half: refusing to
// follow one has to say so.
//
// Without a case of its own a 3xx falls through to "Unknown error. No response
// body, HTTP status code is 307", which tells an operator nothing about what
// happened or why uniqush declined to go along with it.
func TestARedirectIsReportedRatherThanSwallowed(t *testing.T) {
	processor := newHTTPRequestProcessor()

	errChan := make(chan push.Error, 1)
	resChan := make(chan *common.APNSResult, 1)
	request := &common.PushRequest{
		PSP:     pushServiceProvider,
		ErrChan: errChan,
		ResChan: resChan,
	}

	response := &http.Response{
		StatusCode: http.StatusTemporaryRedirect,
		Header:     http.Header{"Location": []string{"https://elsewhere.example/3/device/abc"}},
	}
	processor.handlePushResponseBody(response, nil, 1, request, nil)

	select {
	case err := <-errChan:
		for _, want := range []string{"307", "elsewhere.example", "does not follow"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("Expected the error to mention %q, got: %v", want, err)
			}
		}
	case result := <-resChan:
		t.Fatalf("A redirect was reported as a delivery result (status %v)", result.Status)
	}
}
