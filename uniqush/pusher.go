/*
 *  Uniqush by Nan Deng
 *  Copyright (C) 2010 Nan Deng
 *
 *  This software is free software; you can redistribute it and/or
 *  modify it under the terms of the GNU Lesser General Public
 *  License as published by the Free Software Foundation; either
 *  version 3.0 of the License, or (at your option) any later version.
 *
 *  This software is distributed in the hope that it will be useful,
 *  but WITHOUT ANY WARRANTY; without even the implied warranty of
 *  MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the GNU
 *  Lesser General Public License for more details.
 *
 *  You should have received a copy of the GNU Lesser General Public
 *  License along with this software; if not, write to the Free Software
 *  Foundation, Inc., 59 Temple Place, Suite 330, Boston, MA  02111-1307  USA
 *
 *  Nan Deng <monnand@gmail.com>
 *
 */

package uniqush

import (
    "os"
)

type Pusher interface {
    Push(*PushServiceProvider, *DeliveryPoint, *Notification) (string, os.Error)
}

type NullPusher struct {}

func (p *NullPusher) Push(*PushServiceProvider, *DeliveryPoint, *Notification) (string, os.Error) {
    return "Not Implemented", nil
}

type PushErrorIncompatibleOS struct {
    s ServiceType
    o OSType
}

func (p PushErrorIncompatibleOS) String() string {
    return p.s.String() + " does not compatible with " + p.o.String()
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
        remoteServerError{"Service Quota Exceeded" + sp.Name},
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
        remoteServerError{"Device Unsubcribed: " + s.Name + " unsubscribed the service " + sp.Name},
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
            s.Name + " is not a valid subscriber for service " + sp.Name},
            sp, s}
}

type InvalidPushServiceProviderError struct {
    remoteServerError
    PushServiceProvider *PushServiceProvider
}

func NewInvalidPushServiceProviderError(s *PushServiceProvider) *InvalidPushServiceProviderError {
    return &InvalidPushServiceProviderError{remoteServerError{"Inalid Service Provider: " + s.Name}, s}
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
