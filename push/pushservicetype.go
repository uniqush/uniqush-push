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

package push

import "fmt"

type PushResult struct {
	Provider    *PushServiceProvider
	Destination *DeliveryPoint
	Content     *Notification
	MsgId       string
	Err         error
}

func (r *PushResult) IsError() bool {
	if r.Err == nil {
		return false
	}
	return true
}

func (r *PushResult) Error() string {
	if !r.IsError() {
		return fmt.Sprintf("PushServiceProvider=%v DeliveryPoint=%v MsgId=%v Succsess!",
			r.Provider.Name(),
			r.Destination.Name(),
			r.MsgId)
	}

	ret := fmt.Sprintf("Failed PushServiceProvider=%s DeliveryPoint=%s %v",
		r.Provider.Name(),
		r.Destination.Name(),
		r.Err)
	return ret
}

type PushServiceType interface {

	// Passing a pointer to PushServiceProvider allows us
	// to use a memory pool to store a set of empty *PushServiceProvider
	BuildPushServiceProviderFromMap(map[string]string, *PushServiceProvider) error

	BuildDeliveryPointFromMap(map[string]string, *DeliveryPoint) error
	Name() string

	// NOTE: This method should always be run in a separate goroutine.
	// The implementation of this method should return only
	// if it finished all push request.
	//
	// Once this method returns, it cannot use the second channel
	// to report error. (For example, it cannot fork a new goroutine
	// and use this channel in this goroutine after the function returns.)
	//
	// Any implementation MUST close the second channel (chan<- *PushResult)
	// once the works done.
	Push(*PushServiceProvider, <-chan *DeliveryPoint, chan<- *PushResult, *Notification)

	// Set a channel for the push service provider so that it can report error even if
	// there is no method call on it.
	SetErrorReportChan(errChan chan<- error)

	Finalize()
}
