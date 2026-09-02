/*
 * Copyright 2013-2026 Uniqush Contributors.
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

// Package apnstest provides an APNs simulator: an HTTP/2 server that enforces
// Apple's documented request contract and answers with Apple's documented
// responses.
//
// It exists because uniqush had no way to test the HTTP/2 push path end to end.
// The older uniqush/apns-simulator speaks the binary protocol, which Apple
// switched off on 2021-03-31, so it could only ever exercise the code path that
// no longer delivers anything.
//
// The design goal is that passing means something. A permissive mock that
// returns 200 for any request would have accepted every bug the HTTP/2 repairs
// fixed: a missing apns-push-type, a background push at priority 10, a topic
// header sent twice under two spellings. So this server rejects each of those
// the way Apple does, and additionally records them as conformance violations,
// because a test asserting only on the push result would report "the push
// failed" without saying why.
//
// It is not a complete APNs. It does not authenticate, it stores nothing, and
// it never delivers anything to a device. What it does check is the shape of
// what uniqush puts on the wire.
package apnstest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// MaxPayloadSize is the HTTP/2 limit. Apple allows 5120 for VoIP pushes, which
// SetMaxPayloadSize exists to model.
const MaxPayloadSize = 4096

// Reasons APNs returns in the body of a failed push. Only the ones this server
// produces or that tests set up deliberately.
//
// https://developer.apple.com/documentation/usernotifications/handling-notification-responses-from-apns
const (
	ReasonBadDeviceToken         = "BadDeviceToken"
	ReasonBadExpirationDate      = "BadExpirationDate"
	ReasonBadMessageID           = "BadMessageId"
	ReasonBadPath                = "BadPath"
	ReasonBadPriority            = "BadPriority"
	ReasonDeviceTokenNotForTopic = "DeviceTokenNotForTopic"
	ReasonDuplicateHeaders       = "DuplicateHeaders"
	ReasonExpiredToken           = "ExpiredToken"
	ReasonForbidden              = "Forbidden"
	ReasonInternalServerError    = "InternalServerError"
	ReasonInvalidPushType        = "InvalidPushType"
	ReasonMethodNotAllowed       = "MethodNotAllowed"
	ReasonMissingDeviceToken     = "MissingDeviceToken"
	ReasonMissingTopic           = "MissingTopic"
	ReasonPayloadEmpty           = "PayloadEmpty"
	ReasonPayloadTooLarge        = "PayloadTooLarge"
	ReasonServiceUnavailable     = "ServiceUnavailable"
	ReasonTooManyRequests        = "TooManyRequests"
	ReasonUnregistered           = "Unregistered"
)

// canonicalUUID is the form APNs requires for apns-id: 32 lowercase hex digits
// in 8-4-4-4-12 groups. Apple answers a malformed one with 400 BadMessageId.
var canonicalUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// devicePath matches "/3/device/<hex token>".
var devicePath = regexp.MustCompile(`^/3/device/([0-9a-fA-F]*)$`)

// Request is one push as the simulator received it.
type Request struct {
	Token      string
	Topic      string
	PushType   string
	Priority   string
	Expiration string
	APNSID     string
	CollapseID string
	Payload    []byte
	Header     http.Header
	ReceivedAt time.Time
}

// Response is what the simulator should answer for a given device token.
//
// The zero value is a successful push: 200 with an empty body, which is exactly
// what APNs returns.
type Response struct {
	Status int
	Reason string
	// Timestamp, when set, is included in the body as APNs does on a 410. It
	// records when the token was last known to be invalid.
	Timestamp time.Time
	// RetryAfter, when set, is sent as the Retry-After header.
	RetryAfter string
	// OmitBody answers with the status and no body at all. APNs always sends a
	// reason, but uniqush deliberately does not depend on that for a 410, and
	// this is how that path gets exercised.
	OmitBody bool
}

// Violation is a request that did not conform to Apple's contract.
//
// These are recorded as well as rejected. A test that only checked the push
// result would see a generic failure; the violation says which rule was broken,
// which is the difference between a useful test failure and a puzzling one.
type Violation struct {
	Token   string
	Rule    string
	Details string
}

func (v Violation) String() string {
	return fmt.Sprintf("%s (token %s): %s", v.Rule, shortToken(v.Token), v.Details)
}

func shortToken(token string) string {
	if len(token) <= 12 {
		return token
	}
	return token[:12] + "..."
}

// Server is a running APNs simulator.
type Server struct {
	httpServer *httptest.Server

	mutex          sync.Mutex
	requests       []Request
	violations     []Violation
	responses      map[string]Response
	maxPayloadSize int
	requireTopic   string

	// activeConns counts connections this server currently holds open.
	//
	// Reported per server rather than inferred from the client side, because
	// the only client-side signal is a process-wide goroutine count -- and every
	// other test in the package shares that process. A neighbour opening or
	// closing a connection moves such a count underneath whoever is reading it,
	// which makes a leak test flaky in both directions. This counts exactly the
	// connections to exactly this simulator.
	activeConns int

	// Token authentication state; see auth.go. signingKey being nil means this
	// team uses a certificate and no authorization header is expected.
	signingKey    *SigningKey
	tokenIssuedAt map[string]time.Time
	tokensSeen    []string
	lastMintedAt  time.Time
	clock         func() time.Time
}

// NewServer starts a simulator on a random port with a self-signed certificate.
//
// Close it when done. The returned server speaks HTTP/2 over TLS, because that
// is the only thing uniqush's HTTP/2 processor will talk to: it configures its
// transport with http2.ConfigureTransport, so h2 has to be negotiated over ALPN
// and a cleartext server would never be reached.
func NewServer() *Server {
	server := &Server{
		responses:      make(map[string]Response),
		maxPayloadSize: MaxPayloadSize,
	}

	httpServer := httptest.NewUnstartedServer(http.HandlerFunc(server.handle))
	httpServer.EnableHTTP2 = true
	httpServer.Config.ConnState = server.trackConn
	httpServer.StartTLS()
	server.httpServer = httpServer
	return server
}

// URL is the base URL to give uniqush as the provider's endpoint.
// trackConn keeps activeConns in step with net/http's view of the connection.
//
// StateNew and StateClosed bracket a connection's life; StateHijacked is the
// other terminal state and is counted with it. The intermediate states --
// active, idle -- say what a connection is doing, not whether it exists.
func (s *Server) trackConn(_ net.Conn, state http.ConnState) {
	switch state {
	case http.StateNew:
		s.mutex.Lock()
		s.activeConns++
		s.mutex.Unlock()
	case http.StateClosed, http.StateHijacked:
		s.mutex.Lock()
		s.activeConns--
		s.mutex.Unlock()
	case http.StateActive, http.StateIdle:
	}
}

// ActiveConnections reports how many connections this server currently holds.
//
// The point of measurement for whether a client released what it opened: it
// counts this server's own sockets, so it is unaffected by anything else
// running in the same test binary.
func (s *Server) ActiveConnections() int {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.activeConns
}

// URL returns the simulator's base URL, for an /addpsp endpoint setting.
func (s *Server) URL() string { return s.httpServer.URL }

// Close shuts the simulator down.
func (s *Server) Close() { s.httpServer.Close() }

// WriteCACert writes the simulator's certificate to a PEM file, for use as a
// provider's cacert.
//
// Preferring this over skipverify in tests is not ceremony: it means the test
// exercises the code path that actually verifies a certificate chain, which is
// the path production uses. skipverify would leave that entirely untested.
func (s *Server) WriteCACert(dir string) (string, error) {
	certificate := s.httpServer.Certificate()
	if certificate == nil {
		return "", fmt.Errorf("the simulator has no certificate")
	}
	path := filepath.Join(dir, "apns-simulator-ca.pem")
	encoded := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw})
	if err := os.WriteFile(path, encoded, 0600); err != nil {
		return "", err
	}
	return path, nil
}

// SetResponse makes the simulator answer with r for pushes to this token.
func (s *Server) SetResponse(token string, r Response) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.responses[token] = r
}

// SetMaxPayloadSize overrides the payload ceiling, to model a VoIP topic's 5120.
func (s *Server) SetMaxPayloadSize(size int) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.maxPayloadSize = size
}

// RequireTopic makes the simulator reject pushes whose apns-topic is not this
// value, as APNs does for a token registered against a different app.
func (s *Server) RequireTopic(topic string) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.requireTopic = topic
}

// Requests returns every push the simulator received, in arrival order.
func (s *Server) Requests() []Request {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return append([]Request(nil), s.requests...)
}

// Violations returns every conformance rule broken so far.
func (s *Server) Violations() []Violation {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return append([]Violation(nil), s.violations...)
}

func (s *Server) record(request Request) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.requests = append(s.requests, request)
}

func (s *Server) violate(token, rule, format string, args ...interface{}) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.violations = append(s.violations, Violation{
		Token:   token,
		Rule:    rule,
		Details: fmt.Sprintf(format, args...),
	})
}

func (s *Server) settings() (maxPayload int, topic string) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.maxPayloadSize, s.requireTopic
}

func (s *Server) responseFor(token string) Response {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.responses[token]
}

// fail answers with an APNs error and records the reason as a violation.
func (s *Server) fail(w http.ResponseWriter, token string, status int, reason, rule, format string, args ...interface{}) {
	s.violate(token, rule, format, args...)
	writeAPNSError(w, status, reason, time.Time{})
}

func writeAPNSError(w http.ResponseWriter, status int, reason string, timestamp time.Time) {
	body := map[string]interface{}{"reason": reason}
	if !timestamp.IsZero() {
		// APNs reports this in milliseconds since the epoch.
		body["timestamp"] = timestamp.UnixMilli()
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		http.Error(w, "could not encode the reason", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(encoded)
}

// newAPNSID returns a random RFC 4122 version 4 UUID in the canonical form
// APNs uses: 32 lowercase hex digits in 8-4-4-4-12 groups.
func newAPNSID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand does not fail in practice, and a simulator that refused
		// to answer would be a worse outcome than a fixed id.
		return "00000000-0000-4000-8000-000000000000"
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	// What the client sent, which may be nothing. Recorded on the Request as-is:
	// a test asserting that uniqush supplies an apns-id has to be able to see
	// that it did not, so the generated one below must not stand in for it.
	requestID := r.Header.Get("apns-id")

	// What to echo. APNs assigns an id when the client omits one and returns it
	// on every response, including errors -- which is how a caller that does not
	// send one can still correlate a delivery afterwards. Modelling that matters
	// here because uniqush's decision to always send its own is justified by
	// the alternative being an id that exists only in a response it does not
	// persist; a simulator that echoed nothing would make that reasoning
	// untestable.
	responseID := requestID
	if responseID == "" {
		responseID = newAPNSID()
	}
	// Set once, up here, so it is present on every response this handler can
	// produce. APNs returns apns-id on errors too, and the error paths below go
	// through fail(), which has no way to reach this value.
	w.Header().Set("apns-id", responseID)

	if r.ProtoMajor != 2 {
		s.violate("", "http2", "request arrived over HTTP/%d.%d, not HTTP/2", r.ProtoMajor, r.ProtoMinor)
	}

	if r.Method != http.MethodPost {
		s.fail(w, "", http.StatusMethodNotAllowed, ReasonMethodNotAllowed,
			"method", "expected POST, got %s", r.Method)
		return
	}

	matches := devicePath.FindStringSubmatch(r.URL.Path)
	if matches == nil {
		s.fail(w, "", http.StatusNotFound, ReasonBadPath,
			"path", "expected /3/device/<hex token>, got %q", r.URL.Path)
		return
	}
	token := matches[1]
	if token == "" {
		s.fail(w, token, http.StatusBadRequest, ReasonMissingDeviceToken,
			"token", "the path carried no device token")
		return
	}
	// Apple's own documentation writes tokens in lowercase hex, and a token
	// that round-trips through uniqush's hex.EncodeToString always is. Anything
	// else means something re-cased it on the way.
	if token != strings.ToLower(token) {
		s.violate(token, "token", "device token is not lowercase hex")
	}

	if reason, rule, details := s.checkDuplicateHeaders(r); reason != "" {
		s.fail(w, token, http.StatusBadRequest, reason, rule, "%s", details)
		return
	}

	// Before anything about the notification. APNs authenticates first, which
	// is why a request with no usable credential is answered with 401 or 403
	// rather than with a complaint about a missing topic.
	//
	// A request refused here is not recorded in Requests(), which is Apple's
	// behaviour as far as a provider can observe it -- there is no accepted
	// notification to report. The refusal itself is recorded in Violations(),
	// which is what the auth tests assert on.
	if status, reason, rule, details := s.checkProviderToken(r); reason != "" {
		s.fail(w, token, status, reason, rule, "%s", details)
		return
	}

	// Bounded by the limit this server was actually configured with, not by the
	// package default. They are the same until a test calls SetMaxPayloadSize,
	// and a reader who has just called it should not have to discover that the
	// bound here is a different number.
	maxPayload, _ := s.settings()
	payload, err := io.ReadAll(io.LimitReader(r.Body, int64(maxPayload)*4))
	if err != nil {
		s.fail(w, token, http.StatusBadRequest, ReasonPayloadEmpty,
			"payload", "could not read the body: %v", err)
		return
	}

	request := Request{
		Token:      token,
		Topic:      r.Header.Get("apns-topic"),
		PushType:   r.Header.Get("apns-push-type"),
		Priority:   r.Header.Get("apns-priority"),
		Expiration: r.Header.Get("apns-expiration"),
		APNSID:     requestID,
		CollapseID: r.Header.Get("apns-collapse-id"),
		Payload:    payload,
		Header:     r.Header.Clone(),
		ReceivedAt: time.Now(),
	}
	s.record(request)

	if status, reason, rule, details := s.checkRequest(request); reason != "" {
		s.fail(w, token, status, reason, rule, "%s", details)
		return
	}

	response := s.responseFor(token)
	if response.RetryAfter != "" {
		w.Header().Set("Retry-After", response.RetryAfter)
	}
	if response.Status == 0 || response.Status == http.StatusOK {
		// A successful push is 200 with an empty body.
		w.WriteHeader(http.StatusOK)
		return
	}
	if response.OmitBody {
		w.WriteHeader(response.Status)
		return
	}
	writeAPNSError(w, response.Status, response.Reason, response.Timestamp)
}

// checkDuplicateHeaders catches the bug the lowercase-header comment in
// http_api/processor.go warns about.
//
// HTTP/2 requires lowercase field names, and http.Header.Set canonicalises to
// "Apns-Topic". Using Set alongside a lowercase literal therefore produces two
// map entries that both serialise to "apns-topic" on the wire, and the server
// sees the value twice. Apple answers that with 400 DuplicateHeaders. Without
// this check the mistake is invisible: the first value is usually right, so
// everything appears to work.
func (s *Server) checkDuplicateHeaders(r *http.Request) (reason, rule, details string) {
	for _, name := range []string{"apns-topic", "apns-push-type", "apns-priority", "apns-expiration", "apns-id", "authorization"} {
		if values := r.Header.Values(name); len(values) > 1 {
			return ReasonDuplicateHeaders, "duplicate-headers",
				fmt.Sprintf("%s was sent %d times (%v); Set() alongside a lowercase literal is the usual cause", name, len(values), values)
		}
	}
	return "", "", ""
}

// checkRequest enforces the header rules that matter, and that uniqush's HTTP/2
// repairs were about.
func (s *Server) checkRequest(request Request) (status int, reason, rule, details string) {
	maxPayload, requiredTopic := s.settings()

	if request.Topic == "" {
		return http.StatusBadRequest, ReasonMissingTopic, "topic",
			"apns-topic is required; uniqush takes it from the provider's bundleid"
	}
	if requiredTopic != "" && request.Topic != requiredTopic {
		return http.StatusBadRequest, ReasonDeviceTokenNotForTopic, "topic",
			fmt.Sprintf("apns-topic %q is not the topic this token is registered for", request.Topic)
	}

	// The header at the centre of the HTTP/2 repairs. iOS 13+ requires it, and
	// omitting it on a background push makes APNs return 200 and then drop the
	// notification -- a failure with no symptom at all on the sending side.
	if request.PushType == "" {
		return http.StatusBadRequest, ReasonInvalidPushType, "push-type",
			"apns-push-type is missing; iOS 13+ requires it and a background push without it is silently dropped"
	}
	if !validPushTypes[request.PushType] {
		return http.StatusBadRequest, ReasonInvalidPushType, "push-type",
			fmt.Sprintf("apns-push-type %q is not a value APNs recognises", request.PushType)
	}

	// "Always use priority 5. Using priority 10 is an error" -- Apple, on
	// background pushes.
	switch request.Priority {
	case "":
		// APNs defaults to 10, which is legal but not what uniqush intends to
		// leave to chance.
		return http.StatusBadRequest, ReasonBadPriority, "priority",
			"apns-priority is missing; a background push must set 5 explicitly"
	case "5", "10":
	default:
		return http.StatusBadRequest, ReasonBadPriority, "priority",
			fmt.Sprintf("apns-priority %q is not 5 or 10", request.Priority)
	}
	if request.PushType == "background" && request.Priority == "10" {
		return http.StatusBadRequest, ReasonBadPriority, "priority",
			"a background push must use priority 5; APNs rejects 10"
	}

	if request.Expiration != "" {
		if _, err := strconv.ParseUint(request.Expiration, 10, 64); err != nil {
			return http.StatusBadRequest, ReasonBadExpirationDate, "expiration",
				fmt.Sprintf("apns-expiration %q is not a UNIX timestamp", request.Expiration)
		}
	}

	// APNs generates an apns-id when one is not supplied, so this is only a
	// rule for the ids uniqush sends itself.
	if request.APNSID != "" && !canonicalUUID.MatchString(request.APNSID) {
		return http.StatusBadRequest, ReasonBadMessageID, "apns-id",
			fmt.Sprintf("apns-id %q is not a canonical lowercase UUID", request.APNSID)
	}

	if len(request.Payload) == 0 {
		return http.StatusBadRequest, ReasonPayloadEmpty, "payload", "the payload was empty"
	}
	if len(request.Payload) > maxPayload {
		return http.StatusRequestEntityTooLarge, ReasonPayloadTooLarge, "payload",
			fmt.Sprintf("the payload is %d bytes, over the %d byte limit", len(request.Payload), maxPayload)
	}
	if !json.Valid(request.Payload) {
		return http.StatusBadRequest, ReasonPayloadEmpty, "payload", "the payload is not valid JSON"
	}

	return 0, "", "", ""
}

// validPushTypes mirrors common.validPushTypes. Duplicated deliberately: the
// simulator stands in for Apple, so it must not agree with uniqush by
// construction. If uniqush's list drifts from Apple's, a test that shares the
// list cannot notice.
var validPushTypes = map[string]bool{
	"alert": true, "background": true, "complication": true, "controls": true,
	"fileprovider": true, "liveactivity": true, "location": true, "mdm": true,
	"pushtotalk": true, "voip": true, "widgets": true,
}

// GenerateClientCert writes a self-signed certificate and key for uniqush to
// present as a provider certificate.
//
// The simulator never checks it -- APNs would, but modelling Apple's
// certificate authority adds nothing to what is being tested here. It exists
// because /addpsp requires a loadable cert/key pair, and the fixture in
// srv/apns/apns-test expired in 2022.
func GenerateClientCert(dir string) (certPath, keyPath string, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", err
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "uniqush apns test client"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return "", "", err
	}

	certPath = filepath.Join(dir, "client.cert")
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err = os.WriteFile(certPath, certPEM, 0600); err != nil {
		return "", "", err
	}

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return "", "", err
	}
	keyPath = filepath.Join(dir, "client.key")
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(keyPath, keyPEM, 0600); err != nil {
		return "", "", err
	}
	return certPath, keyPath, nil
}

// DeviceToken returns a syntactically valid 32-byte device token for tests.
func DeviceToken(seed byte) string {
	token := make([]byte, 32)
	for i := range token {
		token[i] = seed + byte(i)
	}
	return hex.EncodeToString(token)
}
