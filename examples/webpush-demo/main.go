// Command webpush-demo is a small web app for exercising uniqush-push's
// webpush/unifiedpush backend end to end.
//
// It exists because that backend is the one uniqush backend you can test
// without a vendor account, a certificate or a device enrolled in someone's
// developer programme. Everything else needs credentials; this needs a browser.
//
// The demo sits between the browser and uniqush and does three things:
//
//   - registers a push service provider with uniqush at startup (/addpsp),
//     using a VAPID key pair it generates and caches on disk
//   - turns a browser or UnifiedPush registration into a uniqush subscription
//     (/subscribe)
//   - sends a test notification (/push)
//
// It is a proxy rather than letting the page call uniqush directly for two
// reasons: uniqush sends no CORS headers, and its REST API has no
// authentication, so it should not be reachable from a browser at all.
//
// This is a testing tool. Do not deploy it.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
)

var (
	listenAddr = flag.String("listen", "localhost:8080", "Address for this demo app")
	uniqushURL = flag.String("uniqush", "http://localhost:9898", "Base URL of the uniqush-push REST API")
	service    = flag.String("service", "webpushdemo", "uniqush service name")
	pushType   = flag.String("pushservicetype", "unifiedpush", "uniqush pushservicetype: unifiedpush or webpush")
	subscriber = flag.String("subscriber", "demo-user", "uniqush subscriber name")
	keyFile    = flag.String("keys", "vapid-keys.json", "Where to cache the generated VAPID key pair")
	contact    = flag.String("contact", "demo@example.org", "VAPID contact: a bare email address or an https URL")
)

// vapidKeys is the key pair identifying this application server to push
// services. It has to be stable across restarts: the public key is baked into
// every subscription a browser creates, so regenerating it silently invalidates
// every existing subscription.
type vapidKeys struct {
	Public  string `json:"vapidpublickey"`
	Private string `json:"vapidprivatekey"`
}

func loadOrCreateVAPIDKeys(path string) (*vapidKeys, error) {
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		keys := new(vapidKeys)
		if parseErr := json.Unmarshal(data, keys); parseErr != nil {
			return nil, fmt.Errorf("could not parse %s: %w", path, parseErr)
		}
		if keys.Public == "" || keys.Private == "" {
			return nil, fmt.Errorf("%s is missing a key", path)
		}
		log.Printf("Loaded VAPID keys from %s", path)
		return keys, nil

	case errors.Is(err, os.ErrNotExist):
		// The only case where generating a new pair is correct.

	default:
		// Anything else -- a permissions problem, a directory where a file was
		// expected, a transient I/O error -- must not be read as "no keys yet".
		// Falling through would mint a new pair and overwrite the existing one,
		// silently invalidating every subscription ever made against the old
		// public key. Refuse to start instead.
		return nil, fmt.Errorf("could not read %s: %w (refusing to generate new keys, "+
			"which would invalidate every existing subscription)", path, err)
	}

	private, public, genErr := webpush.GenerateVAPIDKeys()
	if genErr != nil {
		return nil, fmt.Errorf("could not generate VAPID keys: %w", genErr)
	}
	keys := &vapidKeys{Public: public, Private: private}
	encoded, encErr := json.MarshalIndent(keys, "", "  ")
	if encErr != nil {
		return nil, encErr
	}
	// 0600: the private key is a credential.
	if writeErr := os.WriteFile(path, encoded, 0600); writeErr != nil {
		return nil, fmt.Errorf("could not write %s: %w", path, writeErr)
	}
	log.Printf("Generated new VAPID keys and saved them to %s", path)
	return keys, nil
}

type demo struct {
	keys   *vapidKeys
	client *http.Client
}

// callUniqush posts form values to a uniqush endpoint and returns the body.
//
// uniqush answers most endpoints with a JSON object whose "status" is 0 on
// success, but /push in particular returns a stream of per-delivery-point
// results, so the raw body is passed through to the page. For a debugging tool,
// seeing exactly what uniqush said is more useful than a tidy abstraction.
func (d *demo) callUniqush(path string, form url.Values) (string, error) {
	endpoint := strings.TrimSuffix(*uniqushURL, "/") + path
	response, err := d.client.PostForm(endpoint, form)
	if err != nil {
		return "", fmt.Errorf("could not reach uniqush at %s: %w", endpoint, err)
	}
	return readUniqushResponse(endpoint, response)
}

// getUniqush issues a GET against a uniqush endpoint.
func (d *demo) getUniqush(path string, query url.Values) (string, error) {
	endpoint := strings.TrimSuffix(*uniqushURL, "/") + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	response, err := d.client.Get(endpoint)
	if err != nil {
		return "", fmt.Errorf("could not reach uniqush at %s: %w", endpoint, err)
	}
	return readUniqushResponse(endpoint, response)
}

// readUniqushResponse is shared by both verbs on purpose.
//
// The status check and the read error are easy to omit on one path and not the
// other, and the result is a handler that reports success to the page while
// uniqush was actually returning an error -- which is precisely the sort of
// misdirection a debugging tool must not produce.
func readUniqushResponse(endpoint string, response *http.Response) (string, error) {
	defer response.Body.Close()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		return "", fmt.Errorf("could not read the response from %s: %w", endpoint, err)
	}
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("uniqush returned HTTP %d from %s: %s",
			response.StatusCode, endpoint, strings.TrimSpace(string(body)))
	}
	return strings.TrimSpace(string(body)), nil
}

// registerPushServiceProvider tells uniqush about this application server.
//
// Called at startup. /addpsp is idempotent for an unchanged provider: the
// provider's identity is a hash of its fixed data, so re-registering the same
// VAPID public key, service and contact is a no-op.
func (d *demo) registerPushServiceProvider() error {
	body, err := d.callUniqush("/addpsp", url.Values{
		"service":         {*service},
		"pushservicetype": {*pushType},
		"vapidpublickey":  {d.keys.Public},
		"vapidprivatekey": {d.keys.Private},
		"subscriber":      {*contact},
	})
	if err != nil {
		return err
	}
	log.Printf("Registered push service provider: %s", body)
	return nil
}

type subscribeRequest struct {
	Endpoint string `json:"endpoint"`
	P256dh   string `json:"p256dh"`
	Auth     string `json:"auth"`
}

func writeJSON(w http.ResponseWriter, status int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Printf("Could not write response: %v", err)
	}
}

func writeError(w http.ResponseWriter, status int, err error) {
	log.Printf("Error: %v", err)
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

// handleConfig gives the page the VAPID public key it needs to subscribe.
//
// Only the public key. The private key never leaves this process.
func (d *demo) handleConfig(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"vapidPublicKey":  d.keys.Public,
		"service":         *service,
		"pushservicetype": *pushType,
		"subscriber":      *subscriber,
	})
}

// handleSubscribe turns a push registration into a uniqush subscription.
//
// The three values come from the client: for a browser they are produced by
// PushManager.subscribe, and for UnifiedPush by the connector library on the
// device. They are the same three values in both cases, which is the point.
func (d *demo) handleSubscribe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("use POST"))
		return
	}
	request := new(subscribeRequest)
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(request); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("could not parse the request: %w", err))
		return
	}
	switch {
	case strings.TrimSpace(request.Endpoint) == "":
		writeError(w, http.StatusBadRequest, fmt.Errorf("endpoint is required"))
		return
	case strings.TrimSpace(request.P256dh) == "":
		writeError(w, http.StatusBadRequest, fmt.Errorf("p256dh is required"))
		return
	case strings.TrimSpace(request.Auth) == "":
		writeError(w, http.StatusBadRequest, fmt.Errorf("auth is required"))
		return
	}

	body, err := d.callUniqush("/subscribe", url.Values{
		"service":         {*service},
		"subscriber":      {*subscriber},
		"pushservicetype": {*pushType},
		"endpoint":        {strings.TrimSpace(request.Endpoint)},
		"p256dh":          {strings.TrimSpace(request.P256dh)},
		"auth":            {strings.TrimSpace(request.Auth)},
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	log.Printf("Subscribed %s to %s", *subscriber, truncate(request.Endpoint, 60))
	writeJSON(w, http.StatusOK, map[string]string{"uniqush": body})
}

// handlePush sends a test notification to every subscription for the subscriber.
func (d *demo) handlePush(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("use POST"))
		return
	}
	message := strings.TrimSpace(r.FormValue("msg"))
	if message == "" {
		message = "Hello from uniqush-push at " + time.Now().Format(time.Kitchen)
	}

	// The webpush backend JSON-encodes the notification's fields, minus its own
	// uniqush.* control parameters, and that JSON is what the device decrypts.
	// The service worker in static/sw.js reads "title" and "body" out of it.
	body, err := d.callUniqush("/push", url.Values{
		"service":    {*service},
		"subscriber": {*subscriber},
		"title":      {"uniqush-push"},
		"body":       {message},
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	log.Printf("Pushed: %s", body)
	writeJSON(w, http.StatusOK, map[string]string{"uniqush": body})
}

// handleSubscriptions shows what uniqush currently has stored, which is the
// quickest way to tell whether a failed push means "not subscribed" or
// "subscribed but undeliverable".
func (d *demo) handleSubscriptions(w http.ResponseWriter, _ *http.Request) {
	body, err := d.getUniqush("/subscriptions", url.Values{
		"subscriber": {*subscriber},
		"services":   {*service},
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"uniqush": body})
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "..."
}

func main() {
	flag.Parse()

	keys, err := loadOrCreateVAPIDKeys(*keyFile)
	if err != nil {
		log.Fatalf("VAPID keys: %v", err)
	}

	d := &demo{
		keys:   keys,
		client: &http.Client{Timeout: 30 * time.Second},
	}

	if err := d.registerPushServiceProvider(); err != nil {
		log.Fatalf("Could not register with uniqush: %v\n\n"+
			"Is uniqush-push running at %s, with redis behind it?\n"+
			"See examples/webpush-demo/README.md.", err, *uniqushURL)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/config", d.handleConfig)
	mux.HandleFunc("/api/subscribe", d.handleSubscribe)
	mux.HandleFunc("/api/push", d.handlePush)
	mux.HandleFunc("/api/subscriptions", d.handleSubscriptions)
	// The service worker must be served from the root scope, otherwise it can
	// only receive pushes for /static/*.
	mux.HandleFunc("/sw.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript")
		w.Header().Set("Service-Worker-Allowed", "/")
		http.ServeFile(w, r, "static/sw.js")
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, "static/index.html")
	})

	log.Printf("Demo app on http://%s", *listenAddr)
	log.Printf("  uniqush         %s", *uniqushURL)
	log.Printf("  service         %s", *service)
	log.Printf("  pushservicetype %s", *pushType)
	log.Printf("  subscriber      %s", *subscriber)
	server := &http.Server{
		Addr:              *listenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Fatal(server.ListenAndServe())
}
