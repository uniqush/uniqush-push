// Package http_api implements a client for the new APNs HTTP/2 API (over an encrypted HTTP/2 connection to APNs)
package http_api // nolint: golint

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

// HTTPClient is a mockable interface for the parts of http.Client used by the APNs HTTP2 module.
// The underlying implementation contains a connection pool, to tolerate spurious network errors.
type HTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

// ClientFactory is an abstraction to create HTTPClient instances (one for each push service provider). The value is overridden for testing.
type ClientFactory func(*http.Transport) HTTPClient

// HTTPPushRequestProcessor sends push notification requests to APNs using HTTP API
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

// AddRequest will asynchronously process the request to send a push notification to APNs over HTTP/2
func (prp *HTTPPushRequestProcessor) AddRequest(request *common.PushRequest) {
	go prp.sendRequests(request)
}

// GetMaxPayloadSize will return the max JSON payload size for HTTP/2 pushes (which is larger than the binary API)
func (prp *HTTPPushRequestProcessor) GetMaxPayloadSize() int {
	return 4096
}

// GetClient will return the only HTTP client instance for the given psp. That instance uses the credentials and endpoint associated with the given psp.
func (prp *HTTPPushRequestProcessor) GetClient(psp *push.PushServiceProvider) (HTTPClient, error) {
	pspName := psp.Name()
	if client := prp.TryGetClient(pspName); client != nil {
		return client, nil
	}
	tlsClientConfig, err := createTLSConfig(psp)
	if err != nil {
		return nil, fmt.Errorf("GetClient failed, couldn't create TLS config: %v", err)
	}
	prp.clientsLock.Lock()
	defer prp.clientsLock.Unlock()
	if client, ok := prp.clients[pspName]; ok {
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
		// Because TLSClientConfig is provided, we have to manually configure this client for http2 support.
		TLSClientConfig: tlsClientConfig,
	}

	err = http2.ConfigureTransport(transport)
	if err != nil {
		return nil, fmt.Errorf("GetClient failed, couldn't configure for http2: %v", err)
	}

	client := prp.clientFactory(transport)
	prp.clients[pspName] = client
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

// TryGetClient will
func (prp *HTTPPushRequestProcessor) TryGetClient(pspName string) HTTPClient {
	prp.clientsLock.RLock()
	defer prp.clientsLock.RUnlock()
	if client, exists := prp.clients[pspName]; exists {
		return client
	}
	return nil
}

// Finalize will shut down all of the connections owned by HTTP/2 clients for each PSP.
func (prp *HTTPPushRequestProcessor) Finalize() {
	prp.clientsLock.Lock()
	for _, client := range prp.clients {
		if httpClient, isClient := client.(*http.Client); isClient {
			switch transport := httpClient.Transport.(type) {
			case *http.Transport:
				transport.CloseIdleConnections()
			default:
			}
		}
	}
}

// SetErrorReportChan will set the report chan used for asynchronous feedback that is not associated with a request. (not needed when using APNs's HTTP/2 API, but needed for the binary API)
func (prp *HTTPPushRequestProcessor) SetErrorReportChan(errChan chan<- push.Error) {}

// SetPushServiceConfig is called during initialization to provide the unserialized contents of uniqush.conf. (does nothing for cloud messaging)
func (prp *HTTPPushRequestProcessor) SetPushServiceConfig(c *push.PushServiceConfig) {}

// sendRequests will send a push to one or more device tokens. It will send the response over ResChan or ErrChan.
func (prp *HTTPPushRequestProcessor) sendRequests(request *common.PushRequest) {
	defer close(request.ErrChan)

	bundleid, ok := request.PSP.VolatileData["bundleid"]
	if !ok || bundleid == "" {
		for range request.Devtokens {
			request.ErrChan <- push.NewError("Must add bundleid to PSP by calling /addpsp again")
		}
		return
	}

	wg := new(sync.WaitGroup)
	wg.Add(len(request.Devtokens))

	header := http.Header{
		"apns-expiration": []string{fmt.Sprint(request.Expiry)},
		"apns-priority":   []string{"10"}, // Send notification immediately
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
	client, err := prp.GetClient(psp)
	if err != nil {
		for range request.Devtokens {
			request.ErrChan <- push.NewErrorf("Could not create a client: %v", err)
		}
		return
	}

	for i, token := range request.Devtokens {
		msgID := request.GetID(i)

		url := fmt.Sprintf("%s/3/device/%s", http2UrlHost, hex.EncodeToString(token))
		httpRequest, err := http.NewRequest("POST", url, bytes.NewReader(request.Payload))
		if err != nil {
			request.ErrChan <- push.NewError(err.Error())
			continue
		}
		httpRequest.Header = header

		go prp.sendRequest(wg, client, httpRequest, msgID, request.ErrChan, request.ResChan)
	}

	wg.Wait()
}

func (prp *HTTPPushRequestProcessor) sendRequest(wg *sync.WaitGroup, client HTTPClient, request *http.Request, messageID uint32, errChan chan<- push.Error, resChan chan<- *common.APNSResult) {
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
			MsgID:  messageID,
			Status: common.Status8Unsubscribe,
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

	prp.handlePushResponseBody(response, responseBody, messageID, errChan, resChan)
}

// handle the response body of an HTTP/2 push attempt to APNs.
func (prp *HTTPPushRequestProcessor) handlePushResponseBody(response *http.Response, responseBody []byte, messageID uint32, errChan chan<- push.Error, resChan chan<- *common.APNSResult) {
	if len(responseBody) > 0 {
		// Successful request should return empty response body
		apnsError := new(APNSErrorResponse)
		err := json.Unmarshal(responseBody, apnsError)
		switch apnsError.Reason {
		case "BadDeviceToken": // Status code is 400
			// > The specified device token was bad. If this error is seen, then clients of uniqush should verify that the request contains a valid token and that the token matches the environment (sandbox/prod).
			resChan <- &common.APNSResult{
				MsgID:  messageID,
				Status: common.Status8Unsubscribe,
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
				MsgID:  messageID,
				Status: common.Status0Success,
			}
		} else {
			errChan <- push.NewErrorf("Unknown error. No response body, HTTP status code is %d", response.StatusCode)
		}
	}
}
