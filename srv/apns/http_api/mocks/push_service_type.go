package mocks

import (
	"github.com/uniqush/uniqush-push/push"
)

// TODO: refactor into a common test library.

// MockPushServiceType contains mocks of enough functionality of a push service type for unit testing. It panics for unimplemented functionality.
type MockPushServiceType struct{}

var _ push.PushServiceType = &MockPushServiceType{}

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
func (pst *MockPushServiceType) BuildDeliveryPointFromMap(map[string]string, *push.DeliveryPoint) error {
	panic("Not implemented")
}
func (pst *MockPushServiceType) Name() string {
	return "apns"
}
func (pst *MockPushServiceType) Push(*push.PushServiceProvider, <-chan *push.DeliveryPoint, chan<- *push.PushResult, *push.Notification) {
	panic("Not implemented")
}
func (pst *MockPushServiceType) Preview(*push.Notification) ([]byte, push.PushError) {
	panic("Not implemented")
}
func (pst *MockPushServiceType) SetErrorReportChan(errChan chan<- push.PushError) {
	panic("Not implemented")
}
func (pst *MockPushServiceType) SetPushServiceConfig(*push.PushServiceConfig) {
	panic("Not implemented")
}
func (pst *MockPushServiceType) Finalize() {}
