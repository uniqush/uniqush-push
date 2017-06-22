package http_api

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"sync"

	"github.com/uniqush/uniqush-push/push"
	"github.com/uniqush/uniqush-push/srv/apns/common"
)

// HTTPPushRequestProcessor sends push notification requests to APNS using HTTP API
type HTTPPushRequestProcessor struct {
	client *http.Client
}

// NewRequestProcessor returns a new HTTPPushProcessor using net/http DefaultClient connection pool
func NewRequestProcessor() common.PushRequestProcessor {
	return &HTTPPushRequestProcessor{
		client: http.DefaultClient,
	}
}

func (processor *HTTPPushRequestProcessor) AddRequest(request *common.PushRequest) {
	go processor.sendRequests(request)
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

func (processor *HTTPPushRequestProcessor) sendRequests(request *common.PushRequest) {
	defer close(request.ErrChan)

	jwtManager, err := common.GetJWTManager(request.PSP.FixedData["p8"], request.PSP.FixedData["keyid"], request.PSP.FixedData["teamid"])
	if err != nil {
		request.ErrChan <- push.NewError(err.Error())
		return
	}
	jwt, err := jwtManager.GenerateToken()
	if err != nil {
		request.ErrChan <- push.NewError(err.Error())
		return
	}

	wg := new(sync.WaitGroup)
	wg.Add(len(request.Devtokens))

	header := http.Header{
		"authorization":   []string{"bearer " + jwt},
		"apns-expiration": []string{fmt.Sprint(request.Expiry)},
		"apns-priority":   []string{"10"}, // Send notification immidiately
		"apns-topic":      []string{request.PSP.FixedData["bundleid"]},
	}

	for i, token := range request.Devtokens {
		msgID := request.GetId(i)

		url := fmt.Sprintf("%s/3/device/%s", request.PSP.VolatileData["addr"], hex.EncodeToString(token))
		httpRequest, err := http.NewRequest("POST", url, bytes.NewReader(request.Payload))
		if err != nil {
			request.ErrChan <- push.NewError(err.Error())
			continue
		}
		httpRequest.Header = header

		go processor.sendRequest(wg, httpRequest, jwtManager, msgID, request.ErrChan, request.ResChan)
	}

	wg.Wait()
}

func (processor *HTTPPushRequestProcessor) sendRequest(wg *sync.WaitGroup, request *http.Request, jwtManager common.JWTManager, messageID uint32, errChan chan<- push.PushError, resChan chan<- *common.APNSResult) {
	defer wg.Done()

	response, err := processor.client.Do(request)
	if err != nil {
		errChan <- push.NewConnectionError(err)
		return
	}

	if response.StatusCode == http.StatusForbidden {
		response.Body.Close()
		// Token might have expired, retry with new token
		jwt, err := jwtManager.GenerateToken()
		if err != nil {
			errChan <- push.NewError(err.Error())
			return
		}
		request.Header["authorization"] = []string{"bearer " + jwt}
		response, err = processor.client.Do(request)
		if err != nil {
			errChan <- push.NewConnectionError(err)
			return
		}
	}

	defer response.Body.Close()

	responseBody, err := ioutil.ReadAll(response.Body)
	if err != nil {
		errChan <- push.NewError(err.Error())
		return
	}

	if len(responseBody) > 0 {
		// Successful request should return empty response body
		apnsError := new(APNSErrorResponse)
		err := json.Unmarshal(responseBody, apnsError)
		if err != nil {
			errChan <- push.NewError(err.Error())
		} else {
			errChan <- push.NewBadNotificationWithDetails(apnsError.Reason)
		}
	} else {
		resChan <- &common.APNSResult{
			MsgId: messageID,
		}
	}
}
