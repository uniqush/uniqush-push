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

import (
    "os"
    "fmt"
)

type PushFailureHandler interface {
    OnPushFail(pst PushServiceType, id string, err os.Error)
}

type PushServiceType interface {
    // Passing a pointer to PushServiceProvider allows us to use a memory pool to store a set of empty *PushServiceProvider
    BuildPushServiceProviderFromMap(map[string]string, *PushServiceProvider) os.Error
    BuildDeliveryPointFromMap(map[string]string, *DeliveryPoint) os.Error
    Name() string
    Push(*PushServiceProvider, *DeliveryPoint, *Notification) (string, os.Error)
    SetAsyncFailureHandler(pfp PushFailureHandler)
    Finalize()
}

type PushIncompatibleError struct {
    psp *PushServiceProvider
    dp *DeliveryPoint
    pst PushServiceType
}

func (p *PushIncompatibleError) String() string {
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

func (r *remoteServerError) String() string {
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
    DeliveryPoint *DeliveryPoint
}

func NewUnregisteredError(sp *PushServiceProvider, s *DeliveryPoint) *UnregisteredError {
    return &UnregisteredError{
        remoteServerError{"Device Unsubcribed: " + s.Name() + " unsubscribed the service " + sp.Name()},
        sp, s}
}

type NotificationTooBigError struct {
    remoteServerError
    PushServiceProvider *PushServiceProvider
    DeliveryPoint *DeliveryPoint
    Notification *Notification
}

func NewNotificationTooBigError(sp *PushServiceProvider, s *DeliveryPoint, n *Notification) *NotificationTooBigError{
    return &NotificationTooBigError{remoteServerError{"Notification Too Big"}, sp, s, n}
}

type InvalidDeliveryPointError struct {
    PushServiceProvider *PushServiceProvider
    DeliveryPoint *DeliveryPoint
    Error os.Error
}

func NewInvalidDeliveryPointError(sp *PushServiceProvider, s *DeliveryPoint, err os.Error) *InvalidDeliveryPointError {
    ret := new(InvalidDeliveryPointError)
    ret.PushServiceProvider = sp
    ret.DeliveryPoint = s
    ret.Error = err
    return ret
}

func (e *InvalidDeliveryPointError) String() string {
    ret := fmt.Sprintf("Invalid Delivery Point: %s, under service %s; %v",
                       e.DeliveryPoint.Name(),
                       e.PushServiceProvider.Name(),
                       e.Error)
    return ret
}

type InvalidPushServiceProviderError struct {
    remoteServerError
    PushServiceProvider *PushServiceProvider
    Error os.Error
}

func NewInvalidPushServiceProviderError(s *PushServiceProvider, err os.Error) *InvalidPushServiceProviderError {
    return &InvalidPushServiceProviderError{
        remoteServerError{
            "Inalid Service Provider: " +
            s.Name() + "; " + err.String()}, s, err}
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
    DeliveryPoint *DeliveryPoint
    OtherError os.Error
}

func NewRefreshDataError (psp *PushServiceProvider, dp *DeliveryPoint, o os.Error) *RefreshDataError {
    return &RefreshDataError {
        remoteServerError{"Refresh Push Service Provider"}, psp, dp, o}
}

type InvalidNotification struct {
    PushServiceProvider *PushServiceProvider
    DeliveryPoint *DeliveryPoint
    Notification *Notification
    Error os.Error
}

func NewInvalidNotification (psp *PushServiceProvider,
                             dp *DeliveryPoint,
                             n *Notification,
                             err os.Error) *InvalidNotification {
    return &InvalidNotification{psp, dp, n, err}
}

func (e *InvalidNotification) String() string {
    return fmt.Sprintf("Invalid Notification: %v; PushServiceProvider: %s; %v",
                       e.Notification.Data, e.PushServiceProvider.Name(), e.Error)
}

