package binary_api

import (
	"net"
	"sync"
	"testing"

	cache "github.com/uniqush/cache2"
	"github.com/uniqush/uniqush-push/push"
	"github.com/uniqush/uniqush-push/srv/apns/binary_api/mocks"
	"github.com/uniqush/uniqush-push/srv/apns/common"
)

const APNSSuccess uint8 = 0
const APNSUnsubscribe uint8 = 8

// MockConnectionCount is the number of workers to use for this test. Each worker has a single TCP socket for the binary protocol.
const MockConnectionCount = 2

// MockAPNSPushServiceType replaces apnsPushService. It is registered so that tested functions using the global push registry work properly.
type MockAPNSPushServiceType struct{}

var _ push.PushServiceType = &MockAPNSPushServiceType{}

func (pst *MockAPNSPushServiceType) BuildPushServiceProviderFromMap(kv map[string]string, psp *push.PushServiceProvider) error {
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
func (pst *MockAPNSPushServiceType) BuildDeliveryPointFromMap(map[string]string, *push.DeliveryPoint) error {
	panic("Not implemented")
}
func (pst *MockAPNSPushServiceType) Name() string {
	return "apns"
}
func (pst *MockAPNSPushServiceType) Push(*push.PushServiceProvider, <-chan *push.DeliveryPoint, chan<- *push.Result, *push.Notification) {
	panic("Not implemented")
}
func (pst *MockAPNSPushServiceType) Preview(*push.Notification) ([]byte, push.Error) {
	panic("Not implemented")
}
func (pst *MockAPNSPushServiceType) SetErrorReportChan(errChan chan<- push.Error) {
	panic("Not implemented")
}
func (pst *MockAPNSPushServiceType) SetPushServiceConfig(*push.PushServiceConfig) {
	panic("Not implemented")
}

func (pst *MockAPNSPushServiceType) Finalize() {}

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

func mockEmptyFeedbackChecker(psp *push.PushServiceProvider, dpCache *cache.SimpleCache, errChan chan<- push.Error) {
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
func commonBinaryMocks(requestProcessor *BinaryPushRequestProcessor, status uint8, serviceName string) (*push.PushServiceProvider, *MockConnManager, chan push.Error) {
	mockAPNSConnectionManager := newMockConnManager(status)
	requestProcessor.overrideAPNSConnManagerMaker(mockConnManagerMaker(mockAPNSConnectionManager))
	requestProcessor.overrideFeedbackChecker(mockEmptyFeedbackChecker)
	errChan := make(chan push.Error, 100)
	requestProcessor.SetErrorReportChan(errChan)

	psp := mockPSPData(serviceName)
	return psp, mockAPNSConnectionManager, errChan
}

// NewConn creates a mock connection which responds with a hardcoded status result for each push.
func (cm *MockConnManager) NewConn() (net.Conn, <-chan bool, error) {
	mockedConn := mocks.NewMockNetConn()
	go mocks.SimulateStableAPNSServer(mockedConn, cm.status)
	didClose := make(chan bool, 1)
	go resultCollector(cm.psp, cm.resChan, mockedConn, didClose)
	cm.mutex.Lock()
	defer cm.mutex.Unlock()
	cm.mockedConns = append(cm.mockedConns, mockedConn)
	return mockedConn, didClose, nil
}

func (cm *MockConnManager) CleanUp() {
	cm.mutex.Lock()
	defer cm.mutex.Unlock()
	for _, mockedConn := range cm.mockedConns {
		mockedConn.CleanUp()
	}
	cm.mockedConns = nil
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

func createSinglePushRequest(psp *push.PushServiceProvider) (*common.PushRequest, chan push.Error, chan *common.APNSResult) {
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
	errChan := make(chan push.Error)
	resChan := make(chan *common.APNSResult, len(devtokens))

	req := &common.PushRequest{
		PSP:       psp,
		Devtokens: devtokens,
		Payload:   []byte("{}"),
		MaxMsgID:  7,
		Expiry:    0,
		DPList:    []*push.DeliveryPoint{dp},
		ErrChan:   errChan,
		ResChan:   resChan,
	}
	return req, errChan, resChan
}

// TestPushSuccess checks that the user of this RequestProcessor is forwarded the correct status code for successes.
func TestPushSuccess(t *testing.T) {
	testPushForwardsErrorCode(t, APNSSuccess)
}

// TestPushUnsubscribe checks that the user of this RequestProcessor is forwarded the correct status code for unsubscribe operations..
func TestPushUnsubscribe(t *testing.T) {
	testPushForwardsErrorCode(t, APNSUnsubscribe)
}

func testPushForwardsErrorCode(t *testing.T, status uint8) {
	requestProcessor := NewRequestProcessor(MockConnectionCount)
	defer requestProcessor.Finalize()
	serviceName := "mockservice.apns"
	psp, _, serviceErrChan := commonBinaryMocks(requestProcessor, status, serviceName)
	req, errChan, resChan := createSinglePushRequest(psp)

	requestProcessor.AddRequest(req)
	verifyRequestProcessorRespondsWithStatus(t, status, req, errChan, resChan)
	verifyNewConnectionLogged(t, serviceErrChan)
}

func verifyRequestProcessorRespondsWithStatus(t *testing.T, status uint8, req *common.PushRequest, errChan chan push.Error, resChan chan *common.APNSResult) {

	for err := range errChan {
		t.Fatalf("Expected 0 errors for successful push, got %#v", err)
	}

	res := <-resChan
	close(resChan)
	expectedMsgID := req.MaxMsgID - 1
	if expectedMsgID != res.MsgID {
		t.Errorf("Expected only msgid to be %d, got %d", expectedMsgID, res.MsgID)
	}
	if res.Status != status {
		t.Errorf("Expected status code to be %d, got %d", status, res.Status)
	}
}

func verifyNewConnectionLogged(t *testing.T, serviceErrChan chan push.Error) {
	err := <-serviceErrChan
	expectedErrMsg := "Connection to APNS opened: <nil> to <nil>"
	if expectedErrMsg != err.Error() {
		t.Errorf("Expected loggingConnManager to log %q, but the first log was %#v", expectedErrMsg, err)
	}
}

func TestRequestAfterShutdown(t *testing.T) {
	requestProcessor := NewRequestProcessor(MockConnectionCount)
	serviceName := "mockservice.apns"
	psp, _, serviceErrChan := commonBinaryMocks(requestProcessor, APNSSuccess, serviceName)
	req, errChan, resChan := createSinglePushRequest(psp)
	requestProcessor.AddRequest(req)

	verifyRequestProcessorRespondsWithStatus(t, APNSSuccess, req, errChan, resChan)
	verifyNewConnectionLogged(t, serviceErrChan)

	req, errChan, _ = createSinglePushRequest(psp)

	requestProcessor.Finalize()
	requestProcessor.AddRequest(req)
	err := <-errChan
	expectedErrMsg := "Uniqush is shutting down"
	if expectedErrMsg != err.Error() {
		t.Errorf("Expected to log %q, but the first error was %q (%#v)", expectedErrMsg, err.Error(), err)
	}
	for err := range errChan {
		t.Errorf("Expected just 1 error for successful push, got %#v", err)
	}
	if len(serviceErrChan) > 0 {
		err := <-errChan
		t.Errorf("Expected no additional logs for service, got %#v", err)
	}
}

func TestParallelRequestAndShutdown(t *testing.T) {
	requestProcessor := NewRequestProcessor(MockConnectionCount)
	serviceName := "mockservice.apns"
	psp, _, serviceErrChan := commonBinaryMocks(requestProcessor, APNSSuccess, serviceName)
	req, errChan, resChan := createSinglePushRequest(psp)
	doneFinalize := make(chan bool)

	go func() {
		requestProcessor.Finalize()
		doneFinalize <- true
	}()
	go func() {
		requestProcessor.AddRequest(req)
	}()
	<-doneFinalize
	// Should either return a result or report that uniqush is shutting down.
	// This test should not fail, and should not report a race condition when run with `go test -race`
	errCount := 0
	expectedErrMsg := "Uniqush is shutting down"
	for err := range errChan {
		errCount++
		if expectedErrMsg != err.Error() {
			t.Errorf("Expected to log %q, but the first error was %q (%#v)", expectedErrMsg, err.Error(), err)
		}
	}
	if errCount > 1 {
		t.Errorf("Expected 0 or 1 errors, got %d errors", errCount)
	}

	if errCount == 0 {
		verifyRequestProcessorRespondsWithStatus(t, APNSSuccess, req, errChan, resChan)
		verifyNewConnectionLogged(t, serviceErrChan)
	} else {
		if len(resChan) != 0 {
			res := <-resChan
			t.Errorf("There was an error, didn't expect to have a result, but got %#v", res)
		}
	}
}
