package mocks

import (
	"testing"

	"github.com/uniqush/uniqush-push/push"
	"github.com/uniqush/uniqush-push/srv/apns/common"
)

// TestTheMockStoresTheProviderShapeAddPSPWouldWrite keeps this mock honest
// about the one field the real builder translates.
//
// The mock exists so that tests needing an APNs provider do not need APNs. That
// is only worth anything while the provider it builds has the shape /addpsp
// would have written: a mock storing an addr would let tests pass against data
// production never produces, and ResolveEndpoint reads addr and environment by
// different rules.
func TestTheMockStoresTheProviderShapeAddPSPWouldWrite(t *testing.T) {
	testCases := []struct {
		name     string
		kv       map[string]string
		expected string
	}{
		{name: "no environment given", kv: map[string]string{}, expected: common.EnvironmentProduction},
		{name: "a sandbox addr", kv: map[string]string{common.AddrKey: "gateway.sandbox.push.apple.com:2195"}, expected: common.EnvironmentDevelopment},
		{name: "a production addr", kv: map[string]string{common.AddrKey: "gateway.push.apple.com:2195"}, expected: common.EnvironmentProduction},
		{name: "sandbox=true", kv: map[string]string{"sandbox": "true"}, expected: common.EnvironmentDevelopment},
		{name: "an explicit environment", kv: map[string]string{common.EnvironmentKey: common.EnvironmentDevelopment}, expected: common.EnvironmentDevelopment},
		{
			// Both keys present. Resolving inside the loop over kv would let map
			// iteration order pick the winner, so the provider a mock built
			// would differ between runs.
			name:     "an explicit environment beside a disagreeing addr",
			kv:       map[string]string{common.EnvironmentKey: common.EnvironmentDevelopment, common.AddrKey: "gateway.push.apple.com:2195"},
			expected: common.EnvironmentDevelopment,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			kv := map[string]string{"service": "mock", "pushservicetype": "apns"}
			for key, value := range testCase.kv {
				kv[key] = value
			}
			psp := push.NewEmptyPushServiceProvider()
			if err := (&MockPushServiceType{}).BuildPushServiceProviderFromMap(kv, psp); err != nil {
				t.Fatalf("Could not build a provider: %v", err)
			}
			if got := psp.VolatileData[common.EnvironmentKey]; got != testCase.expected {
				t.Errorf("Expected environment %q, got %q", testCase.expected, got)
			}
			if stored, ok := psp.VolatileData[common.AddrKey]; ok {
				t.Errorf("The mock stored addr=%q, which /addpsp would not have written", stored)
			}
		})
	}
}
