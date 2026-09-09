/*
 * Copyright 2011 Nan Deng
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 */

package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/uniqush/uniqush-push/db"
	"github.com/uniqush/uniqush-push/log"
	"github.com/uniqush/uniqush-push/push"
)

// RestAPI implements uniqush's REST API (/push, /subscribe, /addpsp, etc).
type RestAPI struct {
	psm       *push.PushServiceManager
	loggers   []log.Logger
	backend   *PushBackEnd
	version   string
	waitGroup *sync.WaitGroup
	stopChan  chan<- bool
}

func randomUniqID() string {
	var d [16]byte
	io.ReadFull(rand.Reader, d[:])
	return fmt.Sprintf("%x-%v", time.Now().Unix(), base64.URLEncoding.EncodeToString(d[:]))
}

// NewRestAPI constructs the data structures for the singleton REST API of uniqush-push
func NewRestAPI(psm *push.PushServiceManager, loggers []log.Logger, version string, backend *PushBackEnd) *RestAPI {
	ret := new(RestAPI)
	ret.psm = psm
	ret.loggers = loggers
	ret.version = version
	ret.backend = backend
	ret.waitGroup = new(sync.WaitGroup)
	return ret
}

// Constants for the paths of the REST API
const (
	AddPushServiceProviderToServiceURL      = "/addpsp"
	RemovePushServiceProviderFromServiceURL = "/rmpsp"
	AddDeliveryPointToServiceURL            = "/subscribe"
	RemoveDeliveryPointFromServiceURL       = "/unsubscribe"
	PushNotificationURL                     = "/push"
	PreviewPushNotificationURL              = "/previewpush"
	StopProgramURL                          = "/stop"
	VersionInfoURL                          = "/version"
	QueryNumberOfDeliveryPointsURL          = "/nrdp"
	QuerySubscriptionsURL                   = "/subscriptions"
	QueryPushServiceProviders               = "/psps"
	RebuildServiceSetURL                    = "/rebuildserviceset"
	// CheckDatabaseURL scans the database and reports inconsistencies. It is
	// read-only, repairs nothing, walks the keyspace with SCAN rather than KEYS
	// and takes no lock, so it is safe to run against production.
	CheckDatabaseURL = "/checkdb"
)

// maxLoggedProblems bounds the per-finding lines one /checkdb writes to the log.
//
// Enough to see the shape of the problem without reading the response body, and
// few enough that running the check on a badly inconsistent database cannot cost
// more in log ingestion than the check was worth.
const maxLoggedProblems = 20

// TODO: Switch to the stricter regex in a subsequent release.
// uniqush.org didn't really document the accepted characters, so it's possible some clients used invalid characters.
// Don't allow backticks.
// TODO: Log an error if they're used.
// var validServicePattern *regexp.Regexp = regexp.MustCompile(`^[a-zA-Z.0-9_@-]+$`)
// var validSubscriberPattern *regexp.Regexp = regexp.MustCompile(`^[a-zA-Z.0-9_@-]+$`)

var validServicePattern = regexp.MustCompile(`^[a-zA-Z.0-9_@\[\]^\\\\-]+$`)
var validSubscriberPattern = regexp.MustCompile(`^[a-zA-Z.0-9_@-\[\]^\\\\-]+$`)

func validateSubscribers(subs []string) error {
	for _, sub := range subs {
		if !validSubscriberPattern.MatchString(sub) {
			return fmt.Errorf("invalid subscriber name: %q. Accepted characters: a-z, A-Z, 0-9, -, _, @ or .", sub) //nolint:revive,staticcheck
		}
	}
	return nil
}

func validateService(service string) error {
	if !validServicePattern.MatchString(service) {
		return fmt.Errorf("invalid service name: %q. Accepted characters: a-z, A-Z, 0-9, -, _, @ or .", service) //nolint:revive,staticcheck
	}
	return nil
}

func getSubscribersFromMap(kv map[string]string, validate bool) (subs []string, err error) {
	var v string
	var ok bool
	if v, ok = kv["subscriber"]; !ok {
		if v, ok = kv["subscribers"]; !ok {
			err = fmt.Errorf("NoSubscriber")
			return
		}
	}
	s := strings.Split(v, ",")
	subs = make([]string, 0, len(s))
	for _, sub := range s {
		if len(sub) > 0 {
			subs = append(subs, sub)
		}
	}
	if validate {
		err = validateSubscribers(subs)
		if err != nil {
			subs = nil
			return
		}
	}
	return
}

// Get the optional delivery_point_ids from a map.
func getDeliveryPointIdsFromMap(kv map[string]string) (deliveryPointNames []string, err error) {
	var v string
	var ok bool
	if v, ok = kv["delivery_point_id"]; !ok {
		return nil, nil
	}
	if len(v) == 0 {
		return nil, fmt.Errorf("EmptyDeliveryPoints")
	}
	s := strings.Split(v, ",")
	deliveryPointNames = make([]string, 0, len(s))
	for _, dpName := range s {
		if len(dpName) > 0 {
			// TODO validate.
			deliveryPointNames = append(deliveryPointNames, dpName)
		}
	}
	if len(deliveryPointNames) == 0 {
		return nil, fmt.Errorf("EmptyDeliveryPoints")
	}
	return
}

func getServiceFromMap(kv map[string]string) (service string, err error) {
	var ok bool
	if service, ok = kv["service"]; !ok {
		err = fmt.Errorf("NoService")
		return
	}
	err = validateService(service)
	if err != nil {
		service = ""
		return
	}
	return
}

func (api *RestAPI) changePushServiceProvider(kv map[string]string, logger log.Logger, remoteAddr string, add bool) APIResponseDetails {
	// replace=true is an explicit acknowledgement that an existing provider of
	// this type is being superseded. Off by default: the conflict it bypasses is
	// also what catches a certificate path pasted into the wrong service.
	//
	// Taken out of kv before the provider is built. No push service type reads
	// an unknown key today, so leaving it there would be harmless -- but it is
	// an instruction to uniqush, not a property of the provider, and the two
	// should not be in the same bag.
	replace := kv["replace"] == "true"
	delete(kv, "replace")

	psp, err := api.psm.BuildPushServiceProviderFromMap(kv)
	if err != nil {
		logger.Errorf("From=%v Cannot build push service provider: %v", remoteAddr, err)
		return APIResponseDetails{From: &remoteAddr, Code: UNIQUSH_ERROR_BUILD_PUSH_SERVICE_PROVIDER, ErrorMsg: strPtrOfErr(err)}
	}
	service, err := getServiceFromMap(kv)
	if err != nil {
		logger.Errorf("From=%v Cannot get service name: %v; %v", remoteAddr, service, err)
		return APIResponseDetails{From: &remoteAddr, Service: &service, Code: UNIQUSH_ERROR_CANNOT_GET_SERVICE, ErrorMsg: strPtrOfErr(err)}
	}
	if add {
		err = api.backend.AddPushServiceProvider(service, psp, replace)
	} else {
		err = api.backend.RemovePushServiceProvider(service, psp)
	}
	if err != nil {
		logger.Errorf("From=%v Failed: %v", remoteAddr, err)
		return APIResponseDetails{From: &remoteAddr, Code: UNIQUSH_ERROR_GENERIC, ErrorMsg: strPtrOfErr(err)}
	}
	pspName := psp.Name()
	logger.Infof("From=%v Service=%v PushServiceProvider=%v Success!", remoteAddr, service, pspName)
	return APIResponseDetails{From: &remoteAddr, Service: &service, PushServiceProvider: &pspName, Code: UNIQUSH_SUCCESS}
}

// allDevicesKey asks /unsubscribe to remove every device a subscriber has in a
// service, rather than the one named by the request.
const allDevicesKey = "alldevices"

// unsubscribeAllDevices removes every device a subscriber has in a service.
//
// The case it exists for is an account being deleted: the application knows the
// subscriber is finished, and does not know or care which devices they had. The
// alternative was /subscriptions followed by an /unsubscribe per device, which
// is several round trips, requires reconstructing each device's token, and
// races anything that subscribes in between.
//
// Removing nothing is success. A subscriber with no devices already satisfies
// what the caller asked for, and an account-deletion path that had to treat
// "already gone" as an error would have to special-case it everywhere.
func (api *RestAPI) unsubscribeAllDevices(kv map[string]string, logger log.Logger, remoteAddr string) APIResponseDetails {
	service, err := getServiceFromMap(kv)
	if err != nil {
		logger.Errorf("From=%v Cannot get service name: %v; %v", remoteAddr, service, err)
		return APIResponseDetails{From: &remoteAddr, Service: &service, Code: UNIQUSH_ERROR_CANNOT_GET_SERVICE, ErrorMsg: strPtrOfErr(err)}
	}
	// Validated, as every other subscription operation validates it. This one
	// deletes in bulk from a name, so a wildcard reaching the database here
	// would empty every subscriber it matched.
	subs, err := getSubscribersFromMap(kv, true)
	if err != nil {
		logger.Errorf("From=%v Service=%v Cannot get subscriber: %v", remoteAddr, service, err)
		return APIResponseDetails{From: &remoteAddr, Service: &service, Code: UNIQUSH_ERROR_CANNOT_GET_SUBSCRIBER, ErrorMsg: strPtrOfErr(err)}
	}
	// "subscriber=" and "subscriber=,,," are neither an error nor a subscriber:
	// the parameter is present, so it is not NoSubscriber, and nothing survives
	// dropping the empty entries. Every other caller is shielded from that by
	// accident -- changeSubscription builds a delivery point first, and that
	// fails for a subscriber it cannot read -- and this path exists to skip
	// that build. /push checks the same way a few lines below.
	if len(subs) == 0 {
		logger.Errorf("From=%v Service=%v NoSubscriber", remoteAddr, service)
		return APIResponseDetails{From: &remoteAddr, Service: &service, Code: UNIQUSH_ERROR_NO_SUBSCRIBER}
	}

	removed, err := api.backend.UnsubscribeAll(service, subs[0])
	if err != nil {
		// Reported with the count, because the removal is not a transaction:
		// telling the caller how far it got is what makes a retry an informed
		// decision rather than a guess.
		logger.Errorf("From=%v Service=%v Subscriber=%v Removed=%v Failed: %v", remoteAddr, service, subs[0], removed, err)
		return APIResponseDetails{From: &remoteAddr, Service: &service, Subscriber: &subs[0],
			DevicesRemoved: &removed, Code: UNIQUSH_ERROR_GENERIC, ErrorMsg: strPtrOfErr(err)}
	}

	logger.Infof("From=%v Service=%v Subscriber=%v Removed=%v Success!", remoteAddr, service, subs[0], removed)
	return APIResponseDetails{From: &remoteAddr, Service: &service, Subscriber: &subs[0],
		DevicesRemoved: &removed, Code: UNIQUSH_SUCCESS}
}

func (api *RestAPI) changeSubscription(kv map[string]string, logger log.Logger, remoteAddr string, issub bool) APIResponseDetails {
	// Before the delivery point is built, because this is the one subscription
	// request that does not name a device and has nothing to build one from.
	if !issub && kv[allDevicesKey] == "1" {
		return api.unsubscribeAllDevices(kv, logger, remoteAddr)
	}
	dp, err := api.psm.BuildDeliveryPointFromMap(kv)
	if err != nil {
		logger.Errorf("Cannot build delivery point: %v", err)
		return APIResponseDetails{From: &remoteAddr, Code: UNIQUSH_ERROR_BUILD_DELIVERY_POINT, ErrorMsg: strPtrOfErr(err)}
	}
	service, err := getServiceFromMap(kv)
	if err != nil {
		logger.Errorf("From=%v Cannot get service name: %v; %v", remoteAddr, service, err)
		return APIResponseDetails{From: &remoteAddr, Service: &service, Code: UNIQUSH_ERROR_CANNOT_GET_SERVICE, ErrorMsg: strPtrOfErr(err)}
	}
	subs, err := getSubscribersFromMap(kv, true)
	if err != nil {
		logger.Errorf("From=%v Service=%v Cannot get subscriber: %v", remoteAddr, service, err)
		return APIResponseDetails{From: &remoteAddr, Service: &service, Code: UNIQUSH_ERROR_CANNOT_GET_SUBSCRIBER, ErrorMsg: strPtrOfErr(err)}
	}
	// The same guard the bulk path above has, and for the same reason. This one
	// is narrower than it looks: "subscriber=" is caught earlier, because
	// building the delivery point needs a subscriber and fails without one. A
	// value of "," or ",,," is not -- it is a perfectly good subscriber name as
	// far as that build is concerned, and splits into nothing here.
	if len(subs) == 0 {
		logger.Errorf("From=%v Service=%v NoSubscriber", remoteAddr, service)
		return APIResponseDetails{From: &remoteAddr, Service: &service, Code: UNIQUSH_ERROR_NO_SUBSCRIBER}
	}

	var psp *push.PushServiceProvider
	if issub {
		psp, err = api.backend.Subscribe(service, subs[0], dp)
	} else {
		err = api.backend.Unsubscribe(service, subs[0], dp)
	}
	if err != nil {
		logger.Errorf("From=%v Failed: %v", remoteAddr, err)
		return APIResponseDetails{From: &remoteAddr, Code: UNIQUSH_ERROR_GENERIC, ErrorMsg: strPtrOfErr(err)}
	}
	dpName := dp.Name()
	if psp == nil {
		logger.Infof("From=%v Service=%v Subscriber=%v DeliveryPoint=%v Success!", remoteAddr, service, subs[0], dpName)
		return APIResponseDetails{From: &remoteAddr, Service: &service, Subscriber: &subs[0], DeliveryPoint: &dpName, Code: UNIQUSH_SUCCESS}
	}
	pspName := psp.Name()
	logger.Infof("From=%v Service=%v Subscriber=%v PushServiceProvider=%v DeliveryPoint=%v Success!", remoteAddr, service, subs[0], pspName, dpName)
	return APIResponseDetails{From: &remoteAddr, Service: &service, Subscriber: &subs[0], DeliveryPoint: &dpName, PushServiceProvider: &pspName, Code: UNIQUSH_SUCCESS}
}

func (api *RestAPI) buildNotificationFromKV(reqID string, kv map[string]string, logger log.Logger, remoteAddr string, service string, subs []string) (notif *push.Notification, details *APIResponseDetails, err error) {
	notif = push.NewEmptyNotification()

	for k, v := range kv {
		if len(v) == 0 {
			continue
		}
		switch k {
		case "subscriber":
		case "subscribers":
		case "service":
			// three keys need to be ignored
		case "badge":
			if v != "" {
				var e error
				_, e = strconv.Atoi(v)
				if e == nil {
					notif.Data["badge"] = v
				} else {
					notif.Data["badge"] = "0"
				}
			}
		default:
			notif.Data[k] = v
		}
	}

	if notif.IsEmpty() {
		logger.Errorf("RequestID=%v From=%v Service=%v NrSubscribers=%v Subscribers=\"%+v\" EmptyNotification", reqID, remoteAddr, service, len(subs), subs)
		details = &APIResponseDetails{RequestID: &reqID, From: &remoteAddr, Service: &service, Code: UNIQUSH_ERROR_EMPTY_NOTIFICATION}
		return nil, details, errors.New("empty notification")
	}
	return notif, nil, nil
}

func (api *RestAPI) pushNotification(reqID string, kv map[string]string, perdp map[string][]string, logger log.Logger, remoteAddr string, handler APIResponseHandler) {
	service, err := getServiceFromMap(kv)
	if err != nil {
		logger.Errorf("RequestID=%v From=%v Cannot get service name: %v; %v", reqID, remoteAddr, service, err)
		handler.AddDetailsToHandler(APIResponseDetails{RequestID: &reqID, From: &remoteAddr, Service: &service, Code: UNIQUSH_ERROR_CANNOT_GET_SERVICE})
		return
	}
	subs, err := getSubscribersFromMap(kv, false)
	if err != nil {
		logger.Errorf("RequestID=%v From=%v Service=%v Cannot get subscriber: %v", reqID, remoteAddr, service, err)
		handler.AddDetailsToHandler(APIResponseDetails{RequestID: &reqID, From: &remoteAddr, Service: &service, Code: UNIQUSH_ERROR_CANNOT_GET_SUBSCRIBER})
		return
	}
	if len(subs) == 0 {
		logger.Errorf("RequestID=%v From=%v Service=%v NoSubscriber", reqID, remoteAddr, service)
		handler.AddDetailsToHandler(APIResponseDetails{RequestID: &reqID, From: &remoteAddr, Service: &service, Code: UNIQUSH_ERROR_NO_SUBSCRIBER})
		return
	}
	dpIds, err := getDeliveryPointIdsFromMap(kv)
	if err != nil {
		logger.Errorf("RequestID=%v From=%v Service=%v Cannot get delivery point ids: %v", reqID, remoteAddr, service, err)
		handler.AddDetailsToHandler(APIResponseDetails{RequestID: &reqID, From: &remoteAddr, Service: &service, Code: UNIQUSH_ERROR_CANNOT_GET_DELIVERY_POINT_ID})
		return
	}
	if len(subs) == 0 {
		logger.Errorf("RequestID=%v From=%v Service=%v NoSubscriber", reqID, remoteAddr, service)
		handler.AddDetailsToHandler(APIResponseDetails{RequestID: &reqID, From: &remoteAddr, Service: &service, Code: UNIQUSH_ERROR_NO_SUBSCRIBER})
		return
	}

	notif, details, err := api.buildNotificationFromKV(reqID, kv, logger, remoteAddr, service, subs)
	if err != nil {
		handler.AddDetailsToHandler(*details)
		return
	}

	logger.Infof("RequestID=%v From=%v Service=%v NrSubscribers=%v Subscribers=\"%+v\"", reqID, remoteAddr, service, len(subs), subs)

	api.backend.Push(reqID, remoteAddr, service, subs, dpIds, notif, perdp, logger, handler)
}

// preview takes key-value pairs (pushservicetype, plus data for building the payload), a logger, and logging data.
func (api *RestAPI) preview(reqID string, kv map[string]string, logger log.Logger, remoteAddr string) PreviewAPIResponseDetails {
	pushServiceType, ok := kv["pushservicetype"]
	if !ok || pushServiceType == "" {
		msg := "Must specify a known pushservicetype"
		return PreviewAPIResponseDetails{Code: UNIQUSH_ERROR_NO_PUSH_SERVICE_TYPE, ErrorMsg: &msg}
	}
	delete(kv, "pushservicetype") // Some modules don't filter this out.
	notif, details, err := api.buildNotificationFromKV(reqID, kv, logger, remoteAddr, "placeholderservice", []string{})
	if err != nil {
		return PreviewAPIResponseDetails{
			Code:     details.Code,
			ErrorMsg: details.ErrorMsg,
		}
	}

	data, err := api.backend.Preview(pushServiceType, notif)
	if err != nil {
		errmsg := err.Error()
		return PreviewAPIResponseDetails{Code: UNIQUSH_ERROR_GENERIC, ErrorMsg: &errmsg}
	}
	return PreviewAPIResponseDetails{Code: UNIQUSH_SUCCESS, Payload: apiBytesToObject(data)}
}

func apiBytesToObject(data []byte) interface{} {
	// currently, all types are JSON. In the future, there may be non-JSON payloads in a protocol.
	// Either return a string or an object (to be converted to JSON again by the API)
	var obj interface{}
	err := json.Unmarshal(data, &obj)
	if err != nil || obj == nil {
		return string(data)
	}
	return obj
}

func (api *RestAPI) stop(w io.Writer, remoteAddr string) {
	api.waitGroup.Wait()
	api.backend.Finalize()
	api.loggers[LoggerWeb].Infof("stopped by %v", remoteAddr)
	if w != nil {
		fmt.Fprintf(w, "Stopped\r\n")
	}
	api.stopChan <- true
}

func (api *RestAPI) numberOfDeliveryPoints(kv map[string][]string, logger log.Logger) int {
	ret := 0
	ss, ok := kv["service"]
	if !ok {
		return ret
	}
	if len(ss) == 0 {
		return ret
	}
	service := ss[0]
	subs, ok := kv["subscriber"]
	if !ok {
		return ret
	}
	if len(subs) == 0 {
		return ret
	}
	sub := subs[0]
	ret = api.backend.NumberOfDeliveryPoints(service, sub, logger)
	return ret
}

func (api *RestAPI) querySubscriptions(kv map[string][]string, logger log.Logger) []byte {
	// "subscriber" is a required parameter
	subscriberParam, ok := kv["subscriber"]
	if !ok || len(subscriberParam) == 0 {
		logger.Errorf("Query=Subscriptions NoSubscriber %v", kv)
		return []byte("[]")
	}
	var services []string
	// "services" is an optional parameter that can have one or more services passed in the form of a CSV
	servicesParam, ok := kv["services"]
	if ok && len(servicesParam) > 0 {
		services = strings.Split(servicesParam[0], ",")
	}
	includeDPIds := false
	if v, ok := kv["include_delivery_point_ids"]; ok && len(v) > 0 && v[0] == "1" {
		includeDPIds = true
	}
	includeSecrets := false
	if v, ok := kv["include_subscription_secrets"]; ok && len(v) > 0 && v[0] == "1" {
		includeSecrets = true
	}
	subscriptions := api.backend.Subscriptions(services, subscriberParam[0], logger, includeDPIds)
	if !includeSecrets {
		for _, subscription := range subscriptions {
			removeSubscriptionSecrets(subscription)
		}
	}
	json, err := json.Marshal(subscriptions)
	if err != nil {
		logger.Errorf("Service=%v Subscriber=%v %s", services, subscriberParam[0], err)
		return []byte("[]")
	}

	return json
}

// subscriptionSecrets are the delivery point fields `/subscriptions` withholds
// unless the caller asks for them with include_subscription_secrets=1.
//
// Only credential material belongs here, and only the kind that works for
// whoever holds it. A device token or an FCM registration id identifies a
// device and is useless without the provider credentials uniqush holds, so a
// copy of one buys nothing; those stay, because reconciling them against an
// application's own records is what this endpoint is for.
//
// A Web Push subscription is not like that. RFC 8291 derives the content
// encryption key from the auth secret, so the endpoint, p256dh and auth
// together are everything an application server needs to push to that browser
// -- with no credential of uniqush's involved, and no way for the subscriber to
// tell the difference. That is the one thing this API gave away that keeps
// working after the reader loses access to it.
//
// A list of fields to withhold rather than a list to allow, unlike
// pspFieldsSafeToReport below. The two endpoints answer different questions:
// /psps describes configuration to a person, where a field nobody thought of is
// better withheld than published, while this one hands a program back its own
// per-device records, where dropping a field nobody thought of breaks a caller
// that was relying on it. A backend that stores new credential material on a
// delivery point has to be added here.
var subscriptionSecrets = map[string]bool{
	// Web Push and UnifiedPush (RFC 8291 s3.2).
	"auth": true,
}

// removeSubscriptionSecrets drops the withheld fields from one subscription.
//
// Dropped rather than replaced with a placeholder, which is the opposite of
// what /psps does with a provider. A person reading /psps wants to know a
// private key is set; a program reading this is going to write whatever it
// finds into its own store, and "[redacted]" is a worse thing to end up with
// there than a key that is plainly absent.
func removeSubscriptionSecrets(subscription map[string]string) {
	for field := range subscriptionSecrets {
		delete(subscription, field)
	}
}

// pspFieldsSafeToReport is what /psps is allowed to answer with. Everything
// else in a provider is replaced by redactedValue.
//
// An allowlist rather than a list of secrets to withhold, because the two ways
// of getting this wrong are not comparable. A field missing from here appears as
// "[redacted]" in a debugging endpoint: visible to whoever wanted it, and a
// one-line fix. A credential missing from a list of things to withhold is key
// material on the wire, and nothing in the response says so. That is not
// hypothetical -- it is how this endpoint published every Web Push provider's
// VAPID private key for as long as uniqush has had Web Push support.
//
// Credential file *paths* are here on purpose. /psps exists to answer "is this
// service set up the way I think it is", and which certificate or service
// account file a provider loads is most of that question. What is not here is
// the material itself: a private key is not configuration, and no amount of
// debugging needs it echoed back.
//
// Adding a field: put it here if it is configuration, and leave it out if it is
// a credential. Leaving it out is the safe mistake.
var pspFieldsSafeToReport = map[string]bool{
	// Every provider.
	"service": true,

	// APNs. cert, key and authkey are paths; keyid and teamid identify the
	// signing key but are useless without the .p8 it names. credrev is a digest
	// of the credential files, which is how the push path notices a certificate
	// rotated in place.
	"cert":        true,
	"key":         true,
	"authkey":     true,
	"keyid":       true,
	"teamid":      true,
	"bundleid":    true,
	"addr":        true,
	"environment": true,
	"endpoint":    true,
	"cacert":      true,
	"skipverify":  true,
	"credrev":     true,

	// FCM, and the same backend as gcm. credentialsfile is a path to the
	// service account JSON, not its contents.
	"projectid":       true,
	"credentialsfile": true,

	// Web Push and UnifiedPush. The public half of the VAPID pair and the
	// contact address sent with it; vapidprivatekey is deliberately absent.
	"vapidpublickey": true,
	"subscriber":     true,

	// ADM. clientid names the security profile; expire and type describe the
	// access token ADM issued, while the token itself is deliberately absent,
	// as is clientsecret.
	"clientid": true,
	"expire":   true,
	"type":     true,
}

// redactedValue stands in for a field /psps will not report.
//
// Present rather than omitted, so that the response still says a provider
// carries the field. An operator checking a Web Push setup can see the private
// key is there without being handed it, and a field left out of the allowlist
// by mistake shows up as this rather than vanishing.
const redactedValue = "[redacted]"

func encodePSPForAPI(psp *push.PushServiceProvider) map[string]string {
	result := make(map[string]string)
	// Volatile first, then fixed, so that fixed data wins a collision. That is
	// the order this has always merged them in.
	for _, data := range []map[string]string{psp.VolatileData, psp.FixedData} {
		for key, value := range data {
			if pspFieldsSafeToReport[key] {
				result[key] = value
			} else {
				result[key] = redactedValue
			}
		}
	}
	return result
}

// queryPSPs returns JSON describing the set of all PSPs stored in Uniqush. This API is intended for debugging/verifying that uniqush is set up properly.
func (api *RestAPI) queryPSPs(logger log.Logger) []byte {
	psps, err := api.backend.GetPushServiceProviderConfigs()
	type responseType struct {
		Services     map[string][]map[string]string `json:"services"`
		ErrorMessage *string                        `json:"errorMsg,omitempty"`
		Code         string                         `json:"code"`
	}
	var r responseType
	r.Services = make(map[string][]map[string]string)
	for _, psp := range psps {
		// Grouped by the provider's own service name rather than by the one in
		// the encoded response, which is a redaction away from being the string
		// every provider gets grouped under.
		service := psp.FixedData["service"]
		r.Services[service] = append(r.Services[service], encodePSPForAPI(psp))
	}
	if err != nil {
		errorMsg := err.Error()
		logger.Errorf("Error querying PSPs in /psps: %v", err)
		r.Code = UNIQUSH_ERROR_DATABASE
		r.ErrorMessage = &errorMsg
	} else {
		r.Code = UNIQUSH_SUCCESS
	}
	json, err := json.Marshal(r)
	if err != nil {
		return []byte("Failed to serialize response")
	}
	return json
}

// checkDatabase reports what does not add up in the database.
//
// The report is returned rather than acted on. A repair running unattended
// against a database nobody has looked at is how a consistency check becomes an
// outage, and every problem it finds already has an existing operation that
// fixes it -- /addpsp for a missing provider, a read for an orphaned delivery
// point.
func (api *RestAPI) checkDatabase(logger log.Logger) []byte {
	report, err := api.backend.CheckDatabase()
	if err != nil {
		logger.Errorf("Error in /checkdb: %v", err)
		errorMsg := err.Error()
		details := APIResponseDetails{Code: UNIQUSH_ERROR_GENERIC, ErrorMsg: &errorMsg}
		encoded, e := json.Marshal(details)
		if e != nil {
			return []byte("Failed to serialize response")
		}
		return encoded
	}

	if report.Healthy() {
		logger.Infof("/checkdb: %s", report.Summary())
	} else {
		// Warn rather than Info: nothing here is urgent, but a database with
		// duplicate providers pushes nondeterministically, and that should not
		// be discoverable only by reading a JSON body.
		//
		// The summary carries every count, so it is the line that matters and it
		// is always written. The individual findings are examples, and are
		// capped: this endpoint exists to be run on a database somebody is
		// already worried about, and an unbounded fan-out of warnings would meet
		// that worry by filling the disk that the logs are on.
		logger.Warnf("/checkdb: %s", report.Summary())
		logged := 0
		for _, problem := range report.Problems {
			if logged >= maxLoggedProblems {
				break
			}
			logger.Warnf("/checkdb: %s", problem)
			logged++
		}
		if remaining := report.TotalProblems() - logged; remaining > 0 {
			logger.Warnf("/checkdb: and %d more not logged; the counts above are complete, and the response body "+
				"carries up to %d examples of each kind", remaining, db.MaxProblemsPerKind)
		}
	}

	encoded, err := json.Marshal(report)
	if err != nil {
		return []byte("Failed to serialize response")
	}
	return encoded
}

// rebuildServiceSet is used to make sure that the /subscriptions and /psps APIs work properly, on uniqush setups created before those APIs existed.
func (api *RestAPI) rebuildServiceSet(logger log.Logger) []byte {
	err := api.backend.RebuildServiceSet()
	var details APIResponseDetails
	if err != nil {
		logger.Errorf("Error in /rebuildserviceset: %v", err)
		errorMsg := err.Error()
		details = APIResponseDetails{
			Code:     UNIQUSH_ERROR_GENERIC,
			ErrorMsg: &errorMsg,
		}
	} else {
		details = APIResponseDetails{Code: UNIQUSH_SUCCESS}
	}
	json, err := json.Marshal(details)
	if err != nil {
		return []byte("Failed to encode response")
	}
	return json
}

func parseKV(form url.Values) (kv map[string]string, perdp map[string][]string) {
	kv = make(map[string]string, len(form))
	perdp = make(map[string][]string, 3)
	perdpPrefix := "uniqush.perdp."
	for k, v := range form {
		if len(k) > len(perdpPrefix) {
			if k[:len(perdpPrefix)] == perdpPrefix {
				key := k[len(perdpPrefix):]
				perdp[key] = v
				continue
			}
		}
		if len(v) > 0 {
			kv[k] = v[0]
		}
	}
	return kv, perdp
}

func (api *RestAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	remoteAddr := r.RemoteAddr

	switch r.URL.Path {
	case QuerySubscriptionsURL:
		r.ParseForm()
		n := api.querySubscriptions(r.Form, api.loggers[LoggerSubscriptions])
		fmt.Fprintf(w, "%s\r\n", n)
		return
	case QueryPushServiceProviders:
		n := api.queryPSPs(api.loggers[LoggerPSPs])
		fmt.Fprintf(w, "%s\r\n", n)
		return
	case RebuildServiceSetURL:
		n := api.rebuildServiceSet(api.loggers[LoggerServices])
		fmt.Fprintf(w, "%s\r\n", n)
		return
	case CheckDatabaseURL:
		n := api.checkDatabase(api.loggers[LoggerServices])
		fmt.Fprintf(w, "%s\r\n", n)
		return
	case QueryNumberOfDeliveryPointsURL:
		r.ParseForm()
		n := api.numberOfDeliveryPoints(r.Form, api.loggers[LoggerWeb])
		fmt.Fprintf(w, "%v\r\n", n)
		return
	case PreviewPushNotificationURL:
		r.ParseForm()
		kv, _ := parseKV(r.Form)
		rid := randomUniqID()
		details := api.preview(rid, kv, api.loggers[LoggerPreview], remoteAddr)
		bytes, err := json.Marshal(details)
		if err != nil {
			fmt.Fprintf(w, "%s\r\n", err.Error())
			return
		}
		fmt.Fprintf(w, "%s\r\n", string(bytes))
		return
	case VersionInfoURL:
		fmt.Fprintf(w, "%v\r\n", api.version)
		api.loggers[LoggerWeb].Infof("Checked version from %v", remoteAddr)
		return
	case StopProgramURL:
		api.stop(w, remoteAddr)
		return
	}
	r.ParseForm()
	kv, perdp := parseKV(r.Form)

	api.waitGroup.Add(1)
	defer api.waitGroup.Done()
	var handler APIResponseHandler
	var details APIResponseDetails
	switch r.URL.Path {
	case AddPushServiceProviderToServiceURL:
		handler = newSimpleResponseHandler(api.loggers[LoggerAddPSP], "AddPushServiceProvider")
		details = api.changePushServiceProvider(kv, api.loggers[LoggerAddPSP], remoteAddr, true)
		handler.AddDetailsToHandler(details)
	case RemovePushServiceProviderFromServiceURL:
		handler = newSimpleResponseHandler(api.loggers[LoggerRemovePSP], "RemovePushServiceProvider")
		details = api.changePushServiceProvider(kv, api.loggers[LoggerRemovePSP], remoteAddr, false)
		handler.AddDetailsToHandler(details)
	case AddDeliveryPointToServiceURL:
		handler = newSimpleResponseHandler(api.loggers[LoggerSub], "Subscribe")
		details = api.changeSubscription(kv, api.loggers[LoggerSub], remoteAddr, true)
		handler.AddDetailsToHandler(details)
	case RemoveDeliveryPointFromServiceURL:
		handler = newSimpleResponseHandler(api.loggers[LoggerUnsub], "Unsubscribe")
		details = api.changeSubscription(kv, api.loggers[LoggerUnsub], remoteAddr, false)
		handler.AddDetailsToHandler(details)
	case PushNotificationURL:
		handler = newPushResponseHandler(api.loggers[LoggerPush])
		rid := randomUniqID()
		api.pushNotification(rid, kv, perdp, api.loggers[LoggerPush], remoteAddr, handler)
	}
	if handler != nil {
		// Be consistent about ending responses in \r\n
		_, err := fmt.Fprintf(w, "%s\r\n", string(handler.ToJSON()))
		if err != nil {
			api.loggers[LoggerWeb].Errorf("Failed to write http response: %v", err)
		}
	}
}

// Run will start the API service, listening for requests on the address addr
func (api *RestAPI) Run(addr string, stopChan chan<- bool) {
	api.loggers[LoggerWeb].Infof("[Start] %s", addr)
	api.loggers[LoggerWeb].Debugf("[Version] %s", api.version)

	http.Handle(StopProgramURL, api)
	http.Handle(VersionInfoURL, api)
	http.Handle(AddPushServiceProviderToServiceURL, api)
	http.Handle(AddDeliveryPointToServiceURL, api)
	http.Handle(RemoveDeliveryPointFromServiceURL, api)
	http.Handle(RemovePushServiceProviderFromServiceURL, api)
	http.Handle(PushNotificationURL, api)
	http.Handle(PreviewPushNotificationURL, api)
	http.Handle(QueryNumberOfDeliveryPointsURL, api)
	http.Handle(QuerySubscriptionsURL, api)
	http.Handle(QueryPushServiceProviders, api)
	http.Handle(RebuildServiceSetURL, api)
	http.Handle(CheckDatabaseURL, api)

	api.stopChan = stopChan
	err := http.ListenAndServe(addr, nil)
	if err != nil {
		api.loggers[LoggerWeb].Fatalf("HTTPServerError \"%v\"", err)
	}
}
