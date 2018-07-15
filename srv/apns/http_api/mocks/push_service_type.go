package mocks

import (
	"github.com/uniqush/uniqush-push/push"
)

// TODO: refactor into a common test library.
type MockPushServiceType struct{}

var _ push.PushServiceType = &MockPushServiceType{}

func (self *MockPushServiceType) BuildPushServiceProviderFromMap(kv map[string]string, psp *push.PushServiceProvider) error {
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
func (self *MockPushServiceType) BuildDeliveryPointFromMap(map[string]string, *push.DeliveryPoint) error {
	panic("Not implemented")
}
func (self *MockPushServiceType) Name() string {
	return "apns"
}
func (self *MockPushServiceType) Push(*push.PushServiceProvider, <-chan *push.DeliveryPoint, chan<- *push.PushResult, *push.Notification) {
	panic("Not implemented")
}
func (self *MockPushServiceType) Preview(*push.Notification) ([]byte, push.PushError) {
	panic("Not implemented")
}
func (self *MockPushServiceType) SetErrorReportChan(errChan chan<- push.PushError) {
	panic("Not implemented")
}
func (self *MockPushServiceType) SetPushServiceConfig(*push.PushServiceConfig) {
	panic("Not implemented")
}
func (self *MockPushServiceType) Finalize() {}
