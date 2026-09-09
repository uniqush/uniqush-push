// Package mocks implements mocks for unit testing APNs and the HTTP/2 API.
package mocks

import (
	"github.com/uniqush/uniqush-push/push"
	"github.com/uniqush/uniqush-push/srv/apns/common"
)

// TODO: refactor into a common test library.

// MockPushServiceType contains mocks of enough functionality of a push service type for unit testing. It panics for unimplemented functionality.
type MockPushServiceType struct{}

var _ push.PushServiceType = &MockPushServiceType{}

// BuildPushServiceProviderFromMap unserializes a push service provider from kv, for this mock of the APNs push service type.
func (pst *MockPushServiceType) BuildPushServiceProviderFromMap(kv map[string]string, psp *push.PushServiceProvider) error {
	for key, value := range kv {
		switch key {
		// Everything that can change without changing the provider's identity.
		// A name hashes FixedData, so anything an operator may need to update in
		// place -- the environment a service pushes to, where it pushes, how
		// that destination is trusted -- has to stay out of it.
		//
		// addr is not among them. It is accepted below and translated into an
		// environment, exactly as /addpsp does, and stored no more than /addpsp
		// stores it.
		//
		// Named by the constants rather than by literals, so a rename in
		// srv/apns/common reaches this mock too. A mock that silently disagreed
		// with the provider it stands in for would make the tests using it
		// prove the wrong thing.
		case common.EnvironmentKey, common.SkipVerifyKey, common.EndpointKey, common.CACertKey, "bundleid":
			psp.VolatileData[key] = value
		case "service", "pushservicetype", "cert", "subscriber", "key":
			psp.FixedData[key] = value
		}
	}

	// Which of Apple's environments the provider pushes to, resolved the way the
	// real builder resolves it, and -- like the real builder -- without storing
	// the addr it may have come from. A mock that stored an addr would let tests
	// pass against provider data /addpsp would never write, and ResolveEndpoint
	// reads the two by different rules.
	//
	// After the loop rather than inside it: kv may carry both an addr and an
	// explicit environment, and deciding between them on map iteration order
	// would make the provider a mock builds depend on which key came out first.
	if _, explicit := psp.VolatileData[common.EnvironmentKey]; !explicit {
		environment := common.EnvironmentProduction
		if kv["sandbox"] == "true" {
			environment = common.EnvironmentDevelopment
		} else if addr, ok := kv[common.AddrKey]; ok {
			environment = common.EnvironmentFromAddr(addr)
		}
		psp.VolatileData[common.EnvironmentKey] = environment
	}

	// The real builder records this once it has validated the credential files,
	// and the push path relies on it to notice a credential rotated in place
	// without reopening anything. A mock that skipped it would leave every
	// provider it builds keying on an empty revision, so the tests that check
	// rotation is noticed would pass or fail for reasons of their own.
	psp.VolatileData[common.CredentialRevisionKey] = common.CredentialRevision(
		psp.FixedData["cert"], psp.FixedData["key"], psp.VolatileData[common.CACertKey])

	return nil
}

// BuildDeliveryPointFromMap panics due to not being used in any tests.
func (pst *MockPushServiceType) BuildDeliveryPointFromMap(map[string]string, *push.DeliveryPoint) error {
	panic("Not implemented")
}

// Name returns the name of the push service.
func (pst *MockPushServiceType) Name() string {
	return "apns"
}

// Push will panic (not used by tests using this mock).
func (pst *MockPushServiceType) Push(*push.PushServiceProvider, <-chan *push.DeliveryPoint, chan<- *push.Result, *push.Notification) {
	panic("Not implemented")
}

// Preview will panic (not used by tests using this mock).
func (pst *MockPushServiceType) Preview(*push.Notification) ([]byte, push.Error) {
	panic("Not implemented")
}

// SetErrorReportChan will panic (not used by tests using this mock).
func (pst *MockPushServiceType) SetErrorReportChan(errChan chan<- push.Error) {
	panic("Not implemented")
}

// SetPushServiceConfig will panic (not used by tests using this mock).
func (pst *MockPushServiceType) SetPushServiceConfig(*push.PushServiceConfig) {
	panic("Not implemented")
}

// Finalize will do nothing in this mock.
func (pst *MockPushServiceType) Finalize() {}
