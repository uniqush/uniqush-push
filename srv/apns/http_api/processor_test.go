package http_api

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/jarcoal/httpmock"
	"github.com/uniqush/uniqush-push/push"
	"github.com/uniqush/uniqush-push/srv/apns/common"
)

const (
	authToken  = "test_auth_token"
	authToken2 = "update_auth_token"
	keyFile    = "../apns-test/localhost.p8"
	keyID      = "FD8789SD9"
	teamID     = "JVNS20943"
	bundleID   = "com.example.test"
)

var (
	pushServiceProvider = &push.PushServiceProvider{
		PushPeer: push.PushPeer{
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

type MockJWTManager struct {
	once sync.Once
}

func (jm *MockJWTManager) GenerateToken() (string, error) {
	token := authToken2
	jm.once.Do(func() {
		token = authToken
	})
	return token, nil
}

func newMockJWTManager() *MockJWTManager {
	return &MockJWTManager{}
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

func TestAddRequestPushSuccessful(t *testing.T) {
	httpmock.Activate()
	defer httpmock.DeactivateAndReset()

	request, _, resChan := newPushRequest()
	mockAPNSRequest(func(r *http.Request) (*http.Response, error) {
		if len(r.Header["authorization"]) == 0 {
			t.Error("Missing authorization header")
		}
		if len(r.Header["apns-expiration"]) == 0 {
			t.Error("Missing apns-expiration header")
		}
		if len(r.Header["apns-priority"]) == 0 {
			t.Error("Missing apns-priority header")
		}
		if len(r.Header["apns-topic"]) == 0 {
			t.Error("Missing apns-topic header")
		}
		requestBody, err := ioutil.ReadAll(r.Body)
		if err != nil {
			t.Error("Error reading request body:", err)
		}
		if bytes.Compare(requestBody, payload) != 0 {
			t.Errorf("Wrong message payload, expected `%v`, got `%v`", payload, requestBody)
		}
		// Return empty body
		return httpmock.NewBytesResponse(http.StatusOK, nil), nil
	})

	common.SetJWTManager(keyID, newMockJWTManager())
	NewRequestProcessor().AddRequest(request)

	res := <-resChan
	if res.MsgId == 0 {
		t.Fatal("Expected non-zero message id, got zero")
	}
}

func TestAddRequestRetryInvalidToken(t *testing.T) {
	httpmock.Activate()
	defer httpmock.DeactivateAndReset()

	request, _, resChan := newPushRequest()
	mockAPNSRequest(func(r *http.Request) (*http.Response, error) {
		authHeader := r.Header["authorization"][0]
		token := strings.Split(authHeader, "bearer ")[1]
		if token == authToken {
			response := &APNSErrorResponse{
				Reason: "ExpiredProviderToken",
			}
			return httpmock.NewJsonResponse(http.StatusForbidden, response)
		}
		return httpmock.NewBytesResponse(http.StatusOK, nil), nil
	})

	common.SetJWTManager(keyID, newMockJWTManager())
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
	common.SetJWTManager(keyID, newMockJWTManager())
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
	common.SetJWTManager(keyID, newMockJWTManager())
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
