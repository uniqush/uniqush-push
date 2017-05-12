package http_api

import (
	"encoding/hex"
	"fmt"
	"net/http"
	"testing"

	"github.com/jarcoal/httpmock"
	"github.com/uniqush/uniqush-push/push"
	"github.com/uniqush/uniqush-push/srv/apns/common"
)

const (
	authToken = "test_auth_token"
	keyFile   = "../apns-test/localhost.p8"
	keyID     = "FD8789SD9"
	teamID    = "JVNS20943"
	bundleID  = "com.example.test"
)

var (
	pushServiceProvider = &push.PushServiceProvider{
		push.PushPeer{
			VolatileData: map[string]string{
				"addr": "https://api.development.push.apple.com",
			},
			FixedData: map[string]string{
				"p8":       keyFile,
				"keyid":    keyID,
				"teamid":   teamID,
				"bundleid": bundleID,
			},
		},
	}
	devToken = []byte("test_device_token")
	payload  = []byte(`{"alert":"test_message"}`)
	apiURL   = fmt.Sprintf("%s/3/device/%s", pushServiceProvider.VolatileData["addr"], hex.EncodeToString(devToken))
)

type MockJWTManager struct{}

func (*MockJWTManager) GenerateToken() (string, error) {
	return authToken, nil
}

func mockAPNSRequest(fn func(r *http.Request) (*http.Response, error)) {
	httpmock.RegisterResponder("POST", apiURL, fn)
}

func newPushRequest() (*common.PushRequest, chan push.PushError, chan *common.APNSResult) {
	errChan := make(chan push.PushError)
	resChan := make(chan *common.APNSResult, 1)
	request := &common.PushRequest{
		PSP:       pushServiceProvider,
		Devtokens: [][]byte{devToken},
		Payload:   payload,
		ErrChan:   errChan,
		ResChan:   resChan,
	}

	return request, errChan, resChan
}

var mockJWTManager = &MockJWTManager{}

func TestAddRequestPushSuccessful(t *testing.T) {
	httpmock.Activate()
	defer httpmock.DeactivateAndReset()

	request, _, resChan := newPushRequest()
	mockAPNSRequest(func(r *http.Request) (*http.Response, error) {
		// Return empty body
		return httpmock.NewBytesResponse(http.StatusOK, nil), nil
	})

	common.SetJWTManagerSingleton(mockJWTManager)
	NewRequestProcessor().AddRequest(request)

	res := <-resChan
	if res.MsgId == 0 {
		t.Fatal("Expected non-zero message id, got zero")
	}
}

func TestAddRequestPushFailConnectionError(t *testing.T) {
	httpmock.Activate()
	defer httpmock.DeactivateAndReset()

	request, errChan, _ := newPushRequest()
	common.SetJWTManagerSingleton(mockJWTManager)
	mockAPNSRequest(func(r *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("No connection")
	})

	NewRequestProcessor().AddRequest(request)

	err := <-errChan
	if _, ok := err.(*push.ConnectionError); !ok {
		t.Fatal("Expected Connection error, got", err)
	}
}

func TestAddRequestPushFailNotificationError(t *testing.T) {
	httpmock.Activate()
	defer httpmock.DeactivateAndReset()

	request, errChan, _ := newPushRequest()
	common.SetJWTManagerSingleton(mockJWTManager)
	mockAPNSRequest(func(r *http.Request) (*http.Response, error) {
		response := &APNSErrorResponse{
			Reason: "BadDeviceToken",
		}
		return httpmock.NewJsonResponse(http.StatusBadRequest, response)
	})

	NewRequestProcessor().AddRequest(request)

	err := <-errChan
	if _, ok := err.(*push.BadNotification); !ok {
		t.Fatal("Expected BadNotification error, got", err)
	}
}

func TestGetMaxPayloadSize(t *testing.T) {
	maxPayloadSize := NewRequestProcessor().GetMaxPayloadSize()
	if maxPayloadSize != 4096 {
		t.Fatalf("Wrong max payload, expected `4096`, got `%d`", maxPayloadSize)
	}
}
