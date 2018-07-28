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

// Package common contains common interfaces and data types for abstractions of apns request/response push protocols.
package common

import (
	"github.com/uniqush/uniqush-push/push"
)

// PushRequestProcessor abstracts the different network protocols Apple has for sending push notifications.
type PushRequestProcessor interface {
	// AddRequest adds a push request, and asyncronously processes it.
	// The APNSRequestProcessor will first add errors it encountered sending to add responses and errors to the respective channels and close channels.
	AddRequest(request *PushRequest)

	// GetMaxPayloadSize returns the maximum JSON payload for this protocol.
	GetMaxPayloadSize() int

	// Finalize() will close any connections, waiting for them to finish closing before returning.
	Finalize()

	// SetErrorReportChan sets the error reporting channel (shared with the apnsPushService)
	SetErrorReportChan(errChan chan<- push.Error)

	// SetPushServiceConfig sets the config of this PushRequestProcessor when the service is registered.
	SetPushServiceConfig(c *push.PushServiceConfig)
}

// PushRequest contains the data needed for an push attempt to APNS for the given list of delivery points (for both HTTP/2 and binary APIs).
type PushRequest struct {
	PSP       *push.PushServiceProvider
	Devtokens [][]byte
	Payload   []byte
	MaxMsgID  uint32
	Expiry    uint32

	// DPList is a list of delivery points of the same length as Devtokens. DPList[i].FixedData["dev_token"] == string(Devtokens[i])
	DPList  []*push.DeliveryPoint
	ErrChan chan<- push.Error
	ResChan chan<- *APNSResult
}

// GetID determines the message id associated with a given dev token's index. This is used by the binary protocol.
func (request *PushRequest) GetID(idx int) uint32 {
	if idx < 0 || idx >= len(request.Devtokens) {
		return 0
	}
	startID := request.MaxMsgID - uint32(len(request.Devtokens))
	return startID + uint32(idx)
}

// APNSResult represents the response from the push request to APNs (either from the binary API or HTTP/2)
type APNSResult struct {
	// MsgID is a unique identifier for the given push attempt to APNs (it is unique within a reasonable time window).
	// This is used by the binary API to associate asynchronous errors with the push request.
	MsgID uint32
	// Status is a status code for the binary API. The HTTP/2 errors are also translated to those status codes.
	Status uint8
	// Err will be handled by the error handler for APNS differently based on the type implementing this interface.
	Err push.Error
}
