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

// Result is an abstraction of the result of a request to push to an external service.
type Result struct {
	Provider    *PushServiceProvider
	Destination *DeliveryPoint
	Content     *Notification
	MsgID       string
	Err         Error
}

// IsError returns true if the result of the push attempt was an error.
func (r *Result) IsError() bool {
	return r.Err != nil
}

// Error returns an error message for failed push requests or a success message for successful pushes
func (r *Result) Error() string {
	if !r.IsError() {
		return fmt.Sprintf("PushServiceProvider=%v DeliveryPoint=%v MsgID=%v Success!",
			r.Provider.Name(),
			r.Destination.Name(),
			r.MsgID)
	}

	return fmt.Sprintf("Failed PushServiceProvider=%s DeliveryPoint=%s %v",
		r.Provider.Name(),
		r.Destination.Name(),
		r.Err)
}

// PushServiceType instances contain abstractions for working with a single push service type (ADM, APNS, GCM, FCM).
type PushServiceType interface {

	// BuildPushServiceProviderFromMap will add generic and service-specific fields to the given PushServiceProvider instances.
	BuildPushServiceProviderFromMap(map[string]string, *PushServiceProvider) error

	BuildDeliveryPointFromMap(map[string]string, *DeliveryPoint) error
	Name() string

	/*
		Push will send push notifications to the given list of delivery points.

		NOTE: This method should always be run in a separate goroutine.
		The implementation of this method should return only
		if it finished all push request.

		Once this method returns, it cannot use the second channel
		to report errors. (For example, it cannot fork a new goroutine
		and use this channel in this goroutine after the function returns.)

		Any implementation MUST close the second channel (chan<- *Result)
		once the works done.
	*/
	Push(*PushServiceProvider, <-chan *DeliveryPoint, chan<- *Result, *Notification)

	// Preview returns the bytes of a notification, for placeholder subscriber data. This makes no service/database calls.
	Preview(*Notification) ([]byte, Error)

	// SetErrorReportChan sets a channel for the push service provider so that it can report error even if
	// there is no method call on it.
	// The type of the errors sent may cause the push service manager to take various actions.
	SetErrorReportChan(errChan chan<- Error)

	// SetPushServiceConfig sets the config for the push service provider.
	// The config for a given pushservicetype is passed to the corresponding PushServiceType
	SetPushServiceConfig(conf *PushServiceConfig)

	// Finalize will release any resources (e.g. network connections) used by this push service type. It is called on shutdown
	Finalize()
}
