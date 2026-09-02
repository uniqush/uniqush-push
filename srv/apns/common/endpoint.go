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
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync/atomic"

	"golang.org/x/net/idna"

	"github.com/uniqush/uniqush-push/push"
)

// Keys a provider may carry in VolatileData to describe where HTTP/2 pushes go
// and how that destination is trusted.
const (
	// EndpointKey holds an explicit base URL, e.g. "https://localhost:8443".
	// When absent, the destination is derived from AddrKey; see ResolveEndpoint.
	EndpointKey = "endpoint"

	// CACertKey holds a path to a PEM bundle to verify the endpoint against,
	// instead of the system roots.
	CACertKey = "cacert"

	// SkipVerifyKey disables certificate verification entirely when "true".
	SkipVerifyKey = "skipverify"

	// AddrKey is the binary protocol's host:port. It predates EndpointKey and
	// is still what selects between Apple's two environments for providers that
	// do not set an endpoint.
	AddrKey = "addr"

	// CredentialRevisionKey holds a digest of the credential *files* a provider
	// was built from -- the client certificate, its key, and the CA bundle.
	//
	// It exists so that the push path can tell whether those files have changed
	// without opening them. Rotating a credential in place leaves every path
	// identical, and the paths are what psp.Name() hashes, so nothing else about
	// the provider moves; without this the cached TLS client would go on
	// presenting the retired material until uniqush restarted.
	//
	// Computed once, by the builder, at /addpsp time -- which is the only moment
	// a rotation can take effect anyway, since a provider is reloaded from the
	// database rather than re-read from disk. Doing it here rather than on each
	// push is the difference between hashing three files once per registration
	// and doing it on every push for every provider.
	CredentialRevisionKey = "credrev"
)

// Apple's two HTTP/2 hosts.
const (
	HostProduction  = "https://api.push.apple.com"
	HostDevelopment = "https://api.development.push.apple.com"
)

// ResolveEndpoint returns the base URL HTTP/2 pushes for this provider go to.
//
// An explicit endpoint wins. Without one the environment is inferred from the
// binary protocol's addr, which is how this worked before endpoints could be
// configured at all, and which is why every provider created before this change
// keeps sending exactly where it used to.
//
// The inference is a substring match rather than a comparison against the two
// documented gateway hostnames, because addr also accepts a host:port for a
// local binary-protocol simulator, and those were conventionally named after
// the environment they stood in for.
func ResolveEndpoint(psp *push.PushServiceProvider) string {
	if endpoint := strings.TrimSpace(psp.VolatileData[EndpointKey]); endpoint != "" {
		return strings.TrimSuffix(endpoint, "/")
	}
	addr := psp.VolatileData[AddrKey]
	if strings.Contains(addr, "sandbox") || strings.Contains(addr, "api.development.") {
		return HostDevelopment
	}
	return HostProduction
}

// IsAppleHost reports whether an endpoint points at APNs itself.
//
// The hostname is normalised before comparing. A fully qualified name may carry
// the root label as a trailing dot -- "api.push.apple.com." resolves to exactly
// the same host -- and case is not significant in DNS. Both forms would slip
// past a naive comparison, and the only thing on the other side of this check is
// whether certificate verification may be switched off, so the sloppy version of
// it is a way to reach Apple with verification disabled.
// Matched on the domain rather than against a list of hostnames. The list was
// the original implementation and it was already wrong: it named
// api.push.apple.com and api.development.push.apple.com but not
// api.sandbox.push.apple.com, which is the same development environment under
// its older name. A provider pointed there could therefore be registered with
// skipverify=true, and uniqush would talk to Apple with certificate
// verification disabled.
//
// An enumeration has to be revisited every time Apple adds a name, and the cost
// of forgetting is silent: not a broken push, but a working push over an
// unverified connection. push.apple.com is Apple's own domain, so anything under
// it is Apple by construction and no future name can be missed.
//
// The leading dot in the suffix is load-bearing. Without it "evilpush.apple.com"
// would match, and the point of this function is to be conservative about what
// counts as Apple.
// The hostname is put through IDNA before it is compared, because that is what
// net/http does before dialling, and a check that normalises differently from
// the dialler is not a check.
//
// Unicode has several characters that IDNA maps to a label separator: U+3002
// IDEOGRAPHIC FULL STOP, U+FF0E FULLWIDTH FULL STOP, U+FF61 HALFWIDTH
// IDEOGRAPHIC FULL STOP. So "https://api.push.apple.com。" is a URL net/http
// happily dials as api.push.apple.com, while a byte comparison sees a host that
// is not Apple at all. Without this, /addpsp would accept that endpoint with
// skipverify=true and uniqush would open an unverified connection to Apple --
// the exact bypass this function exists to prevent, reached through a character
// nobody would think to look at.
//
// Conversion failures are treated as "not Apple", which is the safe direction:
// ShouldSkipVerify only ever *refuses* to disable verification on the strength
// of a true answer here, and a hostname that will not convert is not one that
// resolves to Apple either.
func IsAppleHost(endpoint string) bool {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil {
		return false
	}

	// Lookup rather than Registration: this is the profile used to resolve a
	// name, which is the operation about to happen, and it applies the case
	// folding and separator mapping that a byte comparison misses.
	host, err := idna.Lookup.ToASCII(parsed.Hostname())
	if err != nil {
		return false
	}
	host = strings.ToLower(host)
	host = strings.TrimSuffix(host, ".")

	return host == appleAPNSDomain || strings.HasSuffix(host, "."+appleAPNSDomain)
}

// appleAPNSDomain is the domain every APNs host lives under, for both the HTTP/2
// API (api*.push.apple.com) and the retired binary protocol
// (gateway*.push.apple.com).
const appleAPNSDomain = "push.apple.com"

// ShouldSkipVerify reports whether certificate verification may be disabled for
// this provider's connections.
//
// The rule is enforced here, at the point of use, and not only in
// ValidateEndpoint at /addpsp time. That is not belt and braces: a provider read
// back from the database is rebuilt by BuildPushServiceProviderFromBytes, which
// unserializes it directly and never calls the builder, so nothing it carries
// has necessarily been validated by the current code.
//
// That matters for a specific, existing case. skipverify predates the HTTP/2
// path and was silently ignored on it, so an operator who set it years ago for
// the binary-protocol simulator -- which srv/apns/apns-test/apns-test.sh tells
// them to do -- has it stored to this day. Honouring it now, as this package
// began to, would disable verification on connections that resolve to Apple.
//
// So the setting is honoured only when the destination is not Apple. Silently
// ignoring an operator's explicit setting is normally the wrong instinct; here
// the alternative is quietly downgrading a production connection, and the
// setting was inert on this path anyway until very recently.
func ShouldSkipVerify(psp *push.PushServiceProvider) bool {
	if psp.VolatileData[SkipVerifyKey] != "true" {
		return false
	}
	return !IsAppleHost(ResolveEndpoint(psp))
}

// allowNonApple governs whether a provider may be pointed at a host other than
// Apple's own. Off unless uniqush.conf turns it on.
//
// The endpoint setting exists so that uniqush can be pointed at a simulator, a
// staging relay, or a proxy. That is a real need, and it is also a way to make
// every push for a service go somewhere else: a push carries device tokens and
// the notification payload to whatever host it dials, and on a certificate
// provider it presents the APNs client certificate during that handshake. So
// the capability is kept, and the default is that it is not available.
//
// A flag rather than an address policy like srv/webpush/endpoint.go's. webpush
// is defending against endpoints supplied by *subscribers*, where a
// private-range destination is the whole attack and a public one is normal
// traffic. Here the endpoint comes from an operator through /addpsp, and the
// legitimate destinations -- a simulator on localhost, a relay inside the
// network -- are exactly the addresses such a policy blocks, while an
// attacker-controlled public host is exactly what it allows. Ranges would
// forbid every real use and permit the dangerous one.
//
// An atomic rather than a plain bool because it is written once at registration
// and read on the push path, and the race detector should not have to take that
// on trust.
var allowNonApple atomic.Bool

// SetAllowNonAppleEndpoints records whether non-Apple endpoints are permitted.
//
// Called from the apns push service when it is handed uniqush.conf, which
// happens at registration and therefore before any push.
func SetAllowNonAppleEndpoints(allow bool) { allowNonApple.Store(allow) }

// AllowsNonAppleEndpoints reports the current setting.
func AllowsNonAppleEndpoints() bool { return allowNonApple.Load() }

// ErrNonAppleEndpoint explains a refused destination.
//
// Named, because it is reported from two places -- /addpsp and the push path --
// and an operator who has hit one and then the other should recognise it as the
// same rule rather than two.
var ErrNonAppleEndpoint = errors.New("endpoint is not an Apple host; " +
	"set allow_non_apple_endpoints=true in the [apns] section of uniqush-push.conf to permit it")

// CheckEndpointAllowed re-validates a provider's destination at the point of
// use.
//
// The same checks /addpsp applies, for the same reason ShouldSkipVerify is
// re-derived here: a provider read back from the database is rebuilt by
// BuildPushServiceProviderFromBytes, which unserializes it directly and never
// runs the builder. Nothing it carries has necessarily been through the current
// code, or through any validation at all if the row was written by hand.
//
// The whole of ValidateEndpoint, not only the Apple-host policy. Checking the
// host alone left the shape unchecked, and the shape is what decides whether a
// push is encrypted: a stored endpoint of "http://api.push.apple.com" passes any
// hostname test there is, and would have sent device tokens and payload
// contents in the clear to a host uniqush is entitled to talk to. With the
// non-Apple opt-in enabled, any cleartext URL would have done the same.
//
// ShouldSkipVerify rather than the stored skipverify flag. The two differ for
// exactly the providers that must keep working: skipverify predates the HTTP/2
// path and was silently ignored there, so an operator who set it years ago for
// the binary-protocol simulator still has it stored while pointing at Apple.
// ShouldSkipVerify already refuses to honour that combination, whereas passing
// the raw setting would make ValidateEndpoint reject the provider outright --
// turning a stale flag on a working provider into an outage on every push.
func CheckEndpointAllowed(psp *push.PushServiceProvider) error {
	return ValidateEndpoint(ResolveEndpoint(psp), ShouldSkipVerify(psp))
}

// ValidateEndpoint checks an operator-supplied endpoint at /addpsp time.
//
// The point is to fail where the mistake was made. An endpoint that is wrong in
// any of these ways would otherwise surface much later as a connection error or,
// worse, as pushes quietly going somewhere else -- and a push carries device
// tokens and payload contents to whatever host it dials.
func ValidateEndpoint(endpoint string, skipVerify bool) error {
	endpoint = strings.TrimSpace(endpoint)
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("endpoint %q is not a URL: %v", endpoint, err)
	}

	// http:// would send the certificate and the payload in the clear, and APNs
	// has never accepted it. There is no test setup that needs it either: the
	// simulator in srv/apns/apnstest serves TLS precisely so this stays true.
	if parsed.Scheme != "https" {
		return fmt.Errorf("endpoint %q must use https, got scheme %q", endpoint, parsed.Scheme)
	}
	// Hostname() rather than Host, because Host includes the port and is
	// non-empty for an authority that is nothing but one. "https://:443" parses
	// with Host ":443" and Hostname "", so checking Host accepted an endpoint
	// with no destination at all and left it to fail on the first push --
	// exactly the kind of deferred failure this function exists to prevent,
	// since by then whoever supplied it has moved on.
	if parsed.Hostname() == "" {
		return fmt.Errorf("endpoint %q has no host", endpoint)
	}

	// url.Parse only checks that a port is decimal digits, not that it is a
	// port: it accepts ":65536", ":99999" and ":0" without complaint. Each of
	// those parses, passes the Apple-host check, and is stored -- and then every
	// push for that provider fails while dialling, a long way from the /addpsp
	// that accepted it. Catching it here reports the typo to whoever just made
	// it.
	if port := parsed.Port(); port != "" {
		number, convErr := strconv.Atoi(port)
		if convErr != nil || number < 1 || number > 65535 {
			return fmt.Errorf("endpoint %q has port %q, which is not in 1-65535", endpoint, port)
		}
	}

	// uniqush appends "/3/device/<token>", so anything already in the path
	// would produce a URL nobody intended.
	if parsed.Path != "" && parsed.Path != "/" {
		return fmt.Errorf("endpoint %q must not have a path; uniqush appends /3/device/<token>", endpoint)
	}
	// Checked against the raw string, not against parsed.RawQuery and
	// parsed.Fragment. Those are both empty for an endpoint ending in a bare
	// "?" or "#", so the delimiter-only forms passed -- and since the endpoint
	// is concatenated with "/3/device/<token>", the device path then became a
	// query string or a fragment and the push went to "/".
	if strings.ContainsAny(endpoint, "?#") {
		return fmt.Errorf("endpoint %q must not contain %q or %q; uniqush appends /3/device/<token> to it",
			endpoint, "?", "#")
	}

	// Disabling verification against Apple is never the right answer, and it is
	// an easy option to leave behind after testing against a simulator. It also
	// defeats the only thing stopping a push from being served by whoever holds
	// the DNS answer for that name.
	if skipVerify && IsAppleHost(endpoint) {
		return errors.New("skipverify cannot be used with Apple's own endpoints; " +
			"it disables the certificate check that makes the connection meaningful")
	}

	// Last, so that an endpoint which is malformed *and* not permitted is
	// reported as malformed. That is the more useful of the two answers: it
	// names a mistake to fix, where the other names a policy to change, and an
	// operator who turns the flag on to get past this would then hit the parse
	// error anyway.
	if !AllowsNonAppleEndpoints() && !IsAppleHost(endpoint) {
		return fmt.Errorf("%q: %w", endpoint, ErrNonAppleEndpoint)
	}
	return nil
}

// LoadCACert reads a PEM bundle into a certificate pool.
//
// The pool deliberately starts empty rather than from the system roots. A
// caller naming a CA is saying the endpoint is verified by that CA and not by
// the 150-odd commercial authorities a machine happens to trust, which is the
// whole reason to prefer this over skipverify when testing.
func LoadCACert(path string) (*x509.CertPool, error) {
	pem, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("could not read cacert %q: %v", path, err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("cacert %q contains no PEM certificates", path)
	}
	return pool, nil
}

// ValidateCACert reports whether a CA bundle is usable, without keeping it.
func ValidateCACert(path string) error {
	_, err := LoadCACert(path)
	return err
}

// CredentialRevision digests the credential files a provider was built from, so
// that a change to any of their contents produces a different value.
//
// Order is fixed and each path is included alongside its digest, so that moving
// a credential between the cert and CA slots, or clearing one, is a change too.
// An unreadable file contributes an empty digest rather than an error: the
// builder has already validated the ones it requires, and a value here is only
// ever compared against another value here.
func CredentialRevision(certPath, keyPath, caCertPath string) string {
	parts := make([]string, 0, 6)
	for _, path := range []string{certPath, keyPath, caCertPath} {
		parts = append(parts, path, FileFingerprint(path))
	}
	digest := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(digest[:])
}

// FileFingerprint returns a digest of a credential file's contents.
//
// Used to key cached clients, for every file a TLS config is built from: the CA
// bundle, the client certificate, and its private key. The pathname is not
// enough, because paths outlive the material they point at. Rotating a
// credential in place -- writing the new file over the old one and re-running
// /addpsp -- leaves the path identical, so a cache keyed on the path alone would
// go on using a client built from the retired material until a restart.
//
// For the CA that fails in both directions at once: the new authority does not
// take effect and the old one is still trusted.
//
// For the client certificate it is not a corner case but the annual routine. An
// APNs certificate expires every year; the operator drops the replacement at the
// same path and re-runs /addpsp. Nothing about the provider's identity changes
// -- the paths live in FixedData, which is what psp.Name() hashes, so the name
// is identical, and using a *different* path instead would be rejected as a
// different provider. Without the contents in the key there is no way to start
// using a renewed certificate short of restarting uniqush.
//
// An unreadable file returns an empty fingerprint rather than an error. The
// caller is building a cache key on a path that createTLSConfig is about to
// fail on anyway, with a better message than this could give.
func FileFingerprint(path string) string {
	if path == "" {
		return ""
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(contents)
	return hex.EncodeToString(digest[:])
}
