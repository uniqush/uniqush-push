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

func (r remoteServerError) String() string {
    return r.msg
}

type QuotaExceededError struct {
    remoteServerError
    PushServiceProvider
}

func NewQuotaExceededError(sp PushServiceProvider) QuotaExceededError {
    return QuotaExceededError{
        remoteServerError{"Service Quota Exceeded" + sp.Name},
        sp}
}

type DeviceQuotaExceededError struct {
    remoteServerError
}

func NewDeviceQuotaExceededError() DeviceQuotaExceededError {
    return DeviceQuotaExceededError{remoteServerError{"Device Quota Exceeded"}}
}

type UnregisteredError struct {
    remoteServerError
    PushServiceProvider
    DeliveryPoint
}

func NewUnregisteredError(sp PushServiceProvider, s DeliveryPoint) UnregisteredError {
    return UnregisteredError{
        remoteServerError{"Device Unsubcribed: " + s.Name + " unsubscribed the service " + sp.Name},
        sp, s}
}

type NotificationTooBigError struct {
    remoteServerError
    PushServiceProvider
    DeliveryPoint
    Notification
}

func NewNotificationTooBigError(sp PushServiceProvider, s DeliveryPoint, n Notification) NotificationTooBigError{
    return NotificationTooBigError{remoteServerError{"Notification Too Big"}, sp, s, n}
}

type InvalidDeliveryPointError struct {
    remoteServerError
    PushServiceProvider
    DeliveryPoint
}

func NewInvalidDeliveryPointError(sp PushServiceProvider, s DeliveryPoint) InvalidDeliveryPointError {
    return InvalidDeliveryPointError{
        remoteServerError{"Invalid DeliveryPoint - " +
            s.Name + " is not a valid subscriber for service " + sp.Name},
            sp, s}
}

type InvalidPushServiceProviderError struct {
    remoteServerError
    PushServiceProvider
}

func NewInvalidPushServiceProviderError(s PushServiceProvider) InvalidPushServiceProviderError {
    return InvalidPushServiceProviderError{remoteServerError{"Inalid Service Provider: " + s.Name}, s}
}

type RetryError struct {
    remoteServerError
    RetryAfter int
}

func NewRetryError(RetryAfter int) RetryError {
    return RetryError{remoteServerError{"Retry"}, RetryAfter}
}

