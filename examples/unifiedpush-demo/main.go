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

// Command unifiedpush-demo drives one UnifiedPush message all the way through
// uniqush-push and back out again, then reports what happened on the wire.
//
// UnifiedPush has three parties. The application server (uniqush) encrypts a
// payload and POSTs it to an endpoint. The distributor (ntfy, NextPush, Sunup,
// ...) owns that endpoint and forwards what arrives to the device. The
// application holds the subscription keys and is the only party that can read
// the payload.
//
// This program plays the first and third roles and talks to a real distributor
// for the second, so a run exercises every leg: it generates subscription keys
// the way a UnifiedPush connector library does, registers them with uniqush,
// asks uniqush to push, waits for the message to come back out of the
// distributor, and decrypts it. A run that ends in the original text proves the
// whole path, not just that uniqush got a 2xx.
//
// The webpush-demo next door covers the same backend from a browser. This one
// needs no browser, no vendor account and, with -distributor local, no network.
//
// This is a testing tool. Do not deploy it.
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
)

// options is what the flags add up to. It is a struct rather than a long
// parameter list because device mode adds a third way to run, and the
// combinations matter more than any single value.
type options struct {
	uniqushURL      string
	distributorURL  string
	service         string
	subscriber      string
	message         string
	raw             bool
	pushServiceType string
	timeout         time.Duration
	keep            bool

	// The subscription from a real device, when there is one. See deviceMode.
	endpoint string
	p256dh   string
	auth     string
}

// deviceMode reports whether the demo was given a real device's subscription
// rather than generating one. In that mode it cannot watch the delivery or
// decrypt anything -- it has no private key, and the phone is the receiver --
// so the run ends at the push and the phone is the result.
func (o *options) deviceMode() bool {
	return o.endpoint != ""
}

func main() {
	var opts options
	flag.StringVar(&opts.uniqushURL, "uniqush", "http://localhost:9898", "Base URL of the uniqush-push REST API.")
	flag.StringVar(&opts.distributorURL, "distributor", "https://ntfy.sh",
		"Base URL of a UnifiedPush distributor's push server, or \"local\" to use the demo's own. Ignored in device mode.")
	flag.StringVar(&opts.service, "service", "updemo", "uniqush service name.")
	flag.StringVar(&opts.subscriber, "subscriber", "unifiedpush-demo", "uniqush subscriber name.")
	flag.StringVar(&opts.message, "message", "Hello from uniqush-push over UnifiedPush.", "Message to send.")
	flag.BoolVar(&opts.raw, "raw", false,
		"Send the message as a verbatim body (uniqush.payload.webpush) instead of letting uniqush build JSON.")
	flag.StringVar(&opts.pushServiceType, "pushservicetype", "unifiedpush",
		"Which of the backend's two names to register under: unifiedpush or webpush.")
	flag.DurationVar(&opts.timeout, "timeout", 30*time.Second, "How long to wait for the message to come back.")
	flag.BoolVar(&opts.keep, "keep", false, "Leave the subscription in place instead of unsubscribing at the end.")

	flag.StringVar(&opts.endpoint, "endpoint", "",
		"Push a real device's subscription instead of generating one. Give -p256dh and -auth too.")
	flag.StringVar(&opts.p256dh, "p256dh", "", "The device's public key, with -endpoint.")
	flag.StringVar(&opts.auth, "auth", "", "The device's auth secret, with -endpoint.")
	testPage := flag.String("test-page", "",
		"Take -endpoint, -p256dh and -auth from a UnifiedPush test-page link (the one UP-Example shows), "+
			"instead of typing all three.")
	flag.Parse()

	if *testPage != "" {
		if err := opts.applyTestPageURL(*testPage); err != nil {
			fmt.Fprintf(os.Stderr, "\nFAILED: %v\n", err)
			os.Exit(1)
		}
	}
	if err := opts.validate(); err != nil {
		fmt.Fprintf(os.Stderr, "\nFAILED: %v\n", err)
		os.Exit(1)
	}

	if err := run(&opts); err != nil {
		fmt.Fprintf(os.Stderr, "\nFAILED: %v\n", err)
		os.Exit(1)
	}
}

// applyTestPageURL reads a subscription out of the link UP-Example puts on its
// registration screen:
//
//	https://unifiedpush.org/test_wp.html#endpoint=...&p256dh=...&auth=...
//
// The app has no copy button for the three values individually, but it does
// make that link shareable, so this is the least error-prone way to get a
// phone's subscription onto a laptop. The values live in the fragment, which
// means they never leave the browser -- and also that they are not in anyone's
// server logs, so the link is safe to send yourself but not to publish.
func (o *options) applyTestPageURL(link string) error {
	parsed, err := url.Parse(strings.TrimSpace(link))
	if err != nil {
		return fmt.Errorf("-test-page is not a URL: %w", err)
	}
	fragment := parsed.Fragment
	if fragment == "" {
		return errors.New("-test-page has no #endpoint=...&p256dh=...&auth=... fragment")
	}
	values, err := url.ParseQuery(fragment)
	if err != nil {
		return fmt.Errorf("could not read the -test-page fragment: %w", err)
	}
	for name, target := range map[string]*string{
		"endpoint": &o.endpoint,
		"p256dh":   &o.p256dh,
		"auth":     &o.auth,
	} {
		value := values.Get(name)
		if value == "" {
			return fmt.Errorf("-test-page is missing %s", name)
		}
		*target = value
	}
	return nil
}

// validate catches the mistakes worth catching before anything is registered.
func (o *options) validate() error {
	given := 0
	for _, value := range []string{o.endpoint, o.p256dh, o.auth} {
		if value != "" {
			given++
		}
	}
	if given != 0 && given != 3 {
		return errors.New("-endpoint, -p256dh and -auth go together: give all three, or none to have the demo generate a subscription")
	}
	if !o.deviceMode() {
		return nil
	}
	// uniqush checks these too, but a length mismatch here means a value was
	// mistyped or truncated on its way off the phone, and saying so now is
	// kinder than a rejected /subscribe.
	for _, key := range []struct {
		name     string
		value    string
		expected int
	}{
		{"p256dh", o.p256dh, 65},
		{"auth", o.auth, 16},
	} {
		decoded, err := decodeKey(key.value)
		if err != nil {
			return fmt.Errorf("-%s is not valid base64: %w", key.name, err)
		}
		if len(decoded) != key.expected {
			return fmt.Errorf("-%s decodes to %d bytes, expected %d -- it looks truncated",
				key.name, len(decoded), key.expected)
		}
	}
	return nil
}

// decodeKey accepts either base64 alphabet, padded or not. The UnifiedPush
// connector emits raw-url; other libraries differ.
func decodeKey(value string) ([]byte, error) {
	padded := value
	if remainder := len(padded) % 4; remainder != 0 {
		padded += "===="[:4-remainder]
	}
	if decoded, err := base64.URLEncoding.DecodeString(padded); err == nil {
		return decoded, nil
	}
	return base64.StdEncoding.DecodeString(padded)
}

func run(opts *options) error {
	ctx, cancel := context.WithTimeout(context.Background(), opts.timeout)
	defer cancel()

	uniqush := &uniqushClient{baseURL: strings.TrimRight(opts.uniqushURL, "/"), client: &http.Client{Timeout: 15 * time.Second}}
	if err := uniqush.version(ctx); err != nil {
		return fmt.Errorf("uniqush-push is not answering at %s: %w", opts.uniqushURL, err)
	}

	// The subscription is either a real device's, passed in, or one the demo
	// generates and then watches. Only the second can prove delivery; the first
	// can only prove uniqush accepted and sent, and the phone shows the rest.
	var (
		sub      *subscription
		dist     distributor
		endpoint = opts.endpoint
		p256dh   = opts.p256dh
		auth     = opts.auth
	)

	if opts.deviceMode() {
		step(1, "Using the subscription from your device")
		detail("endpoint", endpoint)
		detail("p256dh", p256dh)
		detail("auth", auth)
		note("the private key stayed on the device, so this run ends at the push: " +
			"the phone is what tells you it worked")
	} else {
		if opts.distributorURL == "local" || opts.distributorURL == "" {
			dist = newLocalDistributor()
		} else {
			dist = newNtfyDistributor(opts.distributorURL)
		}
		defer dist.Close()

		// 1. The device generates its own key material. uniqush never sees the
		//    private key, which is why the distributor cannot read the payload
		//    it is carrying.
		step(1, "Generating subscription keys, as a UnifiedPush connector does on the device")
		generated, err := newSubscription()
		if err != nil {
			return err
		}
		sub = generated
		p256dh, auth = sub.P256dh, sub.Auth
		detail("p256dh", p256dh)
		detail("auth", auth)

		// 2. The user's chosen distributor hands out an endpoint.
		step(2, "Registering with "+dist.Name())
		registered, err := dist.Register(ctx)
		if err != nil {
			return err
		}
		endpoint = registered
		detail("endpoint", endpoint)
	}

	if isLoopback(endpoint) {
		note("this endpoint is not globally routable, so uniqush needs " +
			"allow_private_addresses=true in the [" + opts.pushServiceType + "] config section")
	}

	// 3. The application server needs a VAPID identity. Nothing issues it: it is
	//    a key pair the server generates for itself, which is what makes this
	//    the one uniqush backend with no vendor account behind it.
	step(3, "Registering a push service provider with uniqush (/addpsp)")
	vapidPrivate, vapidPublic, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		return fmt.Errorf("generating VAPID keys: %w", err)
	}
	pspResponse, err := uniqush.post(ctx, "/addpsp", url.Values{
		"service":         {opts.service},
		"pushservicetype": {opts.pushServiceType},
		"vapidpublickey":  {vapidPublic},
		"vapidprivatekey": {vapidPrivate},
		"subscriber":      {"unifiedpush-demo@example.org"},
		// The demo mints a fresh key pair every run, so replace the previous
		// provider rather than accumulating one per run.
		"replace": {"true"},
	})
	if err != nil {
		return err
	}
	detail("pushservicetype", opts.pushServiceType)
	detail("provider", pspResponse.Details.PushServiceProvider)

	// 4. The app hands the endpoint and its public keys to the application
	//    server. This is the only step where the three parties meet.
	step(4, "Subscribing the endpoint (/subscribe)")
	subscribeParams := url.Values{
		"service":         {opts.service},
		"subscriber":      {opts.subscriber},
		"pushservicetype": {opts.pushServiceType},
		"endpoint":        {endpoint},
		"p256dh":          {p256dh},
		"auth":            {auth},
	}
	subscribeResponse, err := uniqush.post(ctx, "/subscribe", subscribeParams)
	if err != nil {
		return err
	}
	detail("delivery point", subscribeResponse.Details.DeliveryPoint)

	// A device's subscription is not the demo's to delete: the phone still has
	// it, and the point of a device test is usually to push to it again.
	if !opts.keep && !opts.deviceMode() {
		defer func() {
			// Not ctx: this runs on the way out, including after ctx has
			// expired, and cleaning up is the whole point. Its own short
			// deadline instead, so a uniqush that has stopped answering delays
			// the exit by seconds rather than hanging on it.
			cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancelCleanup()
			if _, unsubscribeErr := uniqush.post(cleanupCtx, "/unsubscribe", subscribeParams); unsubscribeErr != nil {
				fmt.Fprintf(os.Stderr, "warning: could not unsubscribe: %v\n", unsubscribeErr)
			}
		}()
	}

	// 5. Push. uniqush encrypts to the subscription keys and POSTs the result.
	step(5, "Pushing (/push)")
	pushParams := url.Values{"service": {opts.service}, "subscriber": {opts.subscriber}}
	var expected string
	if opts.raw {
		pushParams.Set("uniqush.payload.webpush", opts.message)
		expected = opts.message
	} else {
		pushParams.Set("msg", opts.message)
		// uniqush marshals the non-reserved parameters as a JSON object.
		encoded, marshalErr := json.Marshal(map[string]string{"msg": opts.message})
		if marshalErr != nil {
			return marshalErr
		}
		expected = string(encoded)
	}
	pushResponse, err := uniqush.post(ctx, "/push", pushParams)
	if err != nil {
		return err
	}
	if pushResponse.SuccessCount != 1 {
		// A push the push server answered with a retryable status is neither a
		// success nor a failure in this response: uniqush has queued it and will
		// try again on its own schedule, and only the log knows why. Saying so
		// beats reporting "0 successes, 0 failures" and leaving the reader to
		// guess which of the three outcomes they got.
		if pushResponse.FailureCount == 0 {
			return fmt.Errorf("the push server did not accept the message and uniqush has queued a retry.\n"+
				"       The reason is in the uniqush log, on the line for this push:\n"+
				"         [Push][Info] ... Retry after 1m0s: \"push server returned HTTP <status>: <response body>\"\n"+
				"       On ntfy.sh, HTTP 507 code 50701 means the topic has no registered subscriber; %s",
				topicLengthHint(endpoint))
		}
		return fmt.Errorf("uniqush reported %d successes and %d failures: %s",
			pushResponse.SuccessCount, pushResponse.FailureCount, pushResponse.failureSummary())
	}
	detail("uniqush", "accepted by the push server")

	if opts.deviceMode() {
		reportDevice(opts, expected)
		return nil
	}

	// 6. The delivery leg. Everything above could pass while the message went
	//    nowhere; this is the part that cannot.
	step(6, "Waiting for the distributor to deliver it")
	var body []byte
	select {
	case body = <-dist.Deliveries():
	case <-ctx.Done():
		return fmt.Errorf("no message arrived within %s", opts.timeout)
	}
	if body == nil {
		return errors.New("the distributor closed the delivery stream without a message")
	}
	detail("received", fmt.Sprintf("%d bytes", len(body)))

	// 7. Decrypt, as the app on the device would.
	step(7, "Decrypting (RFC 8291)")
	plaintext, header, err := sub.decrypt(body)
	if err != nil {
		return fmt.Errorf("decrypting what the distributor delivered: %w", err)
	}

	report(body, header, plaintext, expected)
	if string(plaintext) != expected {
		return fmt.Errorf("payload came back changed:\n  sent: %q\n  got:  %q", expected, plaintext)
	}
	return nil
}

// reportDevice ends a device-mode run. There is nothing to verify here, so it
// says what to look for on the phone and how to send again -- the subscription
// is still registered, which is the whole reason to test this way.
func reportDevice(opts *options, sent string) {
	fmt.Printf("\n%s\n", strings.Repeat("-", 72))
	fmt.Println("Sent to the device")
	fmt.Printf("  payload              %s\n", truncate(sent, 52))
	fmt.Println("\nNow look at the phone. UP-Example posts the decrypted body as a notification.")
	fmt.Println("  a notification with that text   the whole path works, encryption included")
	fmt.Println("  \"Could not decrypt content.\"    it arrived, but RFC 8291 decryption failed")
	fmt.Println("  nothing at all                  it never arrived; check the distributor app")
	fmt.Printf("%s\n", strings.Repeat("-", 72))
	fmt.Printf("\nThe subscription is still registered, so you can send again without re-running:\n\n")
	fmt.Printf("  curl %s/push -d service=%s -d subscriber=%s \\\n       --data-urlencode 'msg=another one'\n",
		strings.TrimRight(opts.uniqushURL, "/"), opts.service, opts.subscriber)
	fmt.Printf("\nUP-Example renders 'title=...&message=...' as a titled notification, so:\n\n")
	fmt.Printf("  curl %s/push -d service=%s -d subscriber=%s \\\n"+
		"       --data-urlencode 'uniqush.payload.webpush=title=uniqush&message=hello from the server'\n",
		strings.TrimRight(opts.uniqushURL, "/"), opts.service, opts.subscriber)
}

// report prints what was on the wire, which is the part worth keeping from a
// run: the numbers are what the UnifiedPush server spec constrains.
func report(body []byte, header *messageHeader, plaintext []byte, expected string) {
	fmt.Printf("\n%s\n", strings.Repeat("-", 72))
	fmt.Println("On the wire")
	fmt.Printf("  POST body            %d bytes (UnifiedPush allows 1-4096)\n", len(body))
	fmt.Printf("  Content-Encoding     aes128gcm\n")
	fmt.Printf("  record size (rs)     %d\n", header.RecordSize)
	fmt.Printf("  salt                 %s\n", base64.RawURLEncoding.EncodeToString(header.Salt))
	fmt.Printf("  server public key    %s\n", base64.RawURLEncoding.EncodeToString(header.KeyID))
	fmt.Printf("  ciphertext           %d bytes, padded to fill the record\n", len(body)-headerLength)
	fmt.Println("\nDecrypted")
	fmt.Printf("  %s\n", plaintext)
	fmt.Printf("%s\n", strings.Repeat("-", 72))

	if string(plaintext) == expected {
		fmt.Println("\nOK: the payload uniqush sent is the payload the device read.")
	}
}

// topicLengthHint explains the ntfy.sh 507 for the endpoint actually in use,
// since the usual cause is a topic that is not 14 characters long.
func topicLengthHint(endpoint string) string {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "ntfy only grants that to topics named \"up\" plus 12 characters."
	}
	topic := strings.TrimPrefix(parsed.Path, "/")
	if len(topic) == topicLength {
		return fmt.Sprintf("topic %q is the right shape, so this is a genuine rate limit or outage.", topic)
	}
	return fmt.Sprintf("topic %q is %d characters and ntfy only grants a rate visitor to 14-character \"up\" topics.",
		topic, len(topic))
}

func isLoopback(endpoint string) bool {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return false
	}
	host := parsed.Hostname()
	return host == "localhost" || strings.HasPrefix(host, "127.") || host == "::1"
}

func step(n int, what string) {
	fmt.Printf("\n%d. %s\n", n, what)
}

func detail(label, value string) {
	fmt.Printf("   %-16s %s\n", label, truncate(value, 96))
}

func note(what string) {
	fmt.Printf("   note: %s\n", what)
}

func truncate(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return s[:limit-3] + "..."
}

// uniqushClient is a minimal client for the uniqush REST API.
//
// The API has no authentication, so this only ever points at a uniqush bound to
// localhost. See docs/api.md.
type uniqushClient struct {
	baseURL string
	client  *http.Client
}

// uniqushResponse covers the fields this demo reads from any of the endpoints.
type uniqushResponse struct {
	Type         string `json:"type"`
	Status       int    `json:"status"`
	SuccessCount int    `json:"successCount"`
	FailureCount int    `json:"failureCount"`
	Details      struct {
		Code                string `json:"code"`
		Details             string `json:"details"`
		PushServiceProvider string `json:"pushServiceProvider"`
		DeliveryPoint       string `json:"deliveryPoint"`
	} `json:"details"`
	FailureDetails []struct {
		Code    string `json:"code"`
		Details string `json:"details"`
	} `json:"failureDetails"`
}

func (r *uniqushResponse) failureSummary() string {
	if len(r.FailureDetails) == 0 {
		return "no details; check the uniqush log"
	}
	parts := make([]string, 0, len(r.FailureDetails))
	for _, failure := range r.FailureDetails {
		parts = append(parts, strings.TrimSpace(failure.Code+" "+failure.Details))
	}
	return strings.Join(parts, "; ")
}

func (c *uniqushClient) post(ctx context.Context, path string, params url.Values) (*uniqushResponse, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, strings.NewReader(params.Encode()))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	response, err := c.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	defer response.Body.Close()

	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	var parsed uniqushResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("%s returned HTTP %d with an unreadable body: %s", path, response.StatusCode, truncate(string(body), 200))
	}
	// uniqush answers 200 with status != 0 for application-level failures, so
	// the HTTP status is not enough on its own.
	if parsed.Status != 0 {
		detail := strings.TrimSpace(parsed.Details.Code + " " + parsed.Details.Details)
		if detail == "" {
			detail = truncate(string(body), 200)
		}
		return nil, fmt.Errorf("%s: %s", path, detail)
	}
	return &parsed, nil
}

func (c *uniqushClient) version(ctx context.Context) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/version", nil)
	if err != nil {
		return err
	}
	response, err := c.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("/version returned HTTP %d", response.StatusCode)
	}
	return nil
}
