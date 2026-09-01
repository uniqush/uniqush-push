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

package common

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"

	"github.com/uniqush/uniqush-push/push"
)

// Keys a provider carries in VolatileData for APNs token authentication.
//
// All three are volatile rather than fixed, and that is load-bearing. A
// provider's name is a hash of its FixedData and a delivery point is bound to
// that name, so anything stored there can never change without stranding every
// subscription. A signing key that could not be rotated would be a worse
// arrangement than the certificate it replaces.
const (
	// AuthKeyKey is a path to the .p8 file downloaded from Apple.
	AuthKeyKey = "authkey"
	// KeyIDKey is the 10-character Key ID shown next to the key in the
	// developer portal. It becomes the JWT's "kid" header.
	KeyIDKey = "keyid"
	// TeamIDKey is the 10-character Team ID. It becomes the JWT's "iss" claim.
	TeamIDKey = "teamid"
)

// appleIDLength is the length of a Team ID and a Key ID. Both are ten
// characters, and a wrong one produces a 403 InvalidProviderToken with nothing
// to say which of the two was at fault -- so they are worth checking here.
const appleIDLength = 10

// UsesTokenAuth reports whether a provider authenticates with a signing key
// rather than a certificate.
func UsesTokenAuth(psp *push.PushServiceProvider) bool {
	return psp.VolatileData[AuthKeyKey] != ""
}

// LoadAuthKey reads Apple's .p8 signing key.
//
// The file is PEM around a PKCS#8 ECDSA P-256 key. SEC1 ("EC PRIVATE KEY") is
// accepted too: Apple does not issue that form, but a key round-tripped through
// openssl easily becomes it, and rejecting it would be a puzzling failure with
// no useful message.
func LoadAuthKey(path string) (*ecdsa.PrivateKey, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("could not read authkey %q: %v", path, err)
	}

	block, _ := pem.Decode(contents)
	if block == nil {
		return nil, fmt.Errorf("authkey %q is not PEM; it should be the .p8 file "+
			"downloaded from the developer portal, beginning \"-----BEGIN PRIVATE KEY-----\"", path)
	}

	var parsed interface{}
	switch block.Type {
	case "PRIVATE KEY":
		parsed, err = x509.ParsePKCS8PrivateKey(block.Bytes)
	case "EC PRIVATE KEY":
		parsed, err = x509.ParseECPrivateKey(block.Bytes)
	default:
		return nil, fmt.Errorf("authkey %q contains a %q block, expected \"PRIVATE KEY\"", path, block.Type)
	}
	if err != nil {
		return nil, fmt.Errorf("could not parse authkey %q: %v", path, err)
	}

	key, isECDSA := parsed.(*ecdsa.PrivateKey)
	if !isECDSA {
		// The likely mistake is pointing at a push certificate's RSA key, which
		// is a perfectly valid private key and useless here.
		return nil, fmt.Errorf("authkey %q holds a %T, but APNs token authentication needs an ECDSA key", path, parsed)
	}

	// ES256 means P-256 specifically, and ES256 is the only algorithm APNs
	// accepts. A P-384 or P-521 key parses perfectly well here and would then be
	// handed to a signing method that rejects it, so without this check the
	// mistake passes /addpsp and surfaces on the first push -- which is the
	// failure mode this function exists to prevent. Apple only issues P-256, so
	// another curve means the file was generated locally rather than downloaded.
	if key.Curve != elliptic.P256() {
		return nil, fmt.Errorf("authkey %q uses curve %s, but APNs requires ES256, which is P-256 only; "+
			"this should be the .p8 downloaded from the developer portal",
			path, key.Curve.Params().Name)
	}
	return key, nil
}

// ValidateTokenAuth checks a token-auth configuration at /addpsp time.
//
// Every problem here produces the same 403 InvalidProviderToken from Apple,
// with no indication of which field was wrong, so catching them where the
// values were typed is worth more than usual.
func ValidateTokenAuth(authKey, keyID, teamID string) error {
	if strings.TrimSpace(authKey) == "" {
		return fmt.Errorf("NoAuthKey")
	}
	if err := validateAppleID(KeyIDKey, keyID); err != nil {
		return err
	}
	if err := validateAppleID(TeamIDKey, teamID); err != nil {
		return err
	}
	// Read and parse it now rather than on the first push. uniqush reads this
	// file in its own process and possibly as another user, so a path that does
	// not resolve, or a file that is not a signing key at all, should be
	// reported to whoever just supplied it.
	if _, err := LoadAuthKey(authKey); err != nil {
		return err
	}

	warnIfKeyIsWidelyReadable(authKey)
	return nil
}

// warnIfKeyIsWidelyReadable says something when the .p8 is readable beyond its
// owner.
//
// Here rather than in LoadAuthKey, which runs on the push path: providerTokenFor
// reads the key once per batch, so warning there would emit a line per batch,
// for ever, on a deployment that has deliberately made the key group-readable.
// A warning nobody can turn off is a warning everybody filters out.
//
// /addpsp is the right place: it is where the operator supplied the path, it
// runs once, and it is the moment they can act on what they are told.
//
// A warning and not a refusal. uniqush may legitimately run as a different user
// from the one that installed the key, and turning a permission bit into a
// total push outage is a worse failure than the one being reported.
func warnIfKeyIsWidelyReadable(path string) {
	info, err := os.Stat(path)
	if err != nil {
		// Unreadable paths are LoadAuthKey's to report, and it already has.
		return
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		log.Printf("uniqush: warning: the APNs signing key %q is readable beyond its owner (mode %#o); "+
			"it is a non-expiring credential for the whole team, so 0600 is the right mode", path, perm)
	}
}

// appleIDPattern is the shape of a Key ID or a Team ID: ten characters of
// uppercase alphanumerics.
//
// Length alone made the two interchangeable to this check, and swapping keyid
// and teamid is a plausible thing to do when copying them out of the developer
// portal -- both are ten characters, and the portal shows them near each other.
// Apple answers the result with an opaque 403 InvalidProviderToken, which is
// exactly the class of failure this validation exists to pre-empt. The charset
// does not catch a swap either, but it does catch the other common mistakes:
// a pasted lowercase value, a truncated one, or a filename picked up instead of
// the identifier.
var appleIDPattern = regexp.MustCompile(`^[A-Z0-9]{10}$`)

func validateAppleID(field, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		// Named the way uniqush's other missing-field errors are -- NoCertificate,
		// NoPrivateKey, NoAuthKey -- rather than upper-casing the config key,
		// which produced NoKEYID and NoTEAMID. These strings reach the operator
		// through /addpsp.
		return fmt.Errorf("No%s", appleIDErrorName(field))
	}
	if len(value) != appleIDLength {
		return fmt.Errorf("%s %q is %d characters, but Apple's identifiers are %d",
			field, value, len(value), appleIDLength)
	}
	if !appleIDPattern.MatchString(value) {
		return fmt.Errorf("%s %q is not in Apple's format: ten characters, uppercase letters and digits only",
			field, value)
	}
	return nil
}

// appleIDErrorName renders a config key the way uniqush names it in errors.
func appleIDErrorName(field string) string {
	switch field {
	case KeyIDKey:
		return "KeyID"
	case TeamIDKey:
		return "TeamID"
	default:
		return field
	}
}
