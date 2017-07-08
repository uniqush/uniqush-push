package http_api

import (
	"bytes"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/http2"

	"github.com/uniqush/uniqush-push/push"
	"github.com/uniqush/uniqush-push/srv/apns/common"
)

// HTTPClient is a mockable interface for the parts of http.Client used by the APNS HTTP2 module.
type HTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

type ClientFactory func(*http.Transport) HTTPClient

// HTTPPushRequestProcessor sends push notification requests to APNS using HTTP API
type HTTPPushRequestProcessor struct {
	clients       map[string]HTTPClient
	clientsLock   sync.RWMutex
	clientFactory ClientFactory // can be overridden by test
}

// NewRequestProcessor returns a new HTTPPushProcessor using net/http DefaultClient connection pool
func NewRequestProcessor() common.PushRequestProcessor {
	return &HTTPPushRequestProcessor{
		clients:       make(map[string]HTTPClient),
		clientFactory: defaultClientFactory,
	}
}

func (self *HTTPPushRequestProcessor) AddRequest(request *common.PushRequest) {
	go self.sendRequests(request)
}

func (self *HTTPPushRequestProcessor) GetMaxPayloadSize() int {
	return 4096
}

func (self *HTTPPushRequestProcessor) GetClient(psp *push.PushServiceProvider) (HTTPClient, error) {
	pspName := psp.Name()
	if client := self.TryGetClient(pspName); client != nil {
		return client, nil
	}
	tlsClientConfig, err := createTLSConfig(psp)
	if err != nil {
		return nil, fmt.Errorf("GetClient failed, couldn't create TLS config: %v", err)
	}
	self.clientsLock.Lock()
	defer self.clientsLock.Unlock()
	if client, ok := self.clients[pspName]; ok {
		// Maybe something else locked before this goroutine did.
		return client, nil
	}
	transport := &http.Transport{
		// The same as GCM.
		// TODO: Make the maximum number of idle connections configurable.
		// Note: It's likely that fewer idle clients should be needed than GCM, since HTTP2 allows multiple in-flight requests
		// Note: Do not set IdleTimeout, it may be a cause of errors in setups where pushes are infrequent.
		MaxIdleConns:          20,
		MaxIdleConnsPerHost:   20,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		// Because TLSClientConfig is provided, have to manually configure this client for http2 support.
		TLSClientConfig: tlsClientConfig,
	}

	// Requires Go 1.6 or later
	err = http2.ConfigureTransport(transport)
	if err != nil {
		return nil, fmt.Errorf("GetClient failed, couldn't configure for http2: %v", err)
	}

	client := self.clientFactory(transport)
	self.clients[pspName] = client
	return client, nil
}

func defaultClientFactory(transport *http.Transport) HTTPClient {
	return &http.Client{
		Transport: transport,
		Timeout:   20 * time.Second,
	}
}

func createTLSConfig(psp *push.PushServiceProvider) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(psp.FixedData["cert"], psp.FixedData["key"])
	if err != nil {
		return nil, push.NewBadPushServiceProviderWithDetails(psp, err.Error())
	}

	conf := &tls.Config{
		Certificates:       []tls.Certificate{cert},
		InsecureSkipVerify: false,
	}
	return conf, nil
}

func (self *HTTPPushRequestProcessor) TryGetClient(pspName string) HTTPClient {
	self.clientsLock.RLock()
	defer self.clientsLock.RUnlock()
	if client, exists := self.clients[pspName]; exists {
		return client
	}
	return nil
}

func (self *HTTPPushRequestProcessor) Finalize() {
	self.clientsLock.Lock()
	for _, client := range self.clients {
		if httpClient, isClient := client.(*http.Client); isClient {
			switch transport := httpClient.Transport.(type) {
			case *http.Transport:
				transport.CloseIdleConnections()
			default:
			}
		}
	}
}

func (self *HTTPPushRequestProcessor) SetErrorReportChan(errChan chan<- push.PushError) {}

func (self *HTTPPushRequestProcessor) sendRequests(request *common.PushRequest) {
	defer close(request.ErrChan)

	bundleid, ok := request.PSP.VolatileData["bundleid"]
	if !ok || bundleid == "" {
		for _ = range request.Devtokens {
			request.ErrChan <- push.NewError("Must add bundleid to PSP by calling /addpsp again")
		}
		return
	}

	wg := new(sync.WaitGroup)
	wg.Add(len(request.Devtokens))

	header := http.Header{
		"apns-expiration": []string{fmt.Sprint(request.Expiry)},
		"apns-priority":   []string{"10"}, // Send notification immidiately
		// This is kept in VolatileData. A PSP may need to be updated first in /addpsp to use this,
		// by setting bundleid to the bundle id of the app.
		"apns-topic": []string{bundleid},
	}

	// TODO: Allow specifying http2 addr without string matching heuristics.
	psp := request.PSP
	binaryProtocolAddress := psp.VolatileData["addr"]
	var http2UrlHost string
	if strings.Contains(binaryProtocolAddress, "sandbox") || strings.Contains(binaryProtocolAddress, "api.development.") {
		http2UrlHost = "https://api.development.push.apple.com"
	} else {
		http2UrlHost = "https://api.push.apple.com"
	}
	client, err := self.GetClient(psp)
	if err != nil {
		for _ = range request.Devtokens {
			request.ErrChan <- push.NewErrorf("Could not create a client: %v", err)
		}
		return
	}

	for i, token := range request.Devtokens {
		msgID := request.GetId(i)

		url := fmt.Sprintf("%s/3/device/%s", http2UrlHost, hex.EncodeToString(token))
		httpRequest, err := http.NewRequest("POST", url, bytes.NewReader(request.Payload))
		if err != nil {
			request.ErrChan <- push.NewError(err.Error())
			continue
		}
		httpRequest.Header = header

		go self.sendRequest(wg, client, httpRequest, msgID, request.ErrChan, request.ResChan)
	}

	wg.Wait()
}

func (self *HTTPPushRequestProcessor) sendRequest(wg *sync.WaitGroup, client HTTPClient, request *http.Request, messageID uint32, errChan chan<- push.PushError, resChan chan<- *common.APNSResult) {
	defer wg.Done()

	response, err := client.Do(request)
	if err != nil {
		errChan <- push.NewConnectionError(err)
		return
	}

	defer response.Body.Close()

	responseBody, err := ioutil.ReadAll(response.Body)
	if err != nil {
		errChan <- push.NewError(err.Error())
		return
	}
	// https://developer.apple.com/library/content/documentation/NetworkingInternet/Conceptual/RemoteNotificationsPG/CommunicatingwithAPNs.html
	switch response.StatusCode {
	case 200:
		break // success, body must be empty
	case 410:
		// Reason is "Unregistered"
		// > The device token is inactive for the specified topic.
		resChan <- &common.APNSResult{
			MsgId:  messageID,
			Status: common.STATUS8_UNSUBSCRIBE,
		}
		return
	case 400:
		break // Switch on Reason
	case 405, 413, 429, 500, 503:
		// Not sure how to handle this, these error types shouldn't happen in the normal case.
		// Use generic error handler to report the status message to the client.
		break
	default:
		break
	}

	if len(responseBody) > 0 {
		// Successful request should return empty response body
		apnsError := new(APNSErrorResponse)
		err := json.Unmarshal(responseBody, apnsError)
		switch apnsError.Reason {
		case "BadDeviceToken": // Status code is 400
			// > The specified device token was bad. Verify that the request contains a valid token and that the token matches the environment.
			resChan <- &common.APNSResult{
				MsgId:  messageID,
				Status: common.STATUS8_UNSUBSCRIBE,
			}
			return
		default:
			break
		}
		if err != nil {
			errChan <- push.NewError(err.Error())
		} else {
			errChan <- push.NewBadNotificationWithDetails(apnsError.Reason)
		}
	} else {
		// It must be 200 to be successful.
		if response.StatusCode == 200 {
			resChan <- &common.APNSResult{
				MsgId:  messageID,
				Status: common.STATUS0_SUCCESS,
			}
		} else {
			errChan <- push.NewErrorf("Unknown error. No response body, HTTP status code is %d", response.StatusCode)
		}
	}
}
