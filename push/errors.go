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

import (
	"fmt"
	"time"
)

// This is not an actual error.
// But it is worthy to be reported to the user.
type InfoReport struct {
	info string
}

func (e *InfoReport) Error() string {
	return e.info
}

func NewInfo(msg string) *InfoReport {
	return &InfoReport{info: msg}
}

func NewInfof(f string, v ...interface{}) *InfoReport {
	return &InfoReport{info: fmt.Sprintf(f, v...)}
}

/*********************/

type RetryError struct {
	After       time.Duration
	Provider    *PushServiceProvider
	Destination *DeliveryPoint
	Content     *Notification
}

func (e *RetryError) Error() string {
	return fmt.Sprintf("Retry")
}

func NewRetryError(psp *PushServiceProvider, dp *DeliveryPoint, notif *Notification, after time.Duration) error {
	return &RetryError{
		After:       after,
		Provider:    psp,
		Destination: dp,
		Content:     notif,
	}
}

/*********************/

type PushServiceProviderUpdate struct {
	Provider *PushServiceProvider
}

func (e *PushServiceProviderUpdate) Error() string {
	return fmt.Sprintf("PushServiceProvider=%v Update", e.Provider.Name())
}

func NewPushServiceProviderUpdate(psp *PushServiceProvider) error {
	return &PushServiceProviderUpdate{Provider: psp}
}

/*********************/

type DeliveryPointUpdate struct {
	Destination *DeliveryPoint
}

func (e *DeliveryPointUpdate) Error() string {
	return fmt.Sprintf("DeliveryPoint=%v Update", e.Destination.Name())
}

func NewDeliveryPointUpdate(dp *DeliveryPoint) error {
	return &DeliveryPointUpdate{Destination: dp}
}

/*********************/

type IncompatibleError struct {
}

func (e *IncompatibleError) Error() string {
	return fmt.Sprintf("Incompatible")
}

func NewIncompatibleError() error {
	return &IncompatibleError{}
}

/*********************/

type BadDeliveryPoint struct {
	Destination *DeliveryPoint
	Details     string
}

func (e *BadDeliveryPoint) Error() string {
	if e.Details != "" {
		return fmt.Sprintf("BadDeliveryPoint %v: %v", e.Destination.Name(), e.Details)
	}
	return fmt.Sprintf("BadDeliveryPoint %v", e.Destination.Name())
}

func NewBadDeliveryPoint(dp *DeliveryPoint) error {
	return &BadDeliveryPoint{Destination: dp, Details: ""}
}

func NewBadDeliveryPointWithDetails(dp *DeliveryPoint, details string) error {
	return &BadDeliveryPoint{Destination: dp, Details: details}
}

/*********************/

type BadPushServiceProvider struct {
	Provider *PushServiceProvider
	Details  string
}

func (e *BadPushServiceProvider) Error() string {
	if e.Details != "" {
		return fmt.Sprintf("BadPushServiceProvider %v: %v", e.Provider.Name(), e.Details)
	}
	return fmt.Sprintf("BadPushServiceProvider %v", e.Provider.Name())
}

func NewBadPushServiceProvider(psp *PushServiceProvider) error {
	return &BadPushServiceProvider{Provider: psp, Details: ""}
}

func NewBadPushServiceProviderWithDetails(psp *PushServiceProvider, details string) error {
	return &BadPushServiceProvider{Provider: psp, Details: details}
}

/*********************/

type BadNotification struct {
	Details string
}

func (e *BadNotification) Error() string {
	if e.Details != "" {
		return fmt.Sprintf("Bad Notification: %s", e.Details)
	}
	return fmt.Sprintf("Bad Notification")
}

func NewBadNotification() error {
	return &BadNotification{""}
}

func NewBadNotificationWithDetails(details string) error {
	return &BadNotification{details}
}

/*********************/

type UnsubscribeUpdate struct {
	Provider    *PushServiceProvider
	Destination *DeliveryPoint
}

func (e *UnsubscribeUpdate) Error() string {
	return fmt.Sprintf("RequestUnsubscribe %v", e.Destination.Name())
}

func NewUnsubscribeUpdate(psp *PushServiceProvider, dp *DeliveryPoint) error {
	return &UnsubscribeUpdate{
		Provider:    psp,
		Destination: dp,
	}
}

/*********************/

type ConnectionError struct {
	Err error
}

func (e *ConnectionError) Error() string {
	return fmt.Sprintf("ConnectionError %v", e)
}

func NewConnectionError(err error) error {
	return &ConnectionError{Err: err}
}
