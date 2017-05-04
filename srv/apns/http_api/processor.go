package http_api

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"

	"github.com/uniqush/uniqush-push/push"
	"github.com/uniqush/uniqush-push/srv/apns/common"
)

// HTTPPushRequestProcessor connects to APNS using HTTP
// Reference: https://developer.apple.com/library/content/documentation/NetworkingInternet/Conceptual/RemoteNotificationsPG/CommunicatingwithAPNs.html
type HTTPPushRequestProcessor struct {
	client *http.Client
}

// NewHTTPPushProcessor returns a new HTTPPushProcessor using net/http DefaultClient connection pool
func NewHTTPPushProcessor() common.PushRequestProcessor {
	return &HTTPPushRequestProcessor{
		client: http.DefaultClient,
	}
}

func (processor *HTTPPushRequestProcessor) AddRequest(request *common.PushRequest) {
	jwtManager, err := common.NewJWTManager(request.PSP.FixedData["p8"], request.PSP.FixedData["keyid"], request.PSP.FixedData["teamid"])
	if err != nil {
		request.ErrChan <- push.NewError(err.Error())
		return
	}
	jwt, err := jwtManager.GenerateToken()
	if err != nil {
		request.ErrChan <- push.NewError(err.Error())
		return
	}
	header := http.Header{
		"authorization":   []string{"bearer " + jwt},
		"apns-expiration": []string{"0"},  // Only attempt to send the notification once
		"apns-priority":   []string{"10"}, // Send notification immidiately
	}

	for _, token := range request.Devtokens {
		url := fmt.Sprintf("%s/3/device/%s", request.PSP.FixedData["addr"], hex.EncodeToString(token))
		httpRequest, err := http.NewRequest("POST", url, bytes.NewReader(request.Payload))
		if err != nil {
			request.ErrChan <- push.NewError(err.Error())
			continue
		}
		httpRequest.Header = header

		go processor.sendRequest(httpRequest, request.ErrChan, request.ResChan)
	}
}

func (processor *HTTPPushRequestProcessor) GetMaxPayloadSize() int {
	return 4096
}

func (processor *HTTPPushRequestProcessor) Finalize() {
	switch transport := processor.client.Transport.(type) {
	case *http.Transport:
		transport.CloseIdleConnections()
	default:
	}
}

func (processor *HTTPPushRequestProcessor) SetErrorReportChan(errChan chan<- push.PushError) {}

func (processor *HTTPPushRequestProcessor) sendRequest(request *http.Request, errChan chan<- push.PushError, resChan chan<- *common.APNSResult) {
	response, err := processor.client.Do(request)
	if err != nil {
		errChan <- push.NewConnectionError(err)
		return
	}
	defer response.Body.Close()

	responseBody, err := ioutil.ReadAll(response.Body)
	if err != nil {
		errChan <- push.NewError(err.Error())
	}
	if len(responseBody) > 0 {
		apnsError := new(APNSErrorResponse)
		err := json.Unmarshal(responseBody, apnsError)
		if err != nil {
			errChan <- push.NewError(err.Error())
			resChan <- &common.APNSResult{
				StatusCode: response.StatusCode,
				Err:        push.NewBadNotificationWithDetails(string(responseBody)),
			}
		} else {
			resChan <- &common.APNSResult{
				StatusCode: response.StatusCode,
				Err:        push.NewBadNotificationWithDetails(apnsError.Reason),
			}
		}
	} else {
		resChan <- &common.APNSResult{
			APNSID:     response.Header.Get("apns-id"),
			StatusCode: response.StatusCode,
		}
	}
}
