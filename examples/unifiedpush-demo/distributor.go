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

package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// A distributor is the middle of the three UnifiedPush parties. uniqush is the
// application server and POSTs to an endpoint; the distributor owns that
// endpoint and delivers what arrives to the app on the device.
//
// The demo needs both halves of it: the registration that hands out an endpoint,
// and the delivery leg, which is what proves the message survived the trip
// rather than merely being accepted.
type distributor interface {
	// Name is what to call it in the trace.
	Name() string
	// Register returns a push endpoint, as a real distributor hands one to an
	// app after the user picks it.
	Register(ctx context.Context) (string, error)
	// Deliveries yields the raw POST bodies that reach the endpoint, exactly as
	// received. The UnifiedPush spec is explicit that a distributor passes
	// through "the raw POST data received by the push server".
	Deliveries() <-chan []byte
	// Close releases whatever Register and Deliveries started.
	Close() error
}

// topicLength is the total length of an ntfy UnifiedPush topic, "up" included.
//
// This is not cosmetic. ntfy.sh runs with visitor-subscriber-rate-limiting on,
// which charges a UnifiedPush message to the subscriber's rate limits instead of
// the publisher's -- and it decides whether a topic is eligible for that purely
// by name: `strings.HasPrefix(t.ID, "up") && len(t.ID) == 14`. A topic of any
// other length can never acquire a "rate visitor", so ntfy answers every push to
// it with:
//
//	HTTP 507 {"code":50701,"error":"cannot publish to UnifiedPush topic without
//	previously active subscriber"}
//
// no matter how many subscribers are actually listening. It is a 5xx rather than
// a 4xx on purpose, so that application servers retry instead of deleting the
// subscription. Distributor apps generate 14-character topics, so this only
// bites something like this demo, which mints its own.
const topicLength = 14

// topicAlphabet is deliberately narrow. ntfy accepts [-_A-Za-z0-9], but a topic
// travels through URLs and logs, so mixed case and punctuation buy nothing.
const topicAlphabet = "abcdefghijklmnopqrstuvwxyz0123456789"

// randomTopic returns a UnifiedPush-shaped identifier, which for ntfy is also
// the endpoint's path segment.
func randomTopic() (string, error) {
	suffix := make([]byte, topicLength-len("up"))
	raw := make([]byte, len(suffix))
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	for i, b := range raw {
		// Modulo bias over 36 symbols is irrelevant here: this is a name, not a
		// secret, and 36^12 is ~6e18 either way.
		suffix[i] = topicAlphabet[int(b)%len(topicAlphabet)]
	}
	return "up" + string(suffix), nil
}

// ntfyDistributor drives a real ntfy server, self-hosted or ntfy.sh.
//
// ntfy is the UnifiedPush distributor most people actually run, and it is a real
// third-party implementation of the push-server side, which is the point: the
// demo is not grading uniqush against a mock written to agree with it.
type ntfyDistributor struct {
	baseURL string
	client  *http.Client
	topic   string
	stream  io.ReadCloser
	out     chan []byte
	cancel  context.CancelFunc
}

func newNtfyDistributor(baseURL string) *ntfyDistributor {
	return &ntfyDistributor{
		baseURL: strings.TrimRight(baseURL, "/"),
		client:  &http.Client{},
		out:     make(chan []byte, 4),
	}
}

func (d *ntfyDistributor) Name() string { return "ntfy at " + d.baseURL }

func (d *ntfyDistributor) Register(ctx context.Context) (string, error) {
	topic, err := randomTopic()
	if err != nil {
		return "", err
	}
	d.topic = topic

	// ntfy has no registration call: subscribing to a topic creates it. Opening
	// the stream first is what makes the delivery leg observable, and it also
	// fails early if the server is unreachable.
	streamCtx, cancel := context.WithCancel(ctx)
	d.cancel = cancel

	request, err := http.NewRequestWithContext(streamCtx, http.MethodGet, d.baseURL+"/"+topic+"/json", nil)
	if err != nil {
		cancel()
		return "", err
	}
	response, err := d.client.Do(request)
	if err != nil {
		cancel()
		return "", fmt.Errorf("subscribing to %s: %w", d.baseURL, err)
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		cancel()
		return "", fmt.Errorf("subscribing to %s returned HTTP %d", d.baseURL, response.StatusCode)
	}
	d.stream = response.Body
	go d.readStream()

	// The UnifiedPush endpoint form for ntfy. ?up=1 tells ntfy the body is an
	// opaque UnifiedPush payload rather than a notification to render.
	return d.baseURL + "/" + topic + "?up=1", nil
}

// readStream turns ntfy's newline-delimited JSON into raw bodies.
func (d *ntfyDistributor) readStream() {
	defer close(d.out)
	scanner := bufio.NewScanner(d.stream)
	// A push message fits the default 64KiB token limit with room to spare -- a
	// 4096-byte record is about 5.5KiB base64'd, inside a small JSON envelope.
	// The ceiling is raised because the limit applies to whatever ntfy sends,
	// not to what uniqush sent: a topic can carry messages from anything, and a
	// single oversized line would end the stream with ErrTooLong rather than
	// being skipped.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		var event struct {
			Event    string `json:"event"`
			Message  string `json:"message"`
			Encoding string `json:"encoding"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			continue
		}
		if event.Event != "message" {
			continue // "open" and "keepalive"
		}
		// An encrypted body is not valid UTF-8, so ntfy hands it back base64.
		// Standard alphabet, not the URL-safe one used everywhere else here.
		body := []byte(event.Message)
		if event.Encoding == "base64" {
			decoded, err := base64.StdEncoding.DecodeString(event.Message)
			if err != nil {
				continue
			}
			body = decoded
		}
		d.out <- body
	}
}

func (d *ntfyDistributor) Deliveries() <-chan []byte { return d.out }

func (d *ntfyDistributor) Close() error {
	if d.cancel != nil {
		d.cancel()
	}
	if d.stream != nil {
		return d.stream.Close()
	}
	return nil
}

// localDistributor is a push server built into the demo, for running without a
// distributor to point at.
//
// It implements the push-server side of the UnifiedPush server spec: accept a
// POST, answer 201 with TTL: 0, and hand the raw body to the application. That
// is the whole contract.
type localDistributor struct {
	server   *http.Server
	listener net.Listener
	topic    string
	out      chan []byte
}

func newLocalDistributor() *localDistributor {
	return &localDistributor{out: make(chan []byte, 4)}
}

func (d *localDistributor) Name() string { return "the demo's built-in push server" }

// Register takes a context to satisfy the distributor interface. Unlike the ntfy
// one, this listener is local and immediate, so there is nothing to cancel.
func (d *localDistributor) Register(_ context.Context) (string, error) {
	topic, err := randomTopic()
	if err != nil {
		return "", err
	}
	d.topic = topic

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	d.listener = listener

	mux := http.NewServeMux()
	mux.HandleFunc("/"+topic, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			// The spec requires JSON on GET, so a client can discover what the
			// push server supports.
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"unifiedpush":{"version":1}}`)
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		// "A push server MUST accept a message payload of a size between 1 byte
		// and 4096 bytes (inclusive)", and answer 413 above that.
		if len(body) > 4096 {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			return
		}
		w.Header().Set("TTL", "0")
		w.WriteHeader(http.StatusCreated)
		select {
		case d.out <- body:
		default:
		}
	})

	d.server = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		_ = d.server.Serve(listener)
	}()

	endpoint := &url.URL{Scheme: "http", Host: listener.Addr().String(), Path: "/" + topic}
	return endpoint.String(), nil
}

func (d *localDistributor) Deliveries() <-chan []byte { return d.out }

func (d *localDistributor) Close() error {
	if d.server == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return d.server.Shutdown(ctx)
}
