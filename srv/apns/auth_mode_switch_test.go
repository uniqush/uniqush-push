package apns

import (
	"strings"
	"testing"

	"github.com/uniqush/uniqush-push/push"
	"github.com/uniqush/uniqush-push/srv/apns/apnstest"
	"github.com/uniqush/uniqush-push/srv/apns/common"
)

// Tests for handing buildCredentials a provider that already carries
// credentials.
//
// uniqush's own caller never does: the push service manager always builds from
// a fresh NewEmptyPushServiceProvider. But BuildPushServiceProviderFromMap is
// on the exported push.PushServiceType interface, so an embedder can pass
// anything, and these call buildCredentials directly because that is the only
// way to reach the case.
//
// The answer is that it is refused. Rewriting a provider's credentials in place
// cannot be done correctly from here: PushPeer.Name() memoises its result on
// first call and offers no invalidation, so a provider whose name has already
// been read keeps reporting the old one after its FixedData changes. Every
// delivery point written against it afterwards is bound to an identity that no
// longer describes it -- the same silent-unsubscribe failure the FixedData
// rules exist to prevent, reached from the other direction.
//
// This is not the operator-facing migration story. An operator cannot switch a
// live service between the two modes at all: they hash to different provider
// names and db.pushdb refuses the second as conflicting with the first.
// Migrating means removing the provider and adding it again.

// certificateProvider returns a working certificate provider, as one read back
// from the database would be.
//
// Built through the manager rather than by hand so that Name() works: the name
// is a hash of FixedData prefixed with the push service type, and a provider
// assembled directly has no type to ask.
func certificateProvider(t *testing.T) *push.PushServiceProvider {
	t.Helper()
	ensureAPNSRegistered()

	psp, err := push.GetPushServiceManager().BuildPushServiceProviderFromMap(map[string]string{
		"service":         "migrating",
		"pushservicetype": "apns",
		"cert":            "apns-test/localhost.cert",
		"key":             "apns-test/localhost.key",
		"bundleid":        "com.example.migrating",
	})
	if err != nil {
		t.Fatalf("Could not build a certificate provider: %v", err)
	}
	return psp
}

// tokenProvider returns a working token-auth provider, likewise built through
// the manager.
func tokenProvider(t *testing.T, key *apnstest.SigningKey) *push.PushServiceProvider {
	t.Helper()
	ensureAPNSRegistered()

	psp, err := push.GetPushServiceManager().BuildPushServiceProviderFromMap(map[string]string{
		"service":         "migrating",
		"pushservicetype": "apns",
		"bundleid":        "com.example.migrating",
		common.AuthKeyKey: key.Path,
		common.KeyIDKey:   key.KeyID,
		common.TeamIDKey:  key.TeamID,
	})
	if err != nil {
		t.Fatalf("Could not build a token provider: %v", err)
	}
	return psp
}

// newSigningKey generates a .p8 for these tests.
func newSigningKey(t *testing.T) *apnstest.SigningKey {
	t.Helper()
	key, err := apnstest.GenerateSigningKey(t.TempDir(), "KEYID12345", "TEAMID6789")
	if err != nil {
		t.Fatalf("Could not generate a signing key: %v", err)
	}
	return key
}

// TestSwitchingAuthModeInPlaceIsRefused covers both directions.
//
// Either one used to rewrite the provider, clearing the credentials for the
// mode not chosen. That looked like a migration and was not one: the name had
// already been decided, and nothing here could change it.
func TestSwitchingAuthModeInPlaceIsRefused(t *testing.T) {
	key := newSigningKey(t)

	t.Run("certificate to token", func(t *testing.T) {
		psp := certificateProvider(t)
		err := buildCredentials(map[string]string{
			common.AuthKeyKey: key.Path,
			common.KeyIDKey:   key.KeyID,
			common.TeamIDKey:  key.TeamID,
		}, psp)
		if err == nil {
			t.Fatal("Expected rewriting a certificate provider into a token provider to be refused")
		}
		if !strings.Contains(err.Error(), "already has a certificate") {
			t.Errorf("Expected the refusal to name what the provider already carries, got: %v", err)
		}
		// Refused means untouched: a rejected build must not half-apply.
		if psp.FixedData["cert"] == "" {
			t.Error("The refusal cleared the certificate anyway, which is the mutation it exists to prevent")
		}
		if common.UsesTokenAuth(psp) {
			t.Error("The refusal installed the signing key anyway")
		}
	})

	t.Run("token to certificate", func(t *testing.T) {
		psp := tokenProvider(t, key)
		err := buildCredentials(map[string]string{
			"cert": "apns-test/localhost.cert",
			"key":  "apns-test/localhost.key",
		}, psp)
		if err == nil {
			t.Fatal("Expected rewriting a token provider into a certificate provider to be refused")
		}
		if !strings.Contains(err.Error(), "already has a signing key") {
			t.Errorf("Expected the refusal to name what the provider already carries, got: %v", err)
		}
		if psp.VolatileData[common.AuthKeyKey] != key.Path {
			t.Error("The refusal cleared the signing key that was working, turning a rejected " +
				"update into an outage")
		}
		if psp.FixedData["cert"] != "" {
			t.Error("The refusal installed the certificate anyway")
		}
	})
}

// TestRewritingANamedProviderIsRefused is the sequence that made rewriting
// unsafe in the first place, and the one the earlier version of these tests
// deliberately stepped around.
//
// PushPeer.Name() memoises on first call. A caller that has already asked for
// the name -- which anything storing delivery points against a provider has
// necessarily done -- would keep getting the old one after its FixedData
// changed, so the provider's stored identity and its actual credentials would
// disagree with no error anywhere.
//
// Refusing removes the question. The name is read here *before* the build, on
// the same provider, which is exactly what the previous test avoided doing.
func TestRewritingANamedProviderIsRefused(t *testing.T) {
	key := newSigningKey(t)
	psp := certificateProvider(t)

	nameBefore := psp.Name()
	if nameBefore == "" {
		t.Fatal("Expected the provider to have a name")
	}

	if err := buildCredentials(map[string]string{
		common.AuthKeyKey: key.Path,
		common.KeyIDKey:   key.KeyID,
		common.TeamIDKey:  key.TeamID,
	}, psp); err == nil {
		t.Fatal("Expected a provider whose name has been read to be refused")
	}

	if psp.Name() != nameBefore {
		t.Error("The provider's name changed, which PushPeer.Name's memo says it cannot")
	}
	if common.UsesTokenAuth(psp) {
		t.Error("The provider was rewritten despite the refusal, so its credentials no longer " +
			"match the name every delivery point is stored against")
	}
}

// TestWhitespaceOnlyTokenFieldsAreTreatedAsAbsent covers a normalisation the
// partial-token check originally skipped.
//
// Every other field here is trimmed before being judged, because a form post
// carries whatever the client rendered: a UI that always emits keyid= and
// teamid= inputs sends them empty, or holding a stray space, for a provider
// authenticating with a certificate. The partial-token check read the raw
// values, so such a request was rejected with NoAuthKey -- naming fields the
// operator never filled in, for a certificate configuration that was perfectly
// valid.
func TestWhitespaceOnlyTokenFieldsAreTreatedAsAbsent(t *testing.T) {
	for _, blank := range []string{"", " ", "\t", "\n  "} {
		psp := push.NewEmptyPushServiceProvider()
		psp.FixedData["service"] = "certificates"

		err := buildCredentials(map[string]string{
			"cert":           "apns-test/localhost.cert",
			"key":            "apns-test/localhost.key",
			common.KeyIDKey:  blank,
			common.TeamIDKey: blank,
		}, psp)
		if err != nil {
			t.Errorf("A certificate provider sending blank token fields (%q) was rejected: %v\n"+
				"Whitespace is not a filled-in field, and every other check here already trims.",
				blank, err)
		}
	}
}

// TestPartiallyFilledTokenConfigurationIsStillRefused is the other side: a keyid
// an operator actually typed, with no authkey beside it, is a mistake worth
// naming rather than letting fall through to "NoCertificate".
func TestPartiallyFilledTokenConfigurationIsStillRefused(t *testing.T) {
	psp := push.NewEmptyPushServiceProvider()
	psp.FixedData["service"] = "halfway"

	err := buildCredentials(map[string]string{
		"cert":          "apns-test/localhost.cert",
		"key":           "apns-test/localhost.key",
		common.KeyIDKey: "KEYID12345",
	}, psp)
	if err == nil {
		t.Fatal("Expected a keyid with no authkey to be reported")
	}
	if !strings.Contains(err.Error(), "NoAuthKey") {
		t.Errorf("Expected the error to name the missing authkey, got: %v", err)
	}
}

// TestSendingBothCredentialsIsStillRefused pins the behaviour the clearing must
// not quietly replace. Sending cert and authkey together is a half-finished
// migration; picking one for the operator would be the wrong kind of helpful.
func TestSendingBothCredentialsIsStillRefused(t *testing.T) {
	key := newSigningKey(t)
	psp := certificateProvider(t)

	if err := buildCredentials(map[string]string{
		"cert":            "apns-test/localhost.cert",
		"key":             "apns-test/localhost.key",
		common.AuthKeyKey: key.Path,
		common.KeyIDKey:   key.KeyID,
		common.TeamIDKey:  key.TeamID,
	}, psp); err == nil {
		t.Error("Expected a provider sending both a certificate and a signing key to be refused")
	}
}
