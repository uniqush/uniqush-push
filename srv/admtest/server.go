// Package admtest provides an offline HTTP simulator for ADM token and message requests.
package admtest

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
)

// Response is the response to a message sent to a registration ID.
type Response struct {
	Status     int
	Reason     string
	RetryAfter string
}

// Server simulates the token exchange and per-device message responses.
// It is not an implementation of ADM delivery, registration or rate limiting.
type Server struct {
	server    *httptest.Server
	mu        sync.Mutex
	responses map[string]Response
	tokens    int
	messages  int
}

// NewServer starts a simulator using public, synthetic credentials only.
func NewServer() *Server {
	s := &Server{responses: make(map[string]Response)}
	s.server = httptest.NewServer(http.HandlerFunc(s.serveHTTP))
	return s
}

// Close shuts down the simulator.
func (s *Server) Close() { s.server.Close() }

// SetResponse selects a response for a registration ID. The default is success.
func (s *Server) SetResponse(registrationID string, response Response) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.responses[registrationID] = response
}

// Counts returns the numbers of successful token exchanges and message requests.
func (s *Server) Counts() (tokens, messages int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tokens, s.messages
}

type transport struct {
	base       *url.URL
	underlying *http.Transport
}

func (t transport) CloseIdleConnections() { t.underlying.CloseIdleConnections() }

func (t transport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Scheme != "https" || r.URL.Host != "api.amazon.com" {
		return nil, fmt.Errorf("ADM simulator refuses unexpected URL %q", r.URL)
	}
	request := r.Clone(r.Context())
	request.URL.Scheme = t.base.Scheme
	request.URL.Host = t.base.Host
	request.Host = t.base.Host
	return t.underlying.RoundTrip(request)
}

// Client routes only the canonical ADM host to this server, never to Amazon.
func (s *Server) Client() *http.Client {
	base, _ := url.Parse(s.server.URL)
	return &http.Client{Transport: transport{base, &http.Transport{}}}
}

func (s *Server) serveHTTP(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, "expected POST", http.StatusMethodNotAllowed)
		return
	}
	if r.URL.Path == "/auth/O2/token" {
		if err := r.ParseForm(); err != nil || r.Form.Get("grant_type") != "client_credentials" ||
			r.Form.Get("scope") != "messaging:push" || r.Form.Get("client_id") != "test-client" ||
			r.Form.Get("client_secret") != "test-secret" {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "INVALID_CLIENT"})
			return
		}
		s.mu.Lock()
		s.tokens++
		s.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "test-token", "expires_in": 3600, "token_type": "Bearer", "scope": "messaging:push"})
		return
	}
	prefix, suffix := "/messaging/registrations/", "/messages"
	if !strings.HasPrefix(r.URL.Path, prefix) || !strings.HasSuffix(r.URL.Path, suffix) {
		http.NotFound(w, r)
		return
	}
	if r.Header.Get("Authorization") != "Bearer test-token" {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"reason": "AccessTokenExpired"})
		return
	}
	if r.Header.Get("Content-Type") != "application/json" ||
		r.Header.Get("x-amzn-type-version") != "com.amazon.device.messaging.ADMMessage@1.0" ||
		r.Header.Get("x-amzn-accept-type") != "com.amazon.device.messaging.ADMSendResult@1.0" {
		http.Error(w, "invalid message headers", http.StatusBadRequest)
		return
	}
	var payload struct {
		Data map[string]string `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&payload); err != nil {
		http.Error(w, "invalid message payload", http.StatusBadRequest)
		return
	}
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, prefix), suffix)
	s.mu.Lock()
	response := s.responses[id]
	s.messages++
	s.mu.Unlock()
	if response.Status == 0 {
		response.Status = http.StatusOK
	}
	w.Header().Set("x-amzn-RequestId", "test-request")
	if response.RetryAfter != "" {
		w.Header().Set("Retry-After", response.RetryAfter)
	}
	w.WriteHeader(response.Status)
	if response.Status == http.StatusOK {
		_ = json.NewEncoder(w).Encode(map[string]string{"registrationID": id})
	} else {
		_ = json.NewEncoder(w).Encode(map[string]string{"reason": response.Reason})
	}
}
