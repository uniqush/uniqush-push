package binary_api

import (
	"net"
	"sync"
	"testing"

	"github.com/uniqush/cache"
	"github.com/uniqush/uniqush-push/push"
	"github.com/uniqush/uniqush-push/srv/apns/binary_api/mocks"
	"github.com/uniqush/uniqush-push/srv/apns/common"
)

const APNS_SUCCESS uint8 = 0
const APNS_UNSUBSCRIBE uint8 = 8

const MOCK_NR_CONN = 2

// MockAPNSPushServiceType replaces apnsPushService. It is registered so that tested functions using the global push registry work properly.
type MockAPNSPushServiceType struct{}

var _ push.PushServiceType = &MockAPNSPushServiceType{}

func (self *MockAPNSPushServiceType) BuildPushServiceProviderFromMap(kv map[string]string, psp *push.PushServiceProvider) error {
	for key, value := range kv {
		switch key {
		case "addr":
			psp.VolatileData[key] = value
		case "service", "pushservicetype", "cert", "subscriber", "key":
			psp.FixedData[key] = value
		}
	}
	return nil
}
func (self *MockAPNSPushServiceType) BuildDeliveryPointFromMap(map[string]string, *push.DeliveryPoint) error {
	panic("Not implemented")
}
func (self *MockAPNSPushServiceType) Name() string {
	return "apns"
}
func (self *MockAPNSPushServiceType) Push(*push.PushServiceProvider, <-chan *push.DeliveryPoint, chan<- *push.PushResult, *push.Notification) {
	panic("Not implemented")
}
func (self *MockAPNSPushServiceType) Preview(*push.Notification) ([]byte, push.PushError) {
	panic("Not implemented")
}
func (self *MockAPNSPushServiceType) SetErrorReportChan(errChan chan<- push.PushError) {
	panic("Not implemented")
}
func (self *MockAPNSPushServiceType) Finalize() {}

type MockConnManager struct {
	mockedConns []*mocks.MockNetConn
	psp         *push.PushServiceProvider
	resChan     chan<- *common.APNSResult
	mutex       sync.Mutex
	status      uint8
}

var _ ConnManager = &MockConnManager{}

func newMockConnManager(status uint8) *MockConnManager {
	return &MockConnManager{
		status: status,
	}
}

func mockEmptyFeedbackChecker(psp *push.PushServiceProvider, dpCache *cache.Cache, errChan chan<- push.PushError) {
}

func mockPSPData(serviceName string) *push.PushServiceProvider {
	psm := push.GetPushServiceManager()
	psm.RegisterPushServiceType(&MockAPNSPushServiceType{})
	psp, err := psm.BuildPushServiceProviderFromMap(map[string]string{
		"service":         serviceName,
		"pushservicetype": "apns",
		"cert":            "../apns-test/localhost.cert",
		"key":             "../apns-test/localhost.key",
		"addr":            "gateway.push.apple.com:2195",
	})
	if err != nil {
		panic(err)
	}
	return psp
}

// commonBinaryMocks mocks the PSP, connection manager and a feedback checker, etc, returning the mocks. This is needed for virtually every test.
func commonBinaryMocks(requestProcessor *BinaryPushRequestProcessor, status uint8, serviceName string) (*push.PushServiceProvider, *MockConnManager, chan push.PushError) {
	mockAPNSConnectionManager := newMockConnManager(status)
	requestProcessor.overrideAPNSConnManagerMaker(mockConnManagerMaker(mockAPNSConnectionManager))
	requestProcessor.overrideFeedbackChecker(mockEmptyFeedbackChecker)
	errChan := make(chan push.PushError, 100)
	requestProcessor.SetErrorReportChan(errChan)

	psp := mockPSPData(serviceName)
	return psp, mockAPNSConnectionManager, errChan
}

// NewConn creates a mock connection which responds with a hardcoded status result for each push.
func (self *MockConnManager) NewConn() (net.Conn, <-chan bool, error) {
	mockedConn := mocks.NewMockNetConn()
	go mocks.SimulateStableAPNSServer(mockedConn, self.status)
	didClose := make(chan bool, 1)
	go resultCollector(self.psp, self.resChan, mockedConn, didClose)
	self.mutex.Lock()
	defer self.mutex.Unlock()
	self.mockedConns = append(self.mockedConns, mockedConn)
	return mockedConn, didClose, nil
}

func (self *MockConnManager) CleanUp() {
	self.mutex.Lock()
	defer self.mutex.Unlock()
	for _, mockedConn := range self.mockedConns {
		mockedConn.CleanUp()
	}
	self.mockedConns = nil
}

func mockConnManagerMaker(connManager *MockConnManager) func(psp *push.PushServiceProvider, resChan chan<- *common.APNSResult) ConnManager {
	return func(psp *push.PushServiceProvider, resChan chan<- *common.APNSResult) ConnManager {
		if connManager.psp != nil {
			panic("Already created this mock connection manager?")
		}
		connManager.psp = psp
		connManager.resChan = resChan
		return connManager
	}
}

// TestPoolSize verifies that poolSize is initialized properly.
func TestPoolSize(t *testing.T) {
	nrConn := 4
	requestProcessor := NewRequestProcessor(nrConn)
	if requestProcessor.poolSize != nrConn {
		t.Errorf("Want apns default pool size to be %d, got %d", nrConn, requestProcessor.poolSize)
	}
	requestProcessor.Finalize()
}

func createSinglePushRequest(psp *push.PushServiceProvider) (*common.PushRequest, chan push.PushError, chan *common.APNSResult) {
	devtoken := "01234567890abcdef01234567890abcdef01234567890abcdef01234567890abcdef"
	dp := push.NewEmptyDeliveryPoint()
	service, ok := psp.FixedData["service"]
	if !ok {
		panic("Bad psp mock, no service")
	}
	dp.FixedData["service"] = service
	dp.FixedData["subscriber"] = "SUB1234"
	dp.FixedData["devtoken"] = devtoken

	devtokens := [][]byte{[]byte(devtoken)}
	errChan := make(chan push.PushError)
	resChan := make(chan *common.APNSResult, len(devtokens))

	req := &common.PushRequest{
		PSP:       psp,
		Devtokens: devtokens,
		Payload:   []byte("{}"),
		MaxMsgId:  7,
		Expiry:    0,
		DPList:    []*push.DeliveryPoint{dp},
		ErrChan:   errChan,
		ResChan:   resChan,
	}
	return req, errChan, resChan
}

// TestPushSuccess checks that the user of this RequestProcessor is forwarded the correct status code for successes.
func TestPushSuccess(t *testing.T) {
	testPushForwardsErrorCode(t, APNS_SUCCESS)
}

// TestPushSuccess checks that the user of this RequestProcessor is forwarded the correct status code for unsubscribe operations..
func TestPushUnsubscribe(t *testing.T) {
	testPushForwardsErrorCode(t, APNS_UNSUBSCRIBE)
}

func testPushForwardsErrorCode(t *testing.T, status uint8) {
	requestProcessor := NewRequestProcessor(MOCK_NR_CONN)
	defer requestProcessor.Finalize()
	serviceName := "mockservice.apns"
	psp, _, serviceErrChan := commonBinaryMocks(requestProcessor, status, serviceName)
	req, errChan, resChan := createSinglePushRequest(psp)

	requestProcessor.AddRequest(req)

	for err := range errChan {
		t.Fatalf("Expected 0 errors for successful push, got %#v", err)
	}

	res := <-resChan
	close(resChan)
	expectedMsgId := req.MaxMsgId - 1
	if expectedMsgId != res.MsgId {
		t.Errorf("Expected only msgid to be %d, got %d", expectedMsgId, res.MsgId)
	}
	if res.Status != status {
		t.Errorf("Expected status code to be %d, got %d", status, res.Status)
	}
	err := <-serviceErrChan
	expectedErrMsg := "Connection to APNS opened: <nil> to <nil>"
	if expectedErrMsg != err.Error() {
		t.Errorf("Expected loggingConnManager to log %q, but the first log was %#v", expectedErrMsg, err)
	}
}
