// Command fcm-demo is a small web app for exercising uniqush-push's FCM
// backend end to end, against Google's real servers.
//
// It exists because uniqush's FCM support was rewritten for the HTTP v1 API
// after Google decommissioned the legacy endpoint, and unit tests against a
// mocked FCM cannot tell you whether Google agrees with the result. Answering
// that needs a real Firebase project, real credentials and a real registration
// token.
//
// The awkward part is the registration token, which normally means building an
// Android app. It does not have to: FCM issues registration tokens to browsers
// too, through the same v1 send API and the same "token" target. So this demo
// gets one from Chrome or Firefox in about a minute, and exercises exactly the
// code path in srv/fcm that an Android push would.
//
// It does four things:
//
//   - registers a push service provider with uniqush at startup (/addpsp),
//     from a service account JSON
//   - hands the page the Firebase web config it needs to request a token
//   - turns a registration token -- from this browser, or pasted in from an
//     Android device -- into a uniqush subscription (/subscribe)
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
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var (
	listenAddr = flag.String("listen", "localhost:8080", "Address for this demo app")
	uniqushURL = flag.String("uniqush", "http://localhost:9898", "Base URL of the uniqush-push REST API")
	service    = flag.String("service", "fcmdemo", "uniqush service name")
	pushType   = flag.String("pushservicetype", "fcm", "uniqush pushservicetype: fcm or gcm")
	subscriber = flag.String("subscriber", "demo-user", "uniqush subscriber name")
	configFile = flag.String("config", "fcm-demo.json", "Firebase settings for this demo")
)

// config is everything this demo needs from a Firebase project.
//
// It is split in two deliberately. CredentialsFile is a private key that
// authorises sending to the whole project and never leaves this process;
// everything under Web is public by design and is handed to the browser.
type config struct {
	// ProjectID is the Firebase project id, which forms part of the v1 send
	// URL. Note that this is the project *id*, not the numeric project number
	// and not the display name.
	ProjectID string `json:"projectId"`

	// CredentialsFile is a path to a service account JSON, from
	// Project settings -> Service accounts -> Generate new private key.
	// Relative paths resolve against the config file's directory.
	CredentialsFile string `json:"credentialsFile"`

	Web webConfig `json:"web"`
}

// webConfig is the browser half: the Firebase JS SDK's initialisation object,
// plus the web push certificate.
//
// All of it is public. The apiKey in particular is not a secret despite the
// name -- it identifies the project to Google's client APIs and is compiled
// into every web and mobile app Firebase ships.
type webConfig struct {
	APIKey            string `json:"apiKey"`
	AuthDomain        string `json:"authDomain"`
	ProjectID         string `json:"projectId"`
	MessagingSenderID string `json:"messagingSenderId"`
	AppID             string `json:"appId"`

	// VAPIDKey is the public "Web Push certificate" key pair from
	// Project settings -> Cloud Messaging -> Web configuration. Without it
	// getToken() fails, and its error message does not make the reason obvious.
	VAPIDKey string `json:"vapidKey"`
}

func loadConfig(path string) (*config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%s does not exist. Copy fcm-demo.example.json to %s "+
				"and fill it in; see README.md for where each value comes from", path, path)
		}
		return nil, fmt.Errorf("could not read %s: %w", path, err)
	}

	settings := new(config)
	if err = json.Unmarshal(data, settings); err != nil {
		return nil, fmt.Errorf("could not parse %s: %w", path, err)
	}

	// Check every required field up front and report all of them at once. A
	// half-filled config is the normal state while following the README, and
	// discovering the missing fields one restart at a time is tedious.
	var missing []string
	for name, value := range map[string]string{
		"projectId":             settings.ProjectID,
		"credentialsFile":       settings.CredentialsFile,
		"web.apiKey":            settings.Web.APIKey,
		"web.messagingSenderId": settings.Web.MessagingSenderID,
		"web.appId":             settings.Web.AppID,
		"web.vapidKey":          settings.Web.VAPIDKey,
	} {
		if strings.TrimSpace(value) == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("%s is missing: %s", path, strings.Join(missing, ", "))
	}

	// Resolve the credentials path relative to the config file, so the config
	// can name a service account sitting next to it.
	if !filepath.IsAbs(settings.CredentialsFile) {
		settings.CredentialsFile = filepath.Join(filepath.Dir(path), settings.CredentialsFile)
	}
	absolute, err := filepath.Abs(settings.CredentialsFile)
	if err != nil {
		return nil, fmt.Errorf("could not resolve credentialsFile: %w", err)
	}
	settings.CredentialsFile = absolute

	// uniqush reads this file itself, in its own process and possibly as
	// another user, so a path this process cannot open is worth catching here
	// rather than as an opaque /addpsp failure.
	//
	// Opened rather than stat'd, because os.Stat succeeds on a file this process
	// has no permission to read -- which is the failure this check exists for,
	// and the one a stat would wave through. srv/fcm/push_service.go makes the
	// same point about the same file, and this contradicted it.
	credentials, err := os.Open(settings.CredentialsFile)
	if err != nil {
		return nil, fmt.Errorf("credentialsFile %s: %w", settings.CredentialsFile, err)
	}
	credentials.Close()

	// Fill in the two fields that are derivable, since getting them subtly
	// wrong produces confusing browser-side failures.
	if settings.Web.ProjectID == "" {
		settings.Web.ProjectID = settings.ProjectID
	}
	if settings.Web.AuthDomain == "" {
		settings.Web.AuthDomain = settings.ProjectID + ".firebaseapp.com"
	}
	return settings, nil
}

type demo struct {
	config *config
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

// registerPushServiceProvider tells uniqush which Firebase project to send to.
//
// Called at startup. /addpsp is idempotent for an unchanged provider: a
// provider's identity is a hash of its fixed data, and for fcm that is the
// service name alone, so re-running this with rotated credentials updates the
// existing provider in place rather than creating a second one.
func (d *demo) registerPushServiceProvider() error {
	body, err := d.callUniqush("/addpsp", url.Values{
		"service":         {*service},
		"pushservicetype": {*pushType},
		"projectid":       {d.config.ProjectID},
		"credentialsfile": {d.config.CredentialsFile},
	})
	if err != nil {
		return err
	}
	log.Printf("Registered push service provider: %s", body)
	return nil
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

// handleConfig gives the page the Firebase web config.
//
// Only the Web half. The service account never leaves this process -- it
// authorises sending to every device in the project.
func (d *demo) handleConfig(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"firebase":        d.config.Web,
		"service":         *service,
		"pushservicetype": *pushType,
		"subscriber":      *subscriber,
	})
}

type subscribeRequest struct {
	RegID string `json:"regid"`
}

// handleSubscribe turns a registration token into a uniqush subscription.
//
// The token comes from the client: from getToken() in this browser, or pasted
// in from an Android app. uniqush does not care which -- both are opaque FCM
// registration tokens for an app instance in this project, and both are sent
// to identically.
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
	regID := strings.TrimSpace(request.RegID)
	if regID == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("regid is required"))
		return
	}

	body, err := d.callUniqush("/subscribe", url.Values{
		"service":         {*service},
		"subscriber":      {*subscriber},
		"pushservicetype": {*pushType},
		"regid":           {regID},
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	log.Printf("Subscribed %s with token %s", *subscriber, truncate(regID, 24))
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

	// Every field that is not a uniqush.* control parameter becomes a key in
	// the v1 message's "data" map, which the service worker reads.
	//
	// A data-only message rather than a "notification" one is the deliberate
	// choice: it is delivered to the app's own handler on every platform, so
	// what arrives is evidence that uniqush's payload came through intact,
	// rather than something the OS rendered on its own.
	form := url.Values{
		"service":    {*service},
		"subscriber": {*subscriber},
		"title":      {"uniqush-push"},
		"body":       {message},
		"sentAt":     {time.Now().Format(time.RFC3339)},
	}
	if group := strings.TrimSpace(r.FormValue("msggroup")); group != "" {
		// Becomes android.collapse_key.
		form.Set("msggroup", group)
	}
	if ttl := strings.TrimSpace(r.FormValue("ttl")); ttl != "" {
		// Becomes android.ttl, as a duration string.
		form.Set("ttl", ttl)
	}

	body, err := d.callUniqush("/push", form)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	log.Printf("Pushed: %s", body)
	writeJSON(w, http.StatusOK, map[string]string{"uniqush": body})
}

// handlePreview shows the JSON uniqush would send to FCM, without sending it.
//
// /previewpush needs no subscription and no device, so it is the fastest way to
// check that a payload change produces the v1 message body you expected.
func (d *demo) handlePreview(w http.ResponseWriter, r *http.Request) {
	message := strings.TrimSpace(r.FormValue("msg"))
	if message == "" {
		message = "Hello from uniqush-push"
	}
	body, err := d.callUniqush("/previewpush", url.Values{
		"service":         {*service},
		"pushservicetype": {*pushType},
		"title":           {"uniqush-push"},
		"body":            {message},
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
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

	settings, err := loadConfig(*configFile)
	if err != nil {
		log.Fatalf("Config: %v", err)
	}

	d := &demo{
		config: settings,
		client: &http.Client{Timeout: 30 * time.Second},
	}

	if err := d.registerPushServiceProvider(); err != nil {
		log.Fatalf("Could not register with uniqush: %v\n\n"+
			"Is uniqush-push running at %s, with redis behind it?\n"+
			"See examples/fcm-demo/README.md.", err, *uniqushURL)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/config", d.handleConfig)
	mux.HandleFunc("/api/subscribe", d.handleSubscribe)
	mux.HandleFunc("/api/push", d.handlePush)
	mux.HandleFunc("/api/preview", d.handlePreview)
	mux.HandleFunc("/api/subscriptions", d.handleSubscriptions)

	// The Firebase SDK looks for its service worker at exactly this path in the
	// root scope, and will not find it under /static/.
	mux.HandleFunc("/firebase-messaging-sw.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript")
		w.Header().Set("Service-Worker-Allowed", "/")
		http.ServeFile(w, r, "static/firebase-messaging-sw.js")
	})
	// The service worker needs the same config the page has, and cannot ask an
	// API for it before Firebase initialises, so it is served as JS.
	mux.HandleFunc("/firebase-config.js", func(w http.ResponseWriter, _ *http.Request) {
		encoded, err := json.Marshal(d.config.Web)
		if err != nil {
			http.Error(w, "could not encode config", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/javascript")
		fmt.Fprintf(w, "self.FIREBASE_CONFIG = %s;\n", encoded)
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
	log.Printf("  firebase project %s", settings.ProjectID)
	server := &http.Server{
		Addr:              *listenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Fatal(server.ListenAndServe())
}
