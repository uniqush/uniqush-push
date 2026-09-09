package apns

import (
	"testing"

	"github.com/uniqush/uniqush-push/push"
	"github.com/uniqush/uniqush-push/srv/apns/common"
)

// buildProvider registers a provider with the given extra /addpsp parameters.
//
// Through the push service manager, the way /addpsp does, so that what is
// asserted below is what would actually be written to redis.
func buildProvider(t *testing.T, extra map[string]string) *push.PushServiceProvider {
	t.Helper()
	ensureAPNSRegistered()

	kv := map[string]string{
		"service":         "environments",
		"pushservicetype": "apns",
		"cert":            "apns-test/localhost.cert",
		"key":             "apns-test/localhost.key",
		"bundleid":        "com.example.environments",
	}
	for key, value := range extra {
		kv[key] = value
	}

	psp, err := push.GetPushServiceManager().BuildPushServiceProviderFromMap(kv)
	if err != nil {
		t.Fatalf("Could not build a provider: %v", err)
	}
	return psp
}

// TestAddPSPRecordsTheEnvironment covers what replaced the binary protocol's
// gateway address.
//
// A provider used to carry addr, a host:port nothing has connected to since
// Apple shut that protocol down in 2021, whose only remaining purpose was to
// have "sandbox" looked for in it. The environment is recorded as itself now.
func TestAddPSPRecordsTheEnvironment(t *testing.T) {
	testCases := []struct {
		name     string
		extra    map[string]string
		expected string
	}{
		{
			name:     "production by default",
			expected: common.EnvironmentProduction,
		},
		{
			name:     "sandbox=true asks for the development environment",
			extra:    map[string]string{"sandbox": "true"},
			expected: common.EnvironmentDevelopment,
		},
		{
			// Anything other than "true" is not a request for the sandbox, which
			// is how this parameter has always been read.
			name:     "sandbox=false is production",
			extra:    map[string]string{"sandbox": "false"},
			expected: common.EnvironmentProduction,
		},
		{
			// The compatibility case: a registration script written years ago
			// still sends addr, and dropping it on the floor would move that
			// service to production, where none of its device tokens are valid.
			name:     "a legacy sandbox addr still selects development",
			extra:    map[string]string{"addr": "gateway.sandbox.push.apple.com:2195"},
			expected: common.EnvironmentDevelopment,
		},
		{
			// The other half of the legacy rule, and the half easy to forget:
			// an addr may name one of Apple's api.development. hosts rather
			// than a sandbox gateway.
			name:     "a legacy api.development. addr still selects development",
			extra:    map[string]string{"addr": "api.development.push.apple.com:443"},
			expected: common.EnvironmentDevelopment,
		},
		{
			name:     "a legacy production addr still selects production",
			extra:    map[string]string{"addr": "gateway.push.apple.com:2195"},
			expected: common.EnvironmentProduction,
		},
		{
			// An unrecognised addr was a binary-protocol simulator, and
			// production is what uniqush has always assumed for one.
			name:     "an unrecognised addr is production",
			extra:    map[string]string{"addr": "127.0.0.1:2195"},
			expected: common.EnvironmentProduction,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			psp := buildProvider(t, testCase.extra)
			if got := psp.VolatileData[common.EnvironmentKey]; got != testCase.expected {
				t.Errorf("Expected environment %q, got %q", testCase.expected, got)
			}
		})
	}
}

// TestAddPSPNoLongerStoresAddr is the other half: accepted on the way in,
// never written on the way out.
//
// A provider that still carried addr would leave two answers in the database
// for one question, and the next person to read it would have to know which of
// them uniqush believes.
func TestAddPSPNoLongerStoresAddr(t *testing.T) {
	for _, extra := range []map[string]string{
		nil,
		{"addr": "gateway.sandbox.push.apple.com:2195"},
		{"sandbox": "true"},
	} {
		psp := buildProvider(t, extra)
		if stored, ok := psp.VolatileData[common.AddrKey]; ok {
			t.Errorf("/addpsp with %v stored addr=%q; nothing writes it any more", extra, stored)
		}
	}
}

// TestBuildingOverAStaleProviderClearsTheAddr covers the contract rather than
// the caller.
//
// /addpsp always hands the builder a fresh provider, so nothing on that path
// carries an addr in. But BuildPushServiceProviderFromMap is an exported
// interface method and its signature promises nothing about the provider it is
// given, and a provider holding both keys would have two answers to one
// question with only ResolveEndpoint's precedence to say which was meant. So
// the builder clears it, the way it already clears skipverify, endpoint and
// cacert.
func TestBuildingOverAStaleProviderClearsTheAddr(t *testing.T) {
	ensureAPNSRegistered()
	service := NewPushService()
	t.Cleanup(service.Finalize)

	// A provider as it would come back from a database written before uniqush
	// recorded an environment.
	psp := push.NewEmptyPushServiceProvider()
	psp.VolatileData[common.AddrKey] = "gateway.sandbox.push.apple.com:2195"

	err := service.BuildPushServiceProviderFromMap(map[string]string{
		"service":         "environments",
		"pushservicetype": "apns",
		"cert":            "apns-test/localhost.cert",
		"key":             "apns-test/localhost.key",
		"bundleid":        "com.example.environments",
	}, psp)
	if err != nil {
		t.Fatalf("Could not rebuild the provider: %v", err)
	}

	if stored, ok := psp.VolatileData[common.AddrKey]; ok {
		t.Errorf("Rebuilding left addr=%q behind, alongside environment=%q",
			stored, psp.VolatileData[common.EnvironmentKey])
	}
	// And the registration decides the environment, rather than the addr that
	// was there before it: this call did not ask for the sandbox.
	if got := psp.VolatileData[common.EnvironmentKey]; got != common.EnvironmentProduction {
		t.Errorf("Expected the registration to decide the environment, got %q", got)
	}
}

// TestTheEnvironmentIsWrittenOnEveryRegistration checks that dropping
// sandbox=true from a registration moves the service back to production.
//
// /addpsp replaces a provider wholesale rather than patching it -- bundleid and
// skipverify already work this way -- so an operator who removes a parameter
// means it to be gone. Leaving the previous environment in place would make
// moving a service out of the sandbox impossible without deleting it and
// re-subscribing every device.
func TestTheEnvironmentIsWrittenOnEveryRegistration(t *testing.T) {
	sandbox := buildProvider(t, map[string]string{"sandbox": "true"})
	if got := sandbox.VolatileData[common.EnvironmentKey]; got != common.EnvironmentDevelopment {
		t.Fatalf("Expected the development environment, got %q", got)
	}

	production := buildProvider(t, nil)
	if got := production.VolatileData[common.EnvironmentKey]; got != common.EnvironmentProduction {
		t.Errorf("Re-registering without sandbox=true left the environment at %q", got)
	}

	// And the endpoint the pushes actually go to follows it.
	if got := common.ResolveEndpoint(production); got != common.HostProduction {
		t.Errorf("Expected pushes to go to %s, got %s", common.HostProduction, got)
	}
}
