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
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"fmt"
)

// This file is the receiving half of RFC 8291: what a UnifiedPush connector
// library does on the device after the distributor hands it a push. uniqush
// never runs this code; the demo needs it to prove that what uniqush sent can
// actually be read.
//
// It is deliberately written against nothing but the standard library, so there
// is no shared implementation with the sender and no chance of a matched pair of
// mistakes cancelling out. TestRFC8291Vector in crypto_test.go checks it against
// the worked example in RFC 8291 section 5, so a successful decryption here
// means the bytes on the wire are the bytes the RFC describes.

const (
	// saltLength, per RFC 8188 section 2.1.
	saltLength = 16
	// keyIDLength: the application server's public key, as an uncompressed
	// P-256 point.
	keyIDLength = 65
	// headerLength is salt(16) + rs(4) + idlen(1) + keyid(65).
	headerLength = saltLength + 4 + 1 + keyIDLength
	// authLength is the subscription's auth secret, per RFC 8291 section 3.2.
	authLength = 16
	// gcmTagLength is the AES-GCM authentication tag.
	gcmTagLength = 16
	// minRecordSize is the floor RFC 8188 puts on the rs field.
	minRecordSize = 18
)

// subscription is the client half of a Web Push subscription: the keys a
// UnifiedPush connector generates on the device and hands to the application
// server, plus the private key it keeps.
type subscription struct {
	privateKey *ecdh.PrivateKey
	// P256dh is the public key, raw-url base64. Sent to /subscribe.
	P256dh string
	// Auth is the 16-byte auth secret, raw-url base64. Sent to /subscribe.
	Auth string
	auth []byte
}

// newSubscription generates the key material for one subscription.
func newSubscription() (*subscription, error) {
	privateKey, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generating P-256 key: %w", err)
	}
	auth := make([]byte, authLength)
	if _, err := rand.Read(auth); err != nil {
		return nil, fmt.Errorf("generating auth secret: %w", err)
	}
	return &subscription{
		privateKey: privateKey,
		P256dh:     base64.RawURLEncoding.EncodeToString(privateKey.PublicKey().Bytes()),
		Auth:       base64.RawURLEncoding.EncodeToString(auth),
		auth:       auth,
	}, nil
}

// messageHeader is the aes128gcm content coding header (RFC 8188 section 2.1)
// as it appears at the front of every Web Push body.
type messageHeader struct {
	Salt       []byte
	RecordSize uint32
	KeyID      []byte
}

// parseHeader reads the header without doing any crypto, so the demo can report
// what is on the wire even when decryption fails.
func parseHeader(body []byte) (*messageHeader, error) {
	if len(body) < headerLength {
		return nil, fmt.Errorf("body is %d bytes, too short for an aes128gcm header (%d)", len(body), headerLength)
	}
	idLen := int(body[saltLength+4])
	if idLen != keyIDLength {
		return nil, fmt.Errorf("keyid is %d bytes, Web Push requires an uncompressed P-256 point (%d)", idLen, keyIDLength)
	}
	return &messageHeader{
		Salt:       body[:saltLength],
		RecordSize: binary.BigEndian.Uint32(body[saltLength : saltLength+4]),
		KeyID:      body[saltLength+5 : saltLength+5+idLen],
	}, nil
}

// decrypt recovers the plaintext of a single-record aes128gcm message.
//
// Web Push messages are always one record (RFC 8291 section 2), so this does not
// implement the multi-record chunking RFC 8188 allows in general.
func (s *subscription) decrypt(body []byte) ([]byte, *messageHeader, error) {
	header, err := parseHeader(body)
	if err != nil {
		return nil, nil, err
	}
	if header.RecordSize < minRecordSize {
		return nil, header, fmt.Errorf("record size %d is below the RFC 8188 minimum of %d", header.RecordSize, minRecordSize)
	}
	// RFC 8188: rs is the maximum size of a record, so the record has to fit in
	// it. Note that this is a bound, not an equality: an application server is
	// free to use a smaller record than the receiver's ceiling, and rejecting
	// that is the bug in google/tink that UnifiedPush forked its copy to fix.
	record := body[headerLength:]
	if uint32(len(record)) > header.RecordSize {
		return nil, header, fmt.Errorf("record is %d bytes, larger than the declared record size %d", len(record), header.RecordSize)
	}
	if len(record) < gcmTagLength+1 {
		return nil, header, fmt.Errorf("record is %d bytes, too short to hold a padded plaintext and a GCM tag", len(record))
	}

	// The keyid field is the application server's ephemeral public key.
	serverKey, err := ecdh.P256().NewPublicKey(header.KeyID)
	if err != nil {
		return nil, header, fmt.Errorf("keyid is not a valid P-256 point: %w", err)
	}
	shared, err := s.privateKey.ECDH(serverKey)
	if err != nil {
		return nil, header, fmt.Errorf("ECDH: %w", err)
	}

	// RFC 8291 section 3.4. The auth secret salts the first extraction, which is
	// what binds the derivation to this subscription rather than to anyone who
	// happens to know the public keys.
	keyInfo := make([]byte, 0, len("WebPush: info\x00")+2*keyIDLength)
	keyInfo = append(keyInfo, []byte("WebPush: info\x00")...)
	keyInfo = append(keyInfo, s.privateKey.PublicKey().Bytes()...)
	keyInfo = append(keyInfo, header.KeyID...)

	prkKey, err := hkdf.Extract(sha256.New, shared, s.auth)
	if err != nil {
		return nil, header, fmt.Errorf("HKDF extract (auth): %w", err)
	}
	ikm, err := hkdf.Expand(sha256.New, prkKey, string(keyInfo), 32)
	if err != nil {
		return nil, header, fmt.Errorf("HKDF expand (IKM): %w", err)
	}

	// RFC 8188 section 2.2: the content encryption key and nonce come from the
	// message salt and the IKM above.
	prk, err := hkdf.Extract(sha256.New, ikm, header.Salt)
	if err != nil {
		return nil, header, fmt.Errorf("HKDF extract (salt): %w", err)
	}
	cek, err := hkdf.Expand(sha256.New, prk, "Content-Encoding: aes128gcm\x00", 16)
	if err != nil {
		return nil, header, fmt.Errorf("HKDF expand (CEK): %w", err)
	}
	nonce, err := hkdf.Expand(sha256.New, prk, "Content-Encoding: nonce\x00", 12)
	if err != nil {
		return nil, header, fmt.Errorf("HKDF expand (nonce): %w", err)
	}

	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, header, fmt.Errorf("AES: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, header, fmt.Errorf("GCM: %w", err)
	}
	padded, err := aead.Open(nil, nonce, record, nil)
	if err != nil {
		// A failure here is authentication failing, not a parse error: the key
		// derivation above disagreed with the sender's somewhere.
		return nil, header, fmt.Errorf("AES-GCM authentication failed: %w", err)
	}

	plaintext, err := unpad(padded)
	if err != nil {
		return nil, header, err
	}
	return plaintext, header, nil
}

// unpad strips RFC 8188 section 2 padding: zero or more 0x00 bytes preceded by a
// delimiter, 0x02 on the last record and 0x01 otherwise.
func unpad(padded []byte) ([]byte, error) {
	end := len(padded)
	for end > 0 && padded[end-1] == 0x00 {
		end--
	}
	if end == 0 {
		return nil, fmt.Errorf("plaintext is all padding, with no record delimiter")
	}
	switch padded[end-1] {
	case 0x02, 0x01:
		return padded[:end-1], nil
	default:
		return nil, fmt.Errorf("record delimiter is 0x%02x, expected 0x01 or 0x02", padded[end-1])
	}
}
