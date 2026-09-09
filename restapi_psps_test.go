package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/uniqush/uniqush-push/push"
)

// Tests for what /psps reports about a provider.
//
// The endpoint merges a provider's fixed and volatile data into one object, and
// it used to report all of it. Both halves are also where the backends keep
// their credentials: a Web Push provider's VAPID private key lives in volatile
// data, and an ADM provider's client secret in fixed data. So an unauthenticated
// GET returned key material that keeps working long after whoever fetched it
// has lost access to the server.

// The values below are what a provider is built with, and what must not come
// back out.
const (
	secretVAPIDKey     = "SECRET-vapid-private-key"
	secretClientSecret = "SECRET-adm-client-secret"
	secretAccessToken  = "SECRET-adm-access-token"
)

// pspsDatabase is a recordingDatabase that has providers to report.
type pspsDatabase struct {
	recordingDatabase
	providers []*push.PushServiceProvider
}

func (d *pspsDatabase) GetPushServiceProviderConfigs() ([]*push.PushServiceProvider, error) {
	return d.providers, nil
}

// providerWith builds a provider directly, rather than through a push service
// type.
//
// Directly because the point is the serializer, not the builders: this test has
// to be able to put a credential in either map and see what comes back, without
// depending on which backend happens to keep its secrets where today.
func providerWith(fixed, volatile map[string]string) *push.PushServiceProvider {
	psp := push.NewEmptyPushServiceProvider()
	for key, value := range fixed {
		psp.FixedData[key] = value
	}
	for key, value := range volatile {
		psp.VolatileData[key] = value
	}
	return psp
}

// queryPSPs drives the real handler and returns the raw response alongside the
// decoded providers, keyed by service.
func queryPSPs(t *testing.T, providers ...*push.PushServiceProvider) (string, map[string][]map[string]string) {
	t.Helper()

	psm := push.GetPushServiceManager()
	database := &pspsDatabase{providers: providers}
	api := NewRestAPI(psm, silentLoggers(), "test", NewPushBackEnd(psm, database, silentLoggers()))

	recorder := httptest.NewRecorder()
	api.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, QueryPushServiceProviders, nil))

	body := recorder.Body.String()
	var decoded struct {
		Services map[string][]map[string]string `json:"services"`
		Code     string                         `json:"code"`
	}
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("Could not decode the /psps response %q: %v", body, err)
	}
	if decoded.Code != UNIQUSH_SUCCESS {
		t.Fatalf("Expected a successful response, got %q", body)
	}
	return body, decoded.Services
}

// TestPSPsDoesNotReportCredentials is the regression test.
//
// Asserted against the whole response body rather than field by field, because
// the failure being prevented is a credential appearing somewhere in the JSON,
// and a per-field check only covers the fields somebody thought of.
func TestPSPsDoesNotReportCredentials(t *testing.T) {
	webpush := providerWith(
		map[string]string{"service": "chat", "vapidpublickey": "public-half", "subscriber": "admin@example.org"},
		map[string]string{"vapidprivatekey": secretVAPIDKey},
	)
	adm := providerWith(
		map[string]string{"service": "chat", "clientid": "amzn1.application.abc", "clientsecret": secretClientSecret},
		map[string]string{"token": secretAccessToken, "type": "bearer", "expire": "1789000000"},
	)

	body, _ := queryPSPs(t, webpush, adm)
	for _, secret := range []string{secretVAPIDKey, secretClientSecret, secretAccessToken} {
		if strings.Contains(body, secret) {
			t.Errorf("/psps reported the credential %q.\n"+
				"The API has no authentication, so anything it returns is available to everyone who can "+
				"reach the port, and a key stays usable long after that access is taken away.\n"+
				"Response: %s", secret, body)
		}
	}
}

// TestPSPsStillReportsConfiguration is the other half. A redaction that hid
// everything would be secure and useless: the endpoint exists to answer whether
// a service is set up the way its operator thinks.
func TestPSPsStillReportsConfiguration(t *testing.T) {
	provider := providerWith(
		map[string]string{
			"service": "chat",
			"cert":    "/etc/uniqush/apns.crt",
			"key":     "/etc/uniqush/apns.key",
		},
		map[string]string{
			"bundleid":   "com.example.app",
			"endpoint":   "https://api.push.apple.com",
			"skipverify": "false",
		},
	)

	_, services := queryPSPs(t, provider)
	reported := services["chat"]
	if len(reported) != 1 {
		t.Fatalf("Expected one provider under the service, got %d", len(reported))
	}
	expected := map[string]string{
		"service":    "chat",
		"cert":       "/etc/uniqush/apns.crt",
		"key":        "/etc/uniqush/apns.key",
		"bundleid":   "com.example.app",
		"endpoint":   "https://api.push.apple.com",
		"skipverify": "false",
	}
	for key, want := range expected {
		if got := reported[0][key]; got != want {
			t.Errorf("Expected %s=%q, got %q. Credential file paths are configuration, "+
				"and which certificate a provider loads is most of what this endpoint is for.", key, want, got)
		}
	}
}

// TestPSPsRedactsRatherThanOmits checks an unreported field is still visible as
// a field.
//
// An operator looking for a Web Push provider's private key should be able to
// see that it is set without being handed it, and a field left out of the
// allowlist by mistake should show up as something rather than silently vanish.
func TestPSPsRedactsRatherThanOmits(t *testing.T) {
	provider := providerWith(
		map[string]string{"service": "chat"},
		map[string]string{"vapidprivatekey": secretVAPIDKey, "somethingnew": "whatever"},
	)

	_, services := queryPSPs(t, provider)
	reported := services["chat"][0]
	for _, key := range []string{"vapidprivatekey", "somethingnew"} {
		value, present := reported[key]
		if !present {
			t.Errorf("Expected %s to be reported as redacted rather than dropped", key)
			continue
		}
		if value != redactedValue {
			t.Errorf("Expected %s to be %q, got %q", key, redactedValue, value)
		}
	}
}

// TestPSPsGroupsByTheProvidersOwnService guards the grouping against the
// allowlist.
//
// The response is keyed by service name, and that name used to be read back out
// of the encoded object -- so a "service" that failed to make the allowlist
// would have filed every provider in the database under "[redacted]".
func TestPSPsGroupsByTheProvidersOwnService(t *testing.T) {
	first := providerWith(map[string]string{"service": "chat"}, nil)
	second := providerWith(map[string]string{"service": "alerts"}, nil)

	_, services := queryPSPs(t, first, second)
	if len(services["chat"]) != 1 || len(services["alerts"]) != 1 {
		t.Errorf("Expected one provider under each service, got %v", services)
	}
}
