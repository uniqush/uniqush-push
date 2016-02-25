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

// PushError is a specialized error. It is used for errors which cause the push backend to take actions.
type PushError interface {
	error
	isPushError() // Placeholder function to distinguish these from error class
}

type implementsPushError struct{}

func (*implementsPushError) isPushError() {}

var _ PushError = &InfoReport{}
var _ PushError = &ErrorReport{}
var _ PushError = &RetryError{}
var _ PushError = &PushServiceProviderUpdate{}
var _ PushError = &DeliveryPointUpdate{}
var _ PushError = &IncompatibleError{}
var _ PushError = &BadDeliveryPoint{}
var _ PushError = &BadPushServiceProvider{}
var _ PushError = &UnsubscribeUpdate{}
var _ PushError = &InvalidRegistrationUpdate{}
var _ PushError = &ConnectionError{}

// This is not an actual error.
// But it is worthy to be reported to the user.
type InfoReport struct {
	implementsPushError
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

// ErrorReport is like InfoReport, but has a higher severity.
type ErrorReport struct {
	implementsPushError
	msg string
}

func (e *ErrorReport) Error() string {
	return e.msg
}

func NewError(msg string) *ErrorReport {
	return &ErrorReport{msg: msg}
}

func NewErrorf(f string, v ...interface{}) *ErrorReport {
	return &ErrorReport{msg: fmt.Sprintf(f, v...)}
}

/*********************/

type RetryError struct {
	implementsPushError
	After       time.Duration
	Provider    *PushServiceProvider
	Destination *DeliveryPoint
	Content     *Notification
	Reason      error
}

func (e *RetryError) Error() string {
	if e.Reason != nil {
		return fmt.Sprintf("Retry (%v)", e.Reason)
	}
	return fmt.Sprintf("Retry")
}

func NewRetryErrorWithReason(psp *PushServiceProvider, dp *DeliveryPoint, notif *Notification, after time.Duration, reason error) *RetryError {
	return &RetryError{
		After:       after,
		Provider:    psp,
		Destination: dp,
		Content:     notif,
		Reason:      reason,
	}
}

func NewRetryError(psp *PushServiceProvider, dp *DeliveryPoint, notif *Notification, after time.Duration) *RetryError {
	return &RetryError{
		After:       after,
		Provider:    psp,
		Destination: dp,
		Content:     notif,
	}
}

/*********************/

type PushServiceProviderUpdate struct {
	implementsPushError
	Provider *PushServiceProvider
}

func (e *PushServiceProviderUpdate) Error() string {
	return fmt.Sprintf("PushServiceProvider=%v Update", e.Provider.Name())
}

func NewPushServiceProviderUpdate(psp *PushServiceProvider) *PushServiceProviderUpdate {
	return &PushServiceProviderUpdate{Provider: psp}
}

/*********************/

type DeliveryPointUpdate struct {
	implementsPushError
	Destination *DeliveryPoint
}

func (e *DeliveryPointUpdate) Error() string {
	return fmt.Sprintf("DeliveryPoint=%v Update", e.Destination.Name())
}

func NewDeliveryPointUpdate(dp *DeliveryPoint) *DeliveryPointUpdate {
	return &DeliveryPointUpdate{Destination: dp}
}

/*********************/

type IncompatibleError struct {
	implementsPushError
}

func (e *IncompatibleError) Error() string {
	return fmt.Sprintf("Incompatible")
}

func NewIncompatibleError() *IncompatibleError {
	return &IncompatibleError{}
}

/*********************/

type BadDeliveryPoint struct {
	implementsPushError
	Destination *DeliveryPoint
	Details     string
}

func (e *BadDeliveryPoint) Error() string {
	if e.Details != "" {
		return fmt.Sprintf("BadDeliveryPoint %v: %v", e.Destination.Name(), e.Details)
	}
	return fmt.Sprintf("BadDeliveryPoint %v", e.Destination.Name())
}

func NewBadDeliveryPoint(dp *DeliveryPoint) *BadDeliveryPoint {
	return &BadDeliveryPoint{Destination: dp, Details: ""}
}

func NewBadDeliveryPointWithDetails(dp *DeliveryPoint, details string) *BadDeliveryPoint {
	return &BadDeliveryPoint{Destination: dp, Details: details}
}

/*********************/

type BadPushServiceProvider struct {
	implementsPushError
	Provider *PushServiceProvider
	Details  string
}

func (e *BadPushServiceProvider) Error() string {
	if e.Details != "" {
		return fmt.Sprintf("BadPushServiceProvider %v: %v", e.Provider.Name(), e.Details)
	}
	return fmt.Sprintf("BadPushServiceProvider %v", e.Provider.Name())
}

func NewBadPushServiceProvider(psp *PushServiceProvider) *BadPushServiceProvider {
	return &BadPushServiceProvider{Provider: psp, Details: ""}
}

func NewBadPushServiceProviderWithDetails(psp *PushServiceProvider, details string) *BadPushServiceProvider {
	return &BadPushServiceProvider{Provider: psp, Details: details}
}

/*********************/

type BadNotification struct {
	implementsPushError
	Details string
}

func (e *BadNotification) Error() string {
	if e.Details != "" {
		return fmt.Sprintf("Bad Notification: %s", e.Details)
	}
	return fmt.Sprintf("Bad Notification")
}

func NewBadNotification() *BadNotification {
	return &BadNotification{Details: ""}
}

func NewBadNotificationWithDetails(details string) *BadNotification {
	return &BadNotification{Details: details}
}

/*********************/

type UnsubscribeUpdate struct {
	implementsPushError
	Provider    *PushServiceProvider
	Destination *DeliveryPoint
}

func (e *UnsubscribeUpdate) Error() string {
	return fmt.Sprintf("RequestUnsubscribe %v", e.Destination.Name())
}

func NewUnsubscribeUpdate(psp *PushServiceProvider, dp *DeliveryPoint) *UnsubscribeUpdate {
	return &UnsubscribeUpdate{
		Provider:    psp,
		Destination: dp,
	}
}

/*********************/

type InvalidRegistrationUpdate struct {
	implementsPushError
	Provider    *PushServiceProvider
	Destination *DeliveryPoint
}

func (e *InvalidRegistrationUpdate) Error() string {
	return fmt.Sprintf("InvalidRegistration dropping %v", e.Destination.Name())
}

func NewInvalidRegistrationUpdate(psp *PushServiceProvider, dp *DeliveryPoint) *InvalidRegistrationUpdate {
	return &InvalidRegistrationUpdate{
		Provider:    psp,
		Destination: dp,
	}
}

/*********************/

type ConnectionError struct {
	implementsPushError
	Err error
}

func (e *ConnectionError) Error() string {
	return fmt.Sprintf("ConnectionError %v", e.Err)
}

func NewConnectionError(err error) *ConnectionError {
	return &ConnectionError{Err: err}
}
