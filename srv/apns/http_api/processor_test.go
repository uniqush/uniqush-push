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

func TestAddRequestPushSuccessful(t *testing.T) {
	httpmock.Activate()
	defer httpmock.DeactivateAndReset()

	errChan := make(chan push.PushError)
	resChan := make(chan *common.APNSResult, 1)

	request := &common.PushRequest{
		PSP:       pushServiceProvider,
		Devtokens: [][]byte{devToken},
		Payload:   payload,
		ErrChan:   errChan,
		ResChan:   resChan,
	}

	httpmock.RegisterResponder("POST", apiURL, func(r *http.Request) (*http.Response, error) {
		// Return empty body
		return httpmock.NewBytesResponse(http.StatusOK, nil), nil
	})

	NewRequestProcessor().AddRequest(request)

	for err := range errChan {
		if err != nil {
			t.Fatal("Error processing push request,", err)
		}
	}
}

func TestAddRequestPushFailConnectionError(t *testing.T) {
	httpmock.Activate()
	defer httpmock.DeactivateAndReset()

	errChan := make(chan push.PushError)
	resChan := make(chan *common.APNSResult, 1)

	request := &common.PushRequest{
		PSP:       pushServiceProvider,
		Devtokens: [][]byte{devToken},
		Payload:   payload,
		ErrChan:   errChan,
		ResChan:   resChan,
	}

	httpmock.RegisterResponder("POST", apiURL, func(r *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("No connection")
	})

	NewRequestProcessor().AddRequest(request)

	for err := range errChan {
		switch err := err.(type) {
		case *push.ConnectionError:
		default:
			t.Fatal("Expected Connection error, got", err)
		}
	}
}

func TestAddRequestPushFailNotificationError(t *testing.T) {
	httpmock.Activate()
	defer httpmock.DeactivateAndReset()

	errChan := make(chan push.PushError)
	resChan := make(chan *common.APNSResult, 1)

	request := &common.PushRequest{
		PSP:       pushServiceProvider,
		Devtokens: [][]byte{devToken},
		Payload:   payload,
		ErrChan:   errChan,
		ResChan:   resChan,
	}

	httpmock.RegisterResponder("POST", apiURL, func(r *http.Request) (*http.Response, error) {
		response := &APNSErrorResponse{
			Reason: "BadDeviceToken",
		}
		return httpmock.NewJsonResponse(http.StatusBadRequest, response)
	})

	NewRequestProcessor().AddRequest(request)

	for err := range errChan {
		switch err := err.(type) {
		case *push.BadNotification:
		default:
			t.Fatal("Expected BadNotification error, got", err)
		}
	}
}

func TestGetMaxPayloadSize(t *testing.T) {
	maxPayloadSize := NewRequestProcessor().GetMaxPayloadSize()
	if maxPayloadSize != 4096 {
		t.Fatalf("Wrong max payload, expected `4096`, got `%d`", maxPayloadSize)
	}
}
