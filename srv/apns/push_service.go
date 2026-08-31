/*
 * Copyright 2011-2013 Nan Deng
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *	http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 * This implements version 2 of the Binary Provider API
 *
 * ## A note on ttl and expiry (Expiration date)
 *
 * From
 * https://developer.apple.com/library/content/documentation/NetworkingInternet/Conceptual/RemoteNotificationsPG/BinaryProviderAPI.html#//apple_ref/doc/uid/TP40008194-CH13-SW1
 *
 * > A UNIX epoch date expressed in seconds (UTC) that identifies when the notification is no longer valid and can be discarded.
 * >
 * > If this value is non-zero, APNs stores the notification tries to deliver the notification at least once.
 * > Specify zero to indicate that the notification expires immediately and that APNs should not store the notification at all.
 */

// Package apns implements sending pushes to (and receiving feedback from) APNs.
package apns

import (
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	// There are two protocols for connecting to APNs: the binary protocol,
	// which Apple shut down on 2021-03-31, and HTTP/2. HTTP/2 is the default;
	// see apnsHTTP2Key.
	"github.com/uniqush/uniqush-push/push"
	"github.com/uniqush/uniqush-push/srv/apns/binary_api"
	"github.com/uniqush/uniqush-push/srv/apns/common"
	"github.com/uniqush/uniqush-push/srv/apns/http_api"
)

const (
	maxNrConn int = 13
)

// Keys uniqush clients can set in the /push request to influence APNs behaviour.
const (
	// apnsPushTypeKey overrides the apns-push-type header. Defaults to "alert".
	apnsPushTypeKey = "uniqush.apns_push_type"
	// apnsVoIPKey predates apnsPushTypeKey. It is still honoured, and implies
	// a push type of "voip".
	apnsVoIPKey = "uniqush.apns_voip"
	// apnsHTTP2Key used to opt *in* to the HTTP/2 API. HTTP/2 is now the
	// default, so only the value "0" is meaningful: it opts back in to the
	// binary protocol.
	apnsHTTP2Key = "uniqush.http2"
)

// binaryProtocolDeprecationNotice is reported when a caller explicitly asks for
// the binary protocol. Apple stopped serving it on 2021-03-31, so this is not a
// performance or compatibility trade-off; it simply will not deliver.
const binaryProtocolDeprecationNotice = "uniqush.http2=0 selects the APNs binary protocol, which Apple shut down on 2021-03-31. " +
	"Pushes sent over it cannot be delivered. Remove uniqush.http2=0 from the request; " +
	"this fallback will be removed in a future release."

// pushTypeForNotification resolves the apns-push-type for a notification.
func pushTypeForNotification(notif *push.Notification) (string, push.Error) {
	if pushType, ok := notif.Data[apnsPushTypeKey]; ok && pushType != "" {
		if !common.IsValidPushType(pushType) {
			types := common.ValidPushTypes()
			sort.Strings(types)
			return "", push.NewBadNotificationWithDetails(
				fmt.Sprintf("invalid %s %q, expected one of: %s", apnsPushTypeKey, pushType, strings.Join(types, ", ")))
		}
		return pushType, nil
	}
	// uniqush.apns_voip predates the apns-push-type header. Keep it working.
	if isVoIP, ok := notif.Data[apnsVoIPKey]; ok && isVoIP == "1" {
		return common.PushTypeVoIP, nil
	}
	return common.DefaultPushType, nil
}

// pushService is the APNs push service. It implements the two network protocols for sending requests to APNs and getting the corresponding response.
type pushService struct {
	binaryRequestProcessor common.PushRequestProcessor
	httpRequestProcessor   common.PushRequestProcessor
	errChan                chan<- push.Error
	nextMessageID          uint32
}

var _ push.PushServiceType = &pushService{}

// NewPushService creates a new APNs push service.
func NewPushService() push.PushServiceType {
	return &pushService{
		binaryRequestProcessor: binary_api.NewRequestProcessor(maxNrConn),
		httpRequestProcessor:   http_api.NewRequestProcessor(),
		nextMessageID:          0,
	}
}

// getMessageIds is needed for the binary API of APNs.
func (ps *pushService) getMessageIds(n int) uint32 {
	return atomic.AddUint32(&ps.nextMessageID, uint32(n))
}

func (ps *pushService) Name() string {
	return "apns"
}

func (ps *pushService) Finalize() {
	ps.binaryRequestProcessor.Finalize()
	ps.httpRequestProcessor.Finalize()
}

func (ps *pushService) SetErrorReportChan(errChan chan<- push.Error) {
	ps.errChan = errChan
	ps.binaryRequestProcessor.SetErrorReportChan(errChan)
	ps.httpRequestProcessor.SetErrorReportChan(errChan)
}

// SetPushServiceConfig sets the config for this and the requestProcessor when the service is registered.
func (ps *pushService) SetPushServiceConfig(c *push.PushServiceConfig) {
	// This uses the fact that registration takes place before any requests are sent, so pools aren't created yet.

	ps.binaryRequestProcessor.SetPushServiceConfig(c)
	ps.httpRequestProcessor.SetPushServiceConfig(c)
}

func (ps *pushService) BuildPushServiceProviderFromMap(kv map[string]string, psp *push.PushServiceProvider) error {
	if service, ok := kv["service"]; ok {
		psp.FixedData["service"] = service
	} else {
		return errors.New("NoService")
	}

	return ps.buildBinaryPushServiceProviderFromMap(kv, psp)
}

func (ps *pushService) buildBinaryPushServiceProviderFromMap(kv map[string]string, psp *push.PushServiceProvider) error {
	if cert, ok := kv["cert"]; ok && len(cert) > 0 {
		psp.FixedData["cert"] = cert
	} else {
		return errors.New("NoCertificate")
	}

	if key, ok := kv["key"]; ok && len(key) > 0 {
		psp.FixedData["key"] = key
	} else {
		return errors.New("NoPrivateKey")
	}

	_, err := tls.LoadX509KeyPair(psp.FixedData["cert"], psp.FixedData["key"])
	if err != nil {
		return err
	}

	// Set or cleared on every call, never left alone. /addpsp is a
	// create-or-replace of the whole provider rather than a partial update --
	// bundleid below has always worked this way -- so a setting the operator
	// stopped sending is one they mean to be gone.
	//
	// This one matters most of the three: leaving a stale "true" here would mean
	// certificate verification could be turned off but never back on without
	// deleting the provider and re-subscribing every device.
	skipVerify := kv[common.SkipVerifyKey] == "true"
	if skipVerify {
		psp.VolatileData[common.SkipVerifyKey] = "true"
	} else {
		delete(psp.VolatileData, common.SkipVerifyKey)
	}

	// Put other things which can change in VolatileData.
	// E.g. a bundleid can be changed by the company which manages the app.
	if bundleid, ok := kv["bundleid"]; ok {
		psp.VolatileData["bundleid"] = bundleid
	} else {
		psp.VolatileData["bundleid"] = ""
	}
	if sandbox, ok := kv["sandbox"]; ok && sandbox == "true" {
		psp.VolatileData[common.AddrKey] = "gateway.sandbox.push.apple.com:2195"
	} else {
		if addr, ok := kv[common.AddrKey]; ok {
			psp.VolatileData[common.AddrKey] = addr
		} else {
			psp.VolatileData[common.AddrKey] = "gateway.push.apple.com:2195"
		}
	}

	return buildHTTP2Destination(kv, psp, skipVerify)
}

// buildHTTP2Destination records where HTTP/2 pushes go, and how to trust it.
//
// Both settings live in VolatileData rather than FixedData, and that is not a
// stylistic choice. A provider's name is a hash of its FixedData, and /addpsp
// rejects an update whose fixed data changed -- it reads that as a second,
// conflicting provider for the service. Putting the endpoint there would mean
// an operator could never move a service between the sandbox and production, or
// point it at a simulator and back again, without every device re-subscribing.
//
// Both are optional, and absent they change nothing: ResolveEndpoint falls back
// to the environment inferred from addr, exactly as before.
func buildHTTP2Destination(kv map[string]string, psp *push.PushServiceProvider, skipVerify bool) error {
	// Both are cleared when absent or empty, not merely left alone. Without
	// that, an operator who pointed a service at a simulator could never point
	// it back: the endpoint would still be in VolatileData, and the fallback to
	// the addr-inferred host that the comment above promises would be
	// unreachable. The same for a CA bundle and the system roots. bundleid has
	// always cleared this way, and /addpsp replaces a provider wholesale rather
	// than patching it.
	endpoint := strings.TrimSpace(kv[common.EndpointKey])
	if endpoint == "" {
		delete(psp.VolatileData, common.EndpointKey)
	} else {
		if err := common.ValidateEndpoint(endpoint, skipVerify); err != nil {
			return err
		}
		psp.VolatileData[common.EndpointKey] = endpoint
	}

	caCert := strings.TrimSpace(kv[common.CACertKey])
	if caCert == "" {
		delete(psp.VolatileData, common.CACertKey)
	} else {
		// Read the file now rather than on the first push. uniqush reads it in
		// its own process and possibly as another user, so an unreadable path
		// or a file that is not actually PEM is worth reporting to whoever just
		// supplied it, while they still have the context to fix it.
		if err := common.ValidateCACert(caCert); err != nil {
			return err
		}
		psp.VolatileData[common.CACertKey] = caCert
	}
	return nil
}

func (ps *pushService) BuildDeliveryPointFromMap(kv map[string]string, dp *push.DeliveryPoint) error {
	dp.AddCommonData(kv)
	if devtoken, ok := kv["devtoken"]; ok && len(devtoken) > 0 {
		_, err := hex.DecodeString(devtoken)
		if err != nil {
			return fmt.Errorf("Invalid delivery point: bad device token. %v", err)
		}
		dp.FixedData["devtoken"] = devtoken
	} else {
		return errors.New("NoDevToken")
	}
	return nil
}

func apnsresToError(apnsres *common.APNSResult, psp *push.PushServiceProvider, dp *push.DeliveryPoint) push.Error {
	// TODO: If necessary, update this to account for HTTP2 result codes?
	var err push.Error
	switch apnsres.Status {
	case common.Status0Success:
		err = nil
	case common.Status1ProcessingError:
		err = push.NewBadDeliveryPointWithDetails(dp, "Processing Error")
	case common.Status2MissingDeviceToken:
		err = push.NewBadDeliveryPointWithDetails(dp, "Missing Device Token")
	case common.Status3MissingTopic:
		err = push.NewBadNotificationWithDetails("Missing topic")
	case common.Status4MissingPayload:
		err = push.NewBadNotificationWithDetails("Missing payload")
	case common.Status5InvalidTokenSize:
		err = push.NewBadNotificationWithDetails("Invalid token size")
	case common.Status6InvalidTopicSize:
		err = push.NewBadNotificationWithDetails("Invalid topic size")
	case common.Status7InvalidPayloadSize:
		err = push.NewBadNotificationWithDetails("Invalid payload size")
	case common.Status8Unsubscribe:
		// err = push.NewBadDeliveryPointWithDetails(req.dp, "Invalid Token")
		// This token is invalid, we should unsubscribe this device.
		err = push.NewUnsubscribeUpdate(psp, dp)
	default:
		err = push.NewErrorf("Unknown Error: %d", apnsres.Status)
	}
	return err
}

// waitResults collects all the results from APNs for a push that has already been sent (and uniqush has already replied to clients)
// and sends any UnsubscribeUpdates to the pushbackend so pushbackend can update the DB.
func (ps *pushService) waitResults(psp *push.PushServiceProvider, dpList []*push.DeliveryPoint, lastID uint32, resChan <-chan *common.APNSResult) {
	k := 0
	// Wait for exactly len(dpList) results (one for each deliveryPoint, because of multiPush)
	n := len(dpList)
	if n == 0 {
		return
	}
	for res := range resChan {
		idx := res.MsgID - lastID + uint32(n)
		if idx >= uint32(len(dpList)) {
			continue
		}
		dp := dpList[idx]
		err := apnsresToError(res, psp, dp)
		if err != nil {
			// Send all errors over the channel so they can be logged.
			ps.errChan <- err
		}
		k++
		if k >= n {
			return
		}
	}
}

// Returns a JSON APNs payload, for a dummy device token
func (ps *pushService) Preview(notif *push.Notification) ([]byte, push.Error) {
	return toAPNSPayload(notif)
}

// Push will read all of the delivery points to send to from dpQueue and send responses on resQueue before closing the channel. If the notification data is invalid,
// it will send only one response.
func (ps *pushService) Push(psp *push.PushServiceProvider, dpQueue <-chan *push.DeliveryPoint, resQueue chan<- *push.Result, notif *push.Notification) {
	defer close(resQueue)
	// Profiling
	// ps.updateCheckPoint("")
	var err push.Error
	req := new(common.PushRequest)
	req.PSP = psp
	req.Payload, err = toAPNSPayload(notif)

	pushType, pushTypeErr := pushTypeForNotification(notif)
	if err == nil && pushTypeErr != nil {
		err = pushTypeErr
	}
	req.PushType = pushType

	// HTTP/2 is the default. The binary protocol is only reachable by asking
	// for it explicitly, and Apple stopped serving it in March 2021.
	requestProcessor := ps.httpRequestProcessor
	if http2, ok := notif.Data[apnsHTTP2Key]; ok && http2 == "0" {
		requestProcessor = ps.binaryRequestProcessor
		if ps.errChan != nil {
			ps.errChan <- push.NewError(binaryProtocolDeprecationNotice)
		}
	}

	maxPayloadSize := requestProcessor.GetMaxPayloadSize()
	// A VoIP push may carry 5120 bytes rather than the usual 4096, assuming the
	// PSP was set up with a VoIP certificate. https://github.com/uniqush/uniqush-push/issues/202
	// The binary protocol has a smaller ceiling of its own, so only widen for HTTP/2.
	// TODO: Automatically append ".voip" to apns-topic if it is not already the suffix
	if requestProcessor == ps.httpRequestProcessor && pushType == common.PushTypeVoIP {
		maxPayloadSize = 5120
	}

	if err == nil && len(req.Payload) > maxPayloadSize {
		err = push.NewBadNotificationWithDetails(fmt.Sprintf("payload is too large: %d > %d", len(req.Payload), maxPayloadSize))
	}

	if err != nil {
		// Drain the list of delivery points to send to, until the channel is closed. This allows the caller to proceed past the first step.
		go func() {
			for range dpQueue {
			}
		}()
		res := new(push.Result)
		res.Provider = psp
		res.Content = notif
		res.Err = push.NewErrorf("Failed to create push: %v", err)
		resQueue <- res
		return
	}

	// By default, the notification expires in an hour if the ttl is omitted.
	// Uniqush users can send a ttl of 0 to send a notification that expires immediately.
	// Uniqush users can alternately choose a positive ttl in seconds, which will be converted to a timestamp.
	unixNow := uint32(time.Now().Unix())
	expiry := unixNow + 60*60
	if ttlstr, ok := notif.Data["ttl"]; ok {
		ttl, err := strconv.ParseUint(ttlstr, 10, 32)
		if err == nil {
			if ttl > 0 {
				// Expiry is the exact date and time when the notification
				// expires. It's not a "time to live".
				expiry = unixNow + uint32(ttl)
			} else {
				expiry = uint32(0)
			}
		}
	}

	// Process the list of delivery points, sending error responses for invalid delivery points
	// Keep the remaining valid delivery points
	req.Expiry = expiry
	req.Devtokens = make([][]byte, 0, 10)
	dpList := make([]*push.DeliveryPoint, 0, 10)

	for dp := range dpQueue {
		res := new(push.Result)
		res.Destination = dp
		res.Provider = psp
		res.Content = notif
		devtoken, ok := dp.FixedData["devtoken"]
		if !ok {
			res.Err = push.NewBadDeliveryPointWithDetails(dp, "NoDevtoken")
			resQueue <- res
			continue
		}
		btoken, err := hex.DecodeString(devtoken)
		if err != nil {
			res.Err = push.NewBadDeliveryPointWithDetails(dp, err.Error())
			resQueue <- res
			continue
		}

		req.Devtokens = append(req.Devtokens, btoken)
		dpList = append(dpList, dp)
	}

	n := len(req.Devtokens)
	lastID := ps.getMessageIds(n)
	req.MaxMsgID = lastID
	req.DPList = dpList

	// We send this request object to be processed by pushMux goroutine, to send responses/errors back.
	// If there are no errors, then there will be the same number of results as Devtokens.
	// TODO: NIT: Channels could be created by AddRequest.
	errChan := make(chan push.Error)
	resChan := make(chan *common.APNSResult, n)
	req.ErrChan = errChan
	req.ResChan = resChan

	requestProcessor.AddRequest(req)

	// errChan closed means the message(s) is/are sent successfully to the APNs.
	// However, we may have not yet received responses from APNs - those are sent on resChan
	for err = range errChan {
		res := new(push.Result)
		res.Provider = psp
		res.Content = notif
		if _, ok := err.(*push.ErrorReport); ok {
			res.Err = push.NewErrorf("Failed to send payload to APNS: %v", err)
		} else {
			res.Err = err
		}
		resQueue <- res
	}
	// Profiling
	// ps.updateCheckPoint("sending the message takes")
	if err != nil {
		// TODO: in another pr, send responses where some requests succeed, but other requests fail. (E.g. mix of unsubscribe responses on resChan and connection errors)
		return
	}

	for i, dp := range dpList {
		if dp != nil {
			r := new(push.Result)
			r.Provider = psp
			r.Content = notif
			r.Destination = dp
			mid := req.GetID(i)
			r.MsgID = fmt.Sprintf("apns:%v-%v", psp.Name(), mid)
			r.Err = nil
			resQueue <- r
		}
	}

	// Wait for the unserialized responses from APNs asynchronously - these will not affect what we send our clients for this request, but will affect subsequent requests.
	// TODO: With HTTP/2, this can be refactored to become synchronous (not in this PR, not while binary provider is supported for a PSP). The map[string]T can be removed.
	go ps.waitResults(psp, dpList, lastID, resChan)
}
