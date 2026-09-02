package common

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uniqush/uniqush-push/push"
)

func pspWith(volatile map[string]string) *push.PushServiceProvider {
	psp := push.NewEmptyPushServiceProvider()
	for key, value := range volatile {
		psp.VolatileData[key] = value
	}
	return psp
}

// TestResolveEndpointKeepsExistingProvidersWhereTheyWere is the compatibility
// test, and the reason the addr heuristic survives at all.
//
// Every APNs provider in every existing database predates the endpoint setting
// and has only addr. If this inference changes, those providers silently start
// pushing to the other environment -- where their device tokens are not valid,
// so the symptom is a flood of BadDeviceToken and mass unsubscription rather
// than an obvious failure.
func TestResolveEndpointKeepsExistingProvidersWhereTheyWere(t *testing.T) {
	cases := []struct {
		addr string
		want string
	}{
		{"gateway.push.apple.com:2195", HostProduction},
		{"gateway.sandbox.push.apple.com:2195", HostDevelopment},
		{"api.development.push.apple.com:443", HostDevelopment},
		// An unrecognised addr means a binary-protocol simulator, and
		// production is what uniqush has always assumed for one.
		{"127.0.0.1:2195", HostProduction},
		{"", HostProduction},
	}
	for _, testCase := range cases {
		got := ResolveEndpoint(pspWith(map[string]string{AddrKey: testCase.addr}))
		if got != testCase.want {
			t.Errorf("addr %q: expected %s, got %s", testCase.addr, testCase.want, got)
		}
	}
}

func TestResolveEndpointPrefersAnExplicitEndpoint(t *testing.T) {
	// Note the addr says production while the endpoint says otherwise: an
	// explicit setting has to win, or it would be impossible to point a
	// provider at anything but Apple.
	psp := pspWith(map[string]string{
		AddrKey:     "gateway.push.apple.com:2195",
		EndpointKey: "https://localhost:8443",
	})
	if got := ResolveEndpoint(psp); got != "https://localhost:8443" {
		t.Errorf("Expected the explicit endpoint, got %s", got)
	}

	// A trailing slash would produce "https://host//3/device/...".
	psp.VolatileData[EndpointKey] = "https://localhost:8443/"
	if got := ResolveEndpoint(psp); got != "https://localhost:8443" {
		t.Errorf("Expected the trailing slash to be trimmed, got %s", got)
	}
}

func TestValidateEndpoint(t *testing.T) {
	allowNonAppleEndpoints(t)
	valid := []string{
		"https://localhost:8443",
		"https://localhost:8443/",
		"https://apns-simulator.test",
		HostProduction,
		HostDevelopment,
	}
	for _, endpoint := range valid {
		if err := ValidateEndpoint(endpoint, false); err != nil {
			t.Errorf("Expected %q to be accepted, got: %v", endpoint, err)
		}
	}

	invalid := map[string]string{
		// Would send the client certificate and the payload in the clear.
		"http://localhost:8443": "https",
		"localhost:8443":        "https",
		// uniqush appends /3/device/<token>, so a path here is always a mistake.
		"https://localhost:8443/apns": "path",
		"https://localhost:8443/?a=b": "?",
		// Delimiter-only forms. url.Parse leaves RawQuery and Fragment empty
		// for these, so checking those fields let them through -- and the
		// device path appended afterwards became a query or a fragment, sending
		// the push to "/".
		"https://localhost:8443?":     "?",
		"https://localhost:8443#":     "?",
		"https://localhost:8443/?":    "?",
		"https://localhost:8443/#":    "?",
		"https://localhost:8443#frag": "?",
		"https://":                    "host",
	}
	for endpoint, wantSubstring := range invalid {
		err := ValidateEndpoint(endpoint, false)
		if err == nil {
			t.Errorf("Expected %q to be rejected", endpoint)
			continue
		}
		if !strings.Contains(err.Error(), wantSubstring) {
			t.Errorf("Expected the error for %q to mention %q, got: %v", endpoint, wantSubstring, err)
		}
	}
}

// TestSkipVerifyIsRefusedForApple guards the setting most likely to be left
// behind after testing.
//
// skipverify is how you point uniqush at a simulator with a self-signed
// certificate. Carried over to a production provider it would disable the only
// check that the host answering for api.push.apple.com is Apple, which is
// exactly the sort of thing that survives a copy-pasted /addpsp for years.
func TestSkipVerifyIsRefusedForApple(t *testing.T) {
	allowNonAppleEndpoints(t)
	for _, endpoint := range []string{HostProduction, HostDevelopment} {
		if err := ValidateEndpoint(endpoint, true); err == nil {
			t.Errorf("Expected skipverify to be refused for %s", endpoint)
		}
	}
	// Still allowed where it is the point.
	if err := ValidateEndpoint("https://localhost:8443", true); err != nil {
		t.Errorf("Expected skipverify to be allowed for a local endpoint, got: %v", err)
	}
}

// TestValidateEndpointRejectsAnAuthorityWithNoHostname covers a URL that looks
// like it has a host and does not.
//
// url.Parse fills Host with the whole authority, port included, so "https://:443"
// has a Host of ":443" -- non-empty -- and a Hostname of "". Checking Host
// therefore accepted an endpoint with no destination at all and left it to fail
// on the first push, which is the deferred failure this validation exists to
// prevent: by then whoever typed it has moved on, and the error surfaces as a
// push problem rather than a configuration one.
func TestValidateEndpointRejectsAnAuthorityWithNoHostname(t *testing.T) {
	for _, endpoint := range []string{
		"https://:443",
		"https://:8443",
		// The same shape with the port omitted too.
		"https://",
	} {
		if err := ValidateEndpoint(endpoint, false); err == nil {
			t.Errorf("Expected %q to be rejected: it has a port but no host to send to", endpoint)
		}
	}
}

func TestIsAppleHost(t *testing.T) {
	appleHosts := []string{
		HostProduction,
		HostDevelopment,
		// Case and port must not be a way around the check.
		"https://API.PUSH.APPLE.COM",
		"https://api.push.apple.com:443",

		// The development environment under its older name.
		//
		// An enumeration of hostnames missed this one, and /addpsp would then
		// accept it with skipverify=true -- uniqush talking to Apple with
		// certificate verification disabled. It is the reason the check matches
		// on the domain rather than on a list.
		"https://api.sandbox.push.apple.com",
		"https://api.sandbox.push.apple.com:2197",

		// Apple's alternate HTTP/2 port, and the hosts of the retired binary
		// protocol. Not endpoints uniqush would normally be pointed at, but all
		// of them are Apple, and the only question this answers is whether
		// verification may be switched off.
		"https://api.push.apple.com:2197",
		"https://gateway.push.apple.com",
		"https://gateway.sandbox.push.apple.com",

		// A subdomain Apple has not used yet. Enumerating hostnames means
		// revisiting the list every time one appears, and the cost of forgetting
		// is not a broken push but a working push over an unverified connection.
		"https://api.somethingnew.push.apple.com",

		// Also Apple, though an enumeration would have called it a lookalike.
		// It is a subdomain of Apple's own domain, so the safe reading is that
		// it is Apple and verification stays on.
		"https://notapi.push.apple.com",

		// A fully qualified name carrying the root label. It resolves to the
		// same host, and a naive comparison would not recognise it -- which
		// would be a way to reach Apple with verification disabled.
		"https://api.push.apple.com.",
		"https://api.development.push.apple.com.",
		"https://API.PUSH.APPLE.COM.:443",

		// Unicode label separators. IDNA maps every one of these to an ASCII
		// dot, so net/http dials api.push.apple.com -- but a byte comparison
		// sees a host that is not Apple at all, and /addpsp would have accepted
		// skipverify=true for a real connection to Apple.
		//
		// U+3002 IDEOGRAPHIC FULL STOP:
		"https://api.push.apple.com。",
		// U+FF0E FULLWIDTH FULL STOP:
		"https://api.push.apple.com．",
		// U+FF61 HALFWIDTH IDEOGRAPHIC FULL STOP:
		"https://api.push.apple.com｡",
		// And as separators inside the name rather than only as a trailing
		// root label.
		"https://api。push。apple。com",
		"https://api．push．apple．com:2197",
		// Fullwidth letters fold to ASCII under the same mapping.
		"https://ＡＰＩ.push.apple.com",
	}
	for _, endpoint := range appleHosts {
		if !IsAppleHost(endpoint) {
			t.Errorf("Expected %q to be recognised as Apple", endpoint)
		}
	}

	notApple := []string{
		"https://localhost:8443",
		// Lookalikes. Substring matching would get these wrong, and so would
		// trimming the root label too eagerly.
		"https://api.push.apple.com.evil.test",
		"https://api.push.apple.com.evil.test.",
		// The suffix match needs its leading dot: without it this would pass for
		// Apple, and a domain anyone can register would be able to demand that
		// verification stay on -- or, worse, be mistaken for Apple elsewhere.
		"https://evilpush.apple.com",
		"https://pushxapple.com",
		"",
	}
	for _, endpoint := range notApple {
		if IsAppleHost(endpoint) {
			t.Errorf("Expected %q not to be recognised as Apple", endpoint)
		}
	}
}

// TestShouldSkipVerifyIgnoresStaleSettingsOnAppleHosts is the regression test
// for a real exposure this branch introduced.
//
// skipverify predates the HTTP/2 path and was ignored on it, so the flag sat
// harmlessly in the databases of anyone who followed
// srv/apns/apns-test/apns-test.sh -- which sets skipverify=true. Honouring it,
// as the HTTP/2 code began to, disabled certificate verification on connections
// that resolve to Apple.
//
// /addpsp rejects the combination, but that only covers providers being
// registered now: BuildPushServiceProviderFromBytes rebuilds a stored provider
// without going near the builder, so the rule has to hold at the point of use.
func TestShouldSkipVerifyIgnoresStaleSettingsOnAppleHosts(t *testing.T) {
	cases := []struct {
		name     string
		volatile map[string]string
		want     bool
	}{
		{
			name:     "a stored flag with no endpoint, which resolves to Apple",
			volatile: map[string]string{SkipVerifyKey: "true", AddrKey: "gateway.push.apple.com:2195"},
			want:     false,
		},
		{
			name:     "the same, for the sandbox",
			volatile: map[string]string{SkipVerifyKey: "true", AddrKey: "gateway.sandbox.push.apple.com:2195"},
			want:     false,
		},
		{
			name:     "an explicit Apple endpoint",
			volatile: map[string]string{SkipVerifyKey: "true", EndpointKey: HostProduction},
			want:     false,
		},
		{
			name:     "an Apple endpoint with the root label",
			volatile: map[string]string{SkipVerifyKey: "true", EndpointKey: "https://api.push.apple.com."},
			want:     false,
		},
		{
			name:     "a simulator, where the flag is the point",
			volatile: map[string]string{SkipVerifyKey: "true", EndpointKey: "https://localhost:8443"},
			want:     true,
		},
		{
			name:     "no flag at all",
			volatile: map[string]string{EndpointKey: "https://localhost:8443"},
			want:     false,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := ShouldSkipVerify(pspWith(testCase.volatile)); got != testCase.want {
				t.Errorf("Expected ShouldSkipVerify=%v, got %v", testCase.want, got)
			}
		})
	}
}

// TestFileFingerprintTracksContents covers the cache-key bug: a credential rotated in
// place keeps its pathname, so a client cached against the path would go on
// trusting the retired authority until the process restarted.
func TestFileFingerprintTracksContents(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ca.pem")

	if err := os.WriteFile(path, []byte("first contents"), 0600); err != nil {
		t.Fatalf("Could not write the bundle: %v", err)
	}
	first := FileFingerprint(path)
	if first == "" {
		t.Fatal("Expected a fingerprint for a readable file")
	}
	if again := FileFingerprint(path); again != first {
		t.Error("The fingerprint of unchanged contents changed")
	}

	// Rotated in place: same path, new authority.
	if err := os.WriteFile(path, []byte("second contents"), 0600); err != nil {
		t.Fatalf("Could not rotate the bundle: %v", err)
	}
	if rotated := FileFingerprint(path); rotated == first {
		t.Error("The fingerprint did not change when the bundle did; " +
			"a client cached against it would keep trusting the retired CA")
	}

	if FileFingerprint("") != "" {
		t.Error("Expected no fingerprint for an unset path")
	}
	if FileFingerprint(filepath.Join(dir, "absent.pem")) != "" {
		t.Error("Expected no fingerprint for an unreadable path")
	}
}

func TestLoadCACert(t *testing.T) {
	dir := t.TempDir()

	t.Run("a missing file is an error", func(t *testing.T) {
		if err := ValidateCACert(filepath.Join(dir, "absent.pem")); err == nil {
			t.Error("Expected an error for a missing CA bundle")
		}
	})

	t.Run("a file that is not PEM is an error", func(t *testing.T) {
		// The realistic mistake is naming a DER .cer, or a private key, or a
		// certificate that is there but unparseable. AppendCertsFromPEM reports
		// all of those the same way: by returning false rather than failing.
		path := filepath.Join(dir, "garbage.pem")
		if err := os.WriteFile(path, []byte("this is not a certificate"), 0600); err != nil {
			t.Fatalf("Could not write the fixture: %v", err)
		}
		if err := ValidateCACert(path); err == nil {
			t.Error("Expected an error for a file containing no PEM certificates")
		}
	})
}

// TestPortsOutsideTheValidRangeAreRefused covers what url.Parse does not check.
//
// It validates that a port is decimal digits and stops there, so ":65536",
// ":99999" and ":0" all parse cleanly, pass every other check here, and get
// stored. The failure then surfaces on every push, while dialling -- a long way
// from the /addpsp that accepted it, and with an error that says nothing about
// the endpoint being wrong.
func TestPortsOutsideTheValidRangeAreRefused(t *testing.T) {
	for _, endpoint := range []string{
		"https://api.push.apple.com:65536", // one past the top
		"https://api.push.apple.com:99999",
		"https://api.push.apple.com:0", // port 0 means "pick one", never a destination
		"https://relay.example:70000",
	} {
		if err := ValidateEndpoint(endpoint, false); err == nil {
			t.Errorf("Expected %q to be refused: the port is not in 1-65535", endpoint)
		}
	}

	// The edges themselves are fine, and so is having no port at all.
	for _, endpoint := range []string{
		"https://api.push.apple.com:1",
		"https://api.push.apple.com:65535",
		"https://api.push.apple.com:443",
		"https://api.push.apple.com:2197", // Apple's alternate HTTP/2 port
		"https://api.push.apple.com",
	} {
		if err := ValidateEndpoint(endpoint, false); err != nil {
			t.Errorf("Expected %q to be accepted, got: %v", endpoint, err)
		}
	}
}

// allowNonAppleEndpoints permits non-Apple destinations for one test.
//
// Most of what this file checks -- schemes, paths, query delimiters, empty
// hosts -- is about the shape of an endpoint, and the only endpoints with an
// interesting shape are the ones nobody would point at Apple. Those cases are
// about the parser, not the policy, so they turn the policy off the way an
// operator would.
func allowNonAppleEndpoints(t *testing.T) {
	t.Helper()
	previous := AllowsNonAppleEndpoints()
	SetAllowNonAppleEndpoints(true)
	t.Cleanup(func() { SetAllowNonAppleEndpoints(previous) })
}

// TestNonAppleEndpointsAreRefusedByDefault covers the gate itself.
//
// An endpoint is where every push for a service goes, carrying device tokens
// and the payload, and on a certificate provider it is also where the APNs
// client certificate gets presented. Anyone who can reach /addpsp could
// therefore redirect a service's whole push stream by supplying one, so the
// capability is opt-in.
func TestNonAppleEndpointsAreRefusedByDefault(t *testing.T) {
	if AllowsNonAppleEndpoints() {
		t.Fatal("Non-Apple endpoints should be refused unless uniqush.conf enables them")
	}

	for _, endpoint := range []string{
		"https://apns.attacker.example",
		"https://localhost:8443",
		// Not Apple: the leading dot in the suffix is what stops this matching,
		// and it is the shape an attacker would choose.
		"https://evilpush.apple.com",
	} {
		if err := ValidateEndpoint(endpoint, false); err == nil {
			t.Errorf("Expected %q to be refused while non-Apple endpoints are disabled", endpoint)
		} else if !errors.Is(err, ErrNonAppleEndpoint) {
			t.Errorf("Expected %q to be refused as a non-Apple endpoint, got: %v", endpoint, err)
		}
	}

	// Apple's own hosts are unaffected by the flag, in either position.
	for _, endpoint := range []string{"https://api.push.apple.com", "https://api.sandbox.push.apple.com"} {
		if err := ValidateEndpoint(endpoint, false); err != nil {
			t.Errorf("Expected Apple's own endpoint %q to be accepted, got: %v", endpoint, err)
		}
	}
}

// TestTheFlagDoesNotExcuseAMalformedEndpoint keeps the two rules independent.
//
// Turning the flag on says where pushes may go, not that anything goes. An
// operator who enables it should still be told that their URL has a path.
func TestTheFlagDoesNotExcuseAMalformedEndpoint(t *testing.T) {
	allowNonAppleEndpoints(t)

	for _, endpoint := range []string{
		"http://relay.example",
		"https://relay.example/3/device",
		"https://relay.example?x=1",
		"https://:443",
	} {
		if err := ValidateEndpoint(endpoint, false); err == nil {
			t.Errorf("Expected %q to be refused even with non-Apple endpoints permitted", endpoint)
		} else if errors.Is(err, ErrNonAppleEndpoint) {
			t.Errorf("Expected %q to be refused for its shape, not by the policy: %v", endpoint, err)
		}
	}
}

// TestAMalformedEndpointIsReportedAsMalformed pins the ordering of the two
// ways an endpoint can be refused.
//
// An endpoint can be malformed -- wrong scheme, a path, a query delimiter -- and
// it can be disallowed by policy for pointing somewhere other than Apple. An
// endpoint that is *both* is reported as malformed, because that names a mistake
// to fix rather than a policy to change: an operator who enabled the opt-in to
// get past the policy error would hit the parse error immediately afterwards.
func TestAMalformedEndpointIsReportedAsMalformed(t *testing.T) {
	err := ValidateEndpoint("http://relay.example", false)
	if err == nil {
		t.Fatal("Expected an http:// endpoint to be refused")
	}
	if errors.Is(err, ErrNonAppleEndpoint) {
		t.Errorf("An endpoint that is malformed and not permitted should be reported as malformed, got: %v", err)
	}
}

// TestStoredEndpointsAreRevalidatedAtPushTime covers the guard on the push
// path rather than the one at /addpsp.
//
// A provider read back from the database is rebuilt by
// BuildPushServiceProviderFromBytes, which unserializes it directly and never
// runs the builder -- so nothing it carries has necessarily been validated by
// the current code, or by any code at all if the row was written by hand.
//
// Checking only the hostname there was not enough, and the gap was the one that
// matters most: "http://api.push.apple.com" satisfies any Apple-host test, and
// would have carried device tokens and payload contents in the clear.
func TestStoredEndpointsAreRevalidatedAtPushTime(t *testing.T) {
	stored := func(endpoint string, extra map[string]string) *push.PushServiceProvider {
		psp := push.NewEmptyPushServiceProvider()
		psp.FixedData["service"] = "stored"
		psp.VolatileData[EndpointKey] = endpoint
		for key, value := range extra {
			psp.VolatileData[key] = value
		}
		return psp
	}

	t.Run("cleartext to Apple is refused", func(t *testing.T) {
		// Passes IsAppleHost, so a hostname-only check let it through.
		if err := CheckEndpointAllowed(stored("http://api.push.apple.com", nil)); err == nil {
			t.Error("Expected a stored http:// endpoint to be refused at push time; " +
				"device tokens and the payload would go out in the clear")
		}
	})

	t.Run("cleartext elsewhere is refused even when the opt-in is on", func(t *testing.T) {
		allowNonAppleEndpoints(t)
		if err := CheckEndpointAllowed(stored("http://relay.example", nil)); err == nil {
			t.Error("Expected a stored http:// endpoint to be refused even with non-Apple " +
				"endpoints permitted; the opt-in says where pushes may go, not that anything goes")
		}
	})

	t.Run("a path is refused", func(t *testing.T) {
		// uniqush appends /3/device/<token>, so a stored path produces a URL
		// nobody intended.
		if err := CheckEndpointAllowed(stored("https://api.push.apple.com/oops", nil)); err == nil {
			t.Error("Expected a stored endpoint carrying a path to be refused at push time")
		}
	})

	t.Run("non-Apple is refused by default", func(t *testing.T) {
		err := CheckEndpointAllowed(stored("https://relay.example", nil))
		if err == nil {
			t.Fatal("Expected a stored non-Apple endpoint to be refused while the opt-in is off")
		}
		if !errors.Is(err, ErrNonAppleEndpoint) {
			t.Errorf("Expected the policy error, got: %v", err)
		}
	})

	t.Run("Apple is allowed", func(t *testing.T) {
		if err := CheckEndpointAllowed(stored(HostProduction, nil)); err != nil {
			t.Errorf("Expected Apple's own endpoint to be allowed, got: %v", err)
		}
	})

	t.Run("a legacy stale skipverify against Apple still pushes", func(t *testing.T) {
		// skipverify predates the HTTP/2 path and was ignored there, so an
		// operator who set it years ago for the binary-protocol simulator still
		// has it stored. ShouldSkipVerify already refuses to honour it against
		// Apple; this check must not go further and refuse the push itself,
		// which would turn a stale flag into an outage.
		psp := stored(HostProduction, map[string]string{SkipVerifyKey: "true"})
		if err := CheckEndpointAllowed(psp); err != nil {
			t.Errorf("A provider carrying a stale skipverify against Apple must still push "+
				"(verification stays on); got: %v", err)
		}
	})
}
