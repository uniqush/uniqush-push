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

package uniqush

import "fmt"

type PushFailureHandler interface {
	OnPushFail(pst PushServiceType, id string, err error)
}

type PushServiceType interface {
	// Passing a pointer to PushServiceProvider allows us to use a memory pool to store a set of empty *PushServiceProvider
	BuildPushServiceProviderFromMap(map[string]string, *PushServiceProvider) error
	BuildDeliveryPointFromMap(map[string]string, *DeliveryPoint) error
	Name() string
	Push(*PushServiceProvider, *DeliveryPoint, *Notification) (string, error)
	SetAsyncFailureHandler(pfp PushFailureHandler)
	Finalize()
}

type PushIncompatibleError struct {
	psp *PushServiceProvider
	dp  *DeliveryPoint
	pst PushServiceType
}

func (p *PushIncompatibleError) Error() string {
	return p.psp.PushServiceName() +
		" does not compatible with " +
		p.dp.PushServiceName() + " using " +
		p.pst.Name()
}

func NewPushIncompatibleError(psp *PushServiceProvider,
	dp *DeliveryPoint,
	pst PushServiceType) *PushIncompatibleError {
	ret := new(PushIncompatibleError)
	ret.psp = psp
	ret.dp = dp
	ret.pst = pst
	return ret
}

type remoteServerError struct {
	msg string
}

func (r *remoteServerError) Error() string {
	return r.msg
}

type QuotaExceededError struct {
	remoteServerError
	PushServiceProvider *PushServiceProvider
}

func NewQuotaExceededError(sp *PushServiceProvider) *QuotaExceededError {
	return &QuotaExceededError{
		remoteServerError{"Service Quota Exceeded" + sp.Name()},
		sp}
}

type DeviceQuotaExceededError struct {
	remoteServerError
}

func NewDeviceQuotaExceededError() *DeviceQuotaExceededError {
	return &DeviceQuotaExceededError{remoteServerError{"Device Quota Exceeded"}}
}

type UnregisteredError struct {
	remoteServerError
	PushServiceProvider *PushServiceProvider
	DeliveryPoint       *DeliveryPoint
}

func NewUnregisteredError(sp *PushServiceProvider, s *DeliveryPoint) *UnregisteredError {
	return &UnregisteredError{
		remoteServerError{"Device Unsubcribed: " + s.Name() + " unsubscribed the service " + sp.Name()},
		sp, s}
}

type NotificationTooBigError struct {
	remoteServerError
	PushServiceProvider *PushServiceProvider
	DeliveryPoint       *DeliveryPoint
	Notification        *Notification
}

func NewNotificationTooBigError(sp *PushServiceProvider, s *DeliveryPoint, n *Notification) *NotificationTooBigError {
	return &NotificationTooBigError{remoteServerError{"Notification Too Big"}, sp, s, n}
}

type InvalidDeliveryPointError struct {
	PushServiceProvider *PushServiceProvider
	DeliveryPoint       *DeliveryPoint
	Err                 error
}

func NewInvalidDeliveryPointError(sp *PushServiceProvider, s *DeliveryPoint, err error) *InvalidDeliveryPointError {
	ret := new(InvalidDeliveryPointError)
	ret.PushServiceProvider = sp
	ret.DeliveryPoint = s
	ret.Err = err
	return ret
}

func (e *InvalidDeliveryPointError) Error() string {
	ret := fmt.Sprintf("Invalid Delivery Point: %s, under service %s; %v",
		e.DeliveryPoint.Name(),
		e.PushServiceProvider.Name(),
		e.Err)
	return ret
}

type InvalidPushServiceProviderError struct {
	remoteServerError
	PushServiceProvider *PushServiceProvider
	Err                 error
}

func NewInvalidPushServiceProviderError(s *PushServiceProvider, err error) *InvalidPushServiceProviderError {
	return &InvalidPushServiceProviderError{
		remoteServerError{
			"Inalid Service Provider: " +
				s.Name() + "; " + err.Error()}, s, err}
}

type RetryError struct {
	remoteServerError
	RetryAfter int
}

func NewRetryError(RetryAfter int) *RetryError {
	return &RetryError{remoteServerError{"Retry"}, RetryAfter}
}

type RefreshDataError struct {
	remoteServerError
	PushServiceProvider *PushServiceProvider
	DeliveryPoint       *DeliveryPoint
	OtherError          error
}

func NewRefreshDataError(psp *PushServiceProvider, dp *DeliveryPoint, o error) *RefreshDataError {
	return &RefreshDataError{
		remoteServerError{"Refresh Push Service Provider"}, psp, dp, o}
}

type InvalidNotification struct {
	PushServiceProvider *PushServiceProvider
	DeliveryPoint       *DeliveryPoint
	Notification        *Notification
	Details             error
}

func NewInvalidNotification(psp *PushServiceProvider,
	dp *DeliveryPoint,
	n *Notification,
	err error) *InvalidNotification {
	return &InvalidNotification{psp, dp, n, err}
}

func (e *InvalidNotification) Error() string {
	return fmt.Sprintf("Invalid Notification: %v; PushServiceProvider: %s; %v",
		e.Notification.Data, e.PushServiceProvider.Name(), e.Details)
}
