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
 */

package apns

// Contains functions for building a payload from the url parameter abstraction.

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/uniqush/uniqush-push/push"
	"github.com/uniqush/uniqush-push/util"
)

// validateRawAPNSPayload tests that the client-provided JSON payload can be sent to APNs.
// It converts it to bytes if it is, otherwise it returns a push.Error.
func validateRawAPNSPayload(payload string) ([]byte, push.Error) {
	// https://developer.apple.com/library/ios/documentation/NetworkingInternet/Conceptual/RemoteNotificationsPG/Chapters/ApplePushService.html#//apple_ref/doc/uid/TP40008194-CH100-SW1
	var data map[string]interface{}
	err := json.Unmarshal([]byte(payload), &data)
	if data == nil {
		return nil, push.NewBadNotificationWithDetails(fmt.Sprintf("Could not parse payload: %v", err))
	}
	aps, ok := data["aps"]
	if !ok {
		return nil, push.NewBadNotificationWithDetails("Payload missing aps")
	}
	apsDict, ok := aps.(map[string]interface{})
	if !ok {
		return nil, push.NewBadNotificationWithDetails("aps is not a dictionary")
	}
	if _, ok := apsDict["alert"]; !ok {
		if contentAvailable, ok := apsDict["content-available"]; !ok || contentAvailable != "1" {
			return nil, push.NewBadNotificationWithDetails("Missing alert and this is not a silent notification(content-available is not 1)")
		}
	}

	// TODO: Could optionally validate provided fields further according to documentation of the "The Notification Payload" section.
	// Creating a custom struct would make it simpler.
	// (E.g. body, action-loc-key, loc-key, loc-args, badge, sound, content-available, launch-image)
	return []byte(payload), nil
}

// toAPNSPayload builds the notification APNs receives from the push
// parameters.
//
// Where a parameter lands is the whole job. Apple reads the reserved keys from
// the "aps" dictionary and nowhere else, and everything not routed there ends
// up beside it, where it reaches the app as custom data and means nothing to
// iOS. A key put in the wrong place is not rejected by anyone: APNs accepts the
// notification, reports success, and delivers something that does not do what
// the caller asked for.
//
// That is what happened to mutable-content (#192). It is the key that tells
// iOS to run the app's notification service extension, so a caller asking for
// a rich notification got a plain one delivered successfully, with nothing
// anywhere to say why. The other aps keys below were in the same position, and
// the ones Apple has added since -- interruption-level and relevance-score, of
// the notification's own presentation -- were never handled at all.
//
// A caller who needs a key this does not know about, or needs one of these
// somewhere other than where Apple puts it, can send the whole notification as
// uniqush.payload.apns.
func toAPNSPayload(n *push.Notification) ([]byte, push.Error) {
	// If "uniqush.payload.apns" is provided, then that will be used instead of the other POST parameters.
	if payloadJSON, ok := n.Data["uniqush.payload.apns"]; ok {
		bytes, err := validateRawAPNSPayload(payloadJSON)
		return bytes, err
	}
	payload := make(map[string]interface{})
	aps := make(map[string]interface{})
	alert := make(map[string]interface{})
	for k, v := range n.Data {
		switch k {
		case "msg":
			alert["body"] = v
		case "title", "action-loc-key", "loc-key", "title-loc-key":
			alert[k] = v
		case "sound", "category", "thread-id", "target-content-id", "interruption-level":
			aps[k] = v
		case "loc-args", "title-loc-args":
			alert[k] = parseList(v)
		case "badge", "content-available", "mutable-content":
			// Numbers to APNs, not the strings they arrive as: Apple's
			// documentation is explicit that content-available and
			// mutable-content are the number 1, and iOS ignores "1".
			b, err := strconv.Atoi(v)
			if err != nil {
				continue
			} else {
				aps[k] = b
			}
		case "relevance-score":
			// The one aps number that is not an integer: 0 to 1, ranking
			// notifications within a summary.
			score, err := strconv.ParseFloat(v, 64)
			if err != nil {
				continue
			}
			aps[k] = score
		case "img":
			alert["launch-image"] = v
		case "id", "expiry", "ttl":
			continue
		default:
			if strings.HasPrefix(k, "uniqush.") { // keys beginning with "uniqush." are reserved by uniqush.
				continue
			}
			payload[k] = v
		}
	}

	aps["alert"] = alert
	payload["aps"] = aps
	j, err := util.MarshalJSONUnescaped(payload)
	if err != nil {
		return nil, push.NewErrorf("Failed to convert notification data to JSON: %v", err)
	}
	return j, nil
}

func parseList(str string) []string {
	ret := make([]string, 0, 10)
	elem := make([]rune, 0, len(str))
	escape := false
	for _, r := range str {
		if escape {
			escape = false
			elem = append(elem, r)
		} else if r == '\\' {
			escape = true
		} else if r == ',' {
			ret = append(ret, string(elem))
			elem = elem[:0]
		} else {
			elem = append(elem, r)
		}
	}
	if len(elem) > 0 {
		ret = append(ret, string(elem))
	}
	return ret
}
