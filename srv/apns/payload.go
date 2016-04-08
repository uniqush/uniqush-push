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
	"github.com/uniqush/uniqush-push/srv/apns/common"
)

// validateRawAPNSPayload tests that the client-provided JSON payload can be sent to APNS.
// It converts it to bytes if it is, otherwise it returns a push.PushError.
func validateRawAPNSPayload(payload string) ([]byte, push.PushError) {
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
	aps_dict, ok := aps.(map[string]interface{})
	if !ok {
		return nil, push.NewBadNotificationWithDetails("aps is not a dictionary")
	}
	if _, ok := aps_dict["alert"]; !ok {
		if content_available, ok := aps_dict["content-available"]; !ok || content_available != "1" {
			return nil, push.NewBadNotificationWithDetails("Missing alert and this is not a silent notification(content-available is not 1)")
		}
	}

	// TODO: Could optionally validate provided fields further according to documentation of the "The Notification Payload" section.
	// Creating a custom struct would make it simpler.
	// (E.g. body, action-loc-key, loc-key, loc-args, badge, sound, content-available, launch-image)
	return []byte(payload), nil
}

func toAPNSPayload(n *push.Notification) ([]byte, push.PushError) {
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
		case "action-loc-key":
			alert[k] = v
		case "loc-key":
			alert[k] = v
		case "loc-args":
			alert[k] = parseList(v)
		case "badge", "content-available":
			b, err := strconv.Atoi(v)
			if err != nil {
				continue
			} else {
				aps[k] = b
			}
		case "sound":
			aps["sound"] = v
		case "img":
			alert["launch-image"] = v
		case "id":
			continue
		case "expiry":
			continue
		case "ttl":
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
	j, err := common.MarshalJSONUnescaped(payload)
	if err != nil {
		return nil, push.NewErrorf("Failed to convert notification data to JSON: %v", err)
	}
	if len(j) > maxPayLoadSize {
		return nil, push.NewBadNotificationWithDetails("payload is too large")
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
			if len(elem) > 0 {
				ret = append(ret, string(elem))
			}
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
