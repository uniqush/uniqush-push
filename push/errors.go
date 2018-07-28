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

// Error is a specialized error. It is used for errors which cause the push backend to take actions.
type Error interface {
	error
	isPushError() // Placeholder function to distinguish these from error class
}

type implementsPushError struct{}

func (*implementsPushError) isPushError() {}

var _ Error = &InfoReport{}
var _ Error = &ErrorReport{}
var _ Error = &RetryError{}
var _ Error = &PushServiceProviderUpdate{}
var _ Error = &DeliveryPointUpdate{}
var _ Error = &IncompatibleError{}
var _ Error = &BadDeliveryPoint{}
var _ Error = &BadPushServiceProvider{}
var _ Error = &UnsubscribeUpdate{}
var _ Error = &InvalidRegistrationUpdate{}
var _ Error = &ConnectionError{}

// InfoReport is not an actual error.
// But it is worthy to be reported to the user.
type InfoReport struct {
	implementsPushError
	info string
}

func (e *InfoReport) Error() string {
	return e.info
}

// NewInfo returns an InfoReport for the given message to be reported to the user (with a severity of 'info')
func NewInfo(msg string) *InfoReport {
	return &InfoReport{info: msg}
}

// NewInfof returns an InfoReport for the given format string and arguments to be reported to the user (with a severity of 'info')
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

// NewError returns an ErrorReport for the given error message to be reported to the user (with a severity of 'error')
func NewError(msg string) *ErrorReport {
	return &ErrorReport{msg: msg}
}

// NewErrorf returns an ErrorReport for the given format string and arguments to be reported to the user (with a severity of 'error')
func NewErrorf(f string, v ...interface{}) *ErrorReport {
	return &ErrorReport{msg: fmt.Sprintf(f, v...)}
}

/*********************/

// RetryError indicates to the user that the push attempt for the given delivery point should be re-attempted after the given duration (uniqush retries failed pushes for some push services with exponential backoff and a finite number of re-attempts).
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

// NewRetryErrorWithReason builds a RetryError with the associated error causing uniqush-push to retry the push after the given duration.
func NewRetryErrorWithReason(psp *PushServiceProvider, dp *DeliveryPoint, notif *Notification, after time.Duration, reason error) *RetryError {
	return &RetryError{
		After:       after,
		Provider:    psp,
		Destination: dp,
		Content:     notif,
		Reason:      reason,
	}
}

// NewRetryError builds a RetryError with no associated reason
func NewRetryError(psp *PushServiceProvider, dp *DeliveryPoint, notif *Notification, after time.Duration) *RetryError {
	return &RetryError{
		After:       after,
		Provider:    psp,
		Destination: dp,
		Content:     notif,
	}
}

/*********************/

// PushServiceProviderUpdate is an error object indicating that the push service provider's VolatileData was updated. (E.g. triggered by Update-Client-Auth for GCM/FCM)
type PushServiceProviderUpdate struct { // nolint: golint
	implementsPushError
	Provider *PushServiceProvider
}

// Error returns a representation of the update to the PSP for debugging (probably unused)
func (e *PushServiceProviderUpdate) Error() string {
	return fmt.Sprintf("PushServiceProvider=%v Update", e.Provider.Name())
}

// NewPushServiceProviderUpdate creates an error indicating that the psp's volatile data should be updated
func NewPushServiceProviderUpdate(psp *PushServiceProvider) *PushServiceProviderUpdate {
	return &PushServiceProviderUpdate{Provider: psp}
}

/*********************/

// DeliveryPointUpdate indicates that a delivery point has been updated after a push attempt (e.g. if `registration_id` is included in a GCM/FCM push response payload)
type DeliveryPointUpdate struct {
	implementsPushError
	Destination *DeliveryPoint
}

func (e *DeliveryPointUpdate) Error() string {
	return fmt.Sprintf("DeliveryPoint=%v Update", e.Destination.Name())
}

// NewDeliveryPointUpdate returns a DeliveryPointUpdate indicating error handler should updating the passed in delivery point.
func NewDeliveryPointUpdate(dp *DeliveryPoint) *DeliveryPointUpdate {
	return &DeliveryPointUpdate{Destination: dp}
}

/*********************/

// IncompatibleError indicates that the delivery point was incompatible with the push service, etc. Ideally, this should never happen.
type IncompatibleError struct {
	implementsPushError
}

func (e *IncompatibleError) Error() string {
	return fmt.Sprintf("Incompatible")
}

// NewIncompatibleError creates an IncompatibleError.
func NewIncompatibleError() *IncompatibleError {
	return &IncompatibleError{}
}

/*********************/

// BadDeliveryPoint indicates that the delivery point has missing or invalid data, and as a result, it can't be pushed to. Should not happen unless using incompatible uniqush release (e.g. switching from a newer version to an incompatible older version).
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

// NewBadDeliveryPointWithDetails creates a BadDeliveryPoint error with the delivery point, along with an error message.
func NewBadDeliveryPointWithDetails(dp *DeliveryPoint, details string) *BadDeliveryPoint {
	return &BadDeliveryPoint{Destination: dp, Details: details}
}

/*********************/

// BadPushServiceProvider indicates that uniqush-push or the external push service has rejected our push service provider's credentials.
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

// NewBadPushServiceProviderWithDetails returns a BadPushServiceProvider error with details about why it is invalid.
func NewBadPushServiceProviderWithDetails(psp *PushServiceProvider, details string) *BadPushServiceProvider {
	return &BadPushServiceProvider{Provider: psp, Details: details}
}

/*********************/

// BadNotification indicates that the notification body for a given push service provider or delivery point has been rejected, either by uniqush-push or by the external push service.
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

// NewBadNotificationWithDetails returns a BadNotification with an error message.
func NewBadNotificationWithDetails(details string) *BadNotification {
	return &BadNotification{Details: details}
}

/*********************/

// UnsubscribeUpdate indicates that the external push service told us that the given delivery point's was unsubscribed (and the error handler should remove that delivery point from the db).
type UnsubscribeUpdate struct {
	implementsPushError
	Provider    *PushServiceProvider
	Destination *DeliveryPoint
}

func (e *UnsubscribeUpdate) Error() string {
	return fmt.Sprintf("RequestUnsubscribe %v", e.Destination.Name())
}

// NewUnsubscribeUpdate returns an UnsubscribeUpdate for the given psp and dp.
func NewUnsubscribeUpdate(psp *PushServiceProvider, dp *DeliveryPoint) *UnsubscribeUpdate {
	return &UnsubscribeUpdate{
		Provider:    psp,
		Destination: dp,
	}
}

/*********************/

// InvalidRegistrationUpdate indicates that the external push service provider's response told uniqush that the delivery point was invalid/no longer valid, so the error handler should remove this delivery point from the database.
type InvalidRegistrationUpdate struct {
	implementsPushError
	Provider    *PushServiceProvider
	Destination *DeliveryPoint
}

func (e *InvalidRegistrationUpdate) Error() string {
	return fmt.Sprintf("InvalidRegistration dropping %v", e.Destination.Name())
}

// NewInvalidRegistrationUpdate returns an InvalidRegistrationUpdate for this delivery point and push service provider.
func NewInvalidRegistrationUpdate(psp *PushServiceProvider, dp *DeliveryPoint) *InvalidRegistrationUpdate {
	return &InvalidRegistrationUpdate{
		Provider:    psp,
		Destination: dp,
	}
}

/*********************/

// ConnectionError indicates that we failed to establish a network connection (or send the request) to the external push service. Currently used only for APNS HTTP/2, and handled by reporting the error and giving up.
type ConnectionError struct {
	implementsPushError
	Err error
}

func (e *ConnectionError) Error() string {
	return fmt.Sprintf("ConnectionError %v", e.Err)
}

// NewConnectionError returns a new ConnectionError.
func NewConnectionError(err error) *ConnectionError {
	return &ConnectionError{Err: err}
}
