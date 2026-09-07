package main

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"
)

// deviceKeys returns a subscription's public halves the way a phone would
// present them: raw-url base64, no padding.
func deviceKeys(t *testing.T) (p256dh, auth string) {
	t.Helper()
	key, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generating a P-256 key: %v", err)
	}
	secret := make([]byte, 16)
	if _, err := rand.Read(secret); err != nil {
		t.Fatalf("generating an auth secret: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes()),
		base64.RawURLEncoding.EncodeToString(secret)
}

func TestApplyTestPageURL(t *testing.T) {
	p256dh, auth := deviceKeys(t)
	const endpoint = "https://ntfy.sh/upabcdef123456?up=1"

	t.Run("reads a subscription out of the fragment", func(t *testing.T) {
		var opts options
		link := "https://unifiedpush.org/test_wp.html#endpoint=" + endpoint + "&p256dh=" + p256dh + "&auth=" + auth
		if err := opts.applyTestPageURL(link); err != nil {
			t.Fatalf("applyTestPageURL: %v", err)
		}
		if opts.endpoint != endpoint {
			t.Errorf("endpoint = %q, want %q", opts.endpoint, endpoint)
		}
		if opts.p256dh != p256dh || opts.auth != auth {
			t.Error("the keys did not survive the fragment")
		}
		if !opts.deviceMode() {
			t.Error("a link with an endpoint should put the demo in device mode")
		}
	})

	t.Run("rejects links that are missing pieces", func(t *testing.T) {
		testCases := map[string]string{
			"no fragment":  "https://unifiedpush.org/test_wp.html",
			"no endpoint":  "https://unifiedpush.org/test_wp.html#p256dh=" + p256dh + "&auth=" + auth,
			"no keys":      "https://unifiedpush.org/test_wp.html#endpoint=" + endpoint,
			"empty string": "",
		}
		for name, link := range testCases {
			t.Run(name, func(t *testing.T) {
				var opts options
				if err := opts.applyTestPageURL(link); err == nil {
					t.Errorf("accepted %q", link)
				}
			})
		}
	})
}

func TestOptionsValidate(t *testing.T) {
	p256dh, auth := deviceKeys(t)
	const endpoint = "https://ntfy.sh/upabcdef123456?up=1"

	t.Run("a complete device subscription", func(t *testing.T) {
		opts := options{endpoint: endpoint, p256dh: p256dh, auth: auth}
		if err := opts.validate(); err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
	})

	t.Run("no device subscription at all", func(t *testing.T) {
		if err := (&options{}).validate(); err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
	})

	t.Run("rejects a partial subscription", func(t *testing.T) {
		partials := []options{
			{endpoint: endpoint},
			{endpoint: endpoint, p256dh: p256dh},
			{endpoint: endpoint, auth: auth},
			{p256dh: p256dh, auth: auth},
		}
		for _, opts := range partials {
			if err := opts.validate(); err == nil {
				t.Errorf("accepted %+v", opts)
			}
		}
	})

	// A value copied off a phone screen by hand is easy to truncate, and the
	// resulting /subscribe error says less than this does.
	t.Run("rejects keys of the wrong length", func(t *testing.T) {
		truncated := options{endpoint: endpoint, p256dh: p256dh[:40], auth: auth}
		if err := truncated.validate(); err == nil {
			t.Error("accepted a truncated p256dh")
		} else if !strings.Contains(err.Error(), "truncated") {
			t.Errorf("Expected the error to point at truncation, got: %v", err)
		}

		shortAuth := options{endpoint: endpoint, p256dh: p256dh, auth: auth[:10]}
		if err := shortAuth.validate(); err == nil {
			t.Error("accepted a truncated auth secret")
		}
	})

	t.Run("accepts standard-alphabet keys", func(t *testing.T) {
		raw, err := base64.RawURLEncoding.DecodeString(p256dh)
		if err != nil {
			t.Fatalf("decoding the test key: %v", err)
		}
		opts := options{
			endpoint: endpoint,
			p256dh:   base64.StdEncoding.EncodeToString(raw),
			auth:     auth,
		}
		if err := opts.validate(); err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
	})
}

// The hint in the retry message is only useful if it can tell a well-formed
// ntfy topic from one that will never accept a push.
func TestTopicLengthHint(t *testing.T) {
	good := topicLengthHint("https://ntfy.sh/upabcdef123456?up=1")
	if !strings.Contains(good, "right shape") {
		t.Errorf("Expected a 14-character topic to be accepted, got: %s", good)
	}
	bad := topicLengthHint("https://ntfy.sh/uptoolongtobevalid?up=1")
	if !strings.Contains(bad, "14-character") {
		t.Errorf("Expected an oversized topic to be called out, got: %s", bad)
	}
}
