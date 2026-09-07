package main

import (
	"crypto/ecdh"
	"encoding/base64"
	"testing"
)

// The worked example from RFC 8291 section 5. If the receiver in crypto.go can
// read this, it implements the RFC rather than merely agreeing with whatever
// uniqush happens to send, which is what makes the demo's verdict mean
// something.
const (
	rfc8291Body = "DGv6ra1nlYgDCS1FRnbzlwAAEABBBP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27ml" +
		"mlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A_yl95bQpu6cVPT" +
		"pK4Mqgkf1CXztLVBSt2Ks3oZwbuwXPXLWyouBWLVWGNWQexSgSxsj_Qulcy4a-fN"
	rfc8291Auth       = "BTBZMqHH6r4Tts7J_aSIgg"
	rfc8291PrivateKey = "q1dXpw3UpT5VOmu_cf_v6ih07Aems3njxI-JWgLcM94"
	rfc8291PublicKey  = "BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4"
	rfc8291Plaintext  = "When I grow up, I want to be a watermelon"
)

func rfc8291Subscription(t *testing.T) *subscription {
	t.Helper()
	privateBytes, err := base64.RawURLEncoding.DecodeString(rfc8291PrivateKey)
	if err != nil {
		t.Fatalf("decoding the RFC's private key: %v", err)
	}
	privateKey, err := ecdh.P256().NewPrivateKey(privateBytes)
	if err != nil {
		t.Fatalf("loading the RFC's private key: %v", err)
	}
	auth, err := base64.RawURLEncoding.DecodeString(rfc8291Auth)
	if err != nil {
		t.Fatalf("decoding the RFC's auth secret: %v", err)
	}
	return &subscription{
		privateKey: privateKey,
		P256dh:     rfc8291PublicKey,
		Auth:       rfc8291Auth,
		auth:       auth,
	}
}

func TestRFC8291Vector(t *testing.T) {
	body, err := base64.RawURLEncoding.DecodeString(rfc8291Body)
	if err != nil {
		t.Fatalf("decoding the RFC's message body: %v", err)
	}

	plaintext, header, err := rfc8291Subscription(t).decrypt(body)
	if err != nil {
		t.Fatalf("decrypting the RFC 8291 section 5 example: %v", err)
	}
	if got := string(plaintext); got != rfc8291Plaintext {
		t.Errorf("plaintext = %q, want %q", got, rfc8291Plaintext)
	}
	if header.RecordSize != 4096 {
		t.Errorf("record size = %d, want 4096", header.RecordSize)
	}
	if got := base64.RawURLEncoding.EncodeToString(header.Salt); got != "DGv6ra1nlYgDCS1FRnbzlw" {
		t.Errorf("salt = %q, want %q", got, "DGv6ra1nlYgDCS1FRnbzlw")
	}
}

// A modified body must fail rather than yield the wrong plaintext. Without this
// a decryptor that ignored the tag would still pass the vector test.
func TestRFC8291VectorTamperedFails(t *testing.T) {
	body, err := base64.RawURLEncoding.DecodeString(rfc8291Body)
	if err != nil {
		t.Fatalf("decoding the RFC's message body: %v", err)
	}
	body[len(body)-1] ^= 0x01

	if _, _, err := rfc8291Subscription(t).decrypt(body); err == nil {
		t.Fatal("decrypting a tampered body succeeded, want an authentication failure")
	}
}

// A record smaller than the receiver's ceiling is legal: rs is a bound, not a
// contract. uniqush sends rs=2048, and a receiver that insisted on 4096 would
// reject every message it sends.
func TestSmallerRecordSizeRoundTrips(t *testing.T) {
	sub, err := newSubscription()
	if err != nil {
		t.Fatalf("newSubscription: %v", err)
	}
	const payload = "a UnifiedPush wakeup"
	body, err := encryptForTest(sub, []byte(payload), 2048)
	if err != nil {
		t.Fatalf("encryptForTest: %v", err)
	}
	if len(body) != 2048 {
		t.Errorf("body = %d bytes, want a full 2048-byte record", len(body))
	}

	plaintext, header, err := sub.decrypt(body)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if string(plaintext) != payload {
		t.Errorf("plaintext = %q, want %q", plaintext, payload)
	}
	if header.RecordSize != 2048 {
		t.Errorf("record size = %d, want 2048", header.RecordSize)
	}
}

func TestParseHeaderRejectsShortBody(t *testing.T) {
	if _, err := parseHeader(make([]byte, headerLength-1)); err == nil {
		t.Fatal("parseHeader accepted a body shorter than the header")
	}
}

func TestUnpad(t *testing.T) {
	tests := []struct {
		name    string
		in      []byte
		want    string
		wantErr bool
	}{
		{name: "last record, no padding", in: []byte("hi\x02"), want: "hi"},
		{name: "last record, padded", in: []byte("hi\x02\x00\x00\x00"), want: "hi"},
		{name: "not the last record", in: []byte("hi\x01\x00"), want: "hi"},
		{name: "empty payload", in: []byte("\x02"), want: ""},
		{name: "no delimiter", in: []byte("hi"), wantErr: true},
		{name: "all padding", in: []byte("\x00\x00"), wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := unpad(test.in)
			if test.wantErr {
				if err == nil {
					t.Fatalf("unpad(%q) = %q, want an error", test.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unpad(%q): %v", test.in, err)
			}
			if string(got) != test.want {
				t.Errorf("unpad(%q) = %q, want %q", test.in, got, test.want)
			}
		})
	}
}
