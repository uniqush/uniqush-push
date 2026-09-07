package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"

	webpush "github.com/SherClockHolmes/webpush-go"
)

// encryptForTest produces a body the way uniqush produces one: with the same
// library, through the same send path, captured off the wire by a stand-in push
// server. Hand-rolling the ciphertext here would test the demo against itself.
func encryptForTest(sub *subscription, payload []byte, recordSize int) ([]byte, error) {
	var captured []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		captured = body
		w.Header().Set("TTL", "0")
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	privateKey, publicKey, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		return nil, fmt.Errorf("generating VAPID keys: %w", err)
	}

	response, err := webpush.SendNotification(payload, &webpush.Subscription{
		Endpoint: server.URL,
		Keys:     webpush.Keys{P256dh: sub.P256dh, Auth: sub.Auth},
	}, &webpush.Options{
		Subscriber:      "test@example.org",
		VAPIDPublicKey:  publicKey,
		VAPIDPrivateKey: privateKey,
		TTL:             60,
		RecordSize:      uint32(recordSize),
	})
	if err != nil {
		return nil, fmt.Errorf("sending: %w", err)
	}
	defer response.Body.Close()

	if captured == nil {
		return nil, fmt.Errorf("the stand-in push server captured no body")
	}
	return captured, nil
}
