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
)

type PushServiceType interface {
    BuildPushServiceProviderFromMap(map[string]string) (*PushServiceProvider, os.Error)
    BuildDeliveryPointFromMap(map[string]string) (*DeliveryPoint, os.Error)
    Name() string
    Push(*PushServiceProvider, *DeliveryPoint, *Notification) (string, os.Error)
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

/* Begin Obsoleted */
type PushErrorIncompatibleOS struct {
    s ServiceType
    o OSType
}

func (p *PushErrorIncompatibleOS) String() string {
    return p.s.String() + " does not compatible with " + p.o.String()
}
/* End Obsoleted */


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
    remoteServerError
    PushServiceProvider *PushServiceProvider
    DeliveryPoint *DeliveryPoint
}

func NewInvalidDeliveryPointError(sp *PushServiceProvider, s *DeliveryPoint) *InvalidDeliveryPointError {
    return &InvalidDeliveryPointError{
        remoteServerError{"Invalid DeliveryPoint - " +
            s.Name() + " is not a valid subscriber for service " + sp.Name()},
            sp, s}
}

type InvalidPushServiceProviderError struct {
    remoteServerError
    PushServiceProvider *PushServiceProvider
}

func NewInvalidPushServiceProviderError(s *PushServiceProvider) *InvalidPushServiceProviderError {
    return &InvalidPushServiceProviderError{remoteServerError{"Inalid Service Provider: " + s.Name()}, s}
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

