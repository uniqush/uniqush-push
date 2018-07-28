// Package mocks implements mocks for unit testing APNs and the HTTP/2 API.
package mocks

import (
	"github.com/uniqush/uniqush-push/push"
)

// TODO: refactor into a common test library.

// MockPushServiceType contains mocks of enough functionality of a push service type for unit testing. It panics for unimplemented functionality.
type MockPushServiceType struct{}

var _ push.PushServiceType = &MockPushServiceType{}

// BuildPushServiceProviderFromMap unserializes a push service provider from kv, for this mock of the APNS push service type.
func (pst *MockPushServiceType) BuildPushServiceProviderFromMap(kv map[string]string, psp *push.PushServiceProvider) error {
	for key, value := range kv {
		switch key {
		case "addr", "bundleid", "skipverify":
			psp.VolatileData[key] = value
		case "service", "pushservicetype", "cert", "subscriber", "key":
			psp.FixedData[key] = value
		}
	}
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
