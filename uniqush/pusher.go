package uniqush

import (
    "os"
)

type Pusher interface {
    Push(*PushServiceProvider, *DeliveryPoint, *Notification) (string, os.Error)
}

type PushErrorIncompatibleOS struct {
    s ServiceType
    o OSType
}

func (p PushErrorIncompatibleOS) String() string {
    return p.s.String() + " does not compatible with " + p.o.String()
}

type RemoteServerError struct {
    msg string
}

func (r RemoteServerError) String() string {
    return r.msg
}

type QuotaExceededError struct {
    RemoteServerError
    PushServiceProvider
}

func NewQuotaExceededError(sp PushServiceProvider) QuotaExceededError {
    return QuotaExceededError{
        RemoteServerError{"Service Quota Exceeded" + sp.Name},
        sp}
}

type DeviceQuotaExceededError struct {
    RemoteServerError
}

func NewDeviceQuotaExceededError() DeviceQuotaExceededError {
    return DeviceQuotaExceededError{RemoteServerError{"Device Quota Exceeded"}}
}

type UnregisteredError struct {
    RemoteServerError
    PushServiceProvider
    DeliveryPoint
}

func NewUnregisteredError(sp PushServiceProvider, s DeliveryPoint) UnregisteredError {
    return UnregisteredError{
        RemoteServerError{"Device Unsubcribed: " + s.Name + " unsubscribed the service " + sp.Name},
        sp, s}
}

type NotificationTooBigError struct {
    RemoteServerError
    PushServiceProvider
    DeliveryPoint
    Notification
}

func NewNotificationTooBigError(sp PushServiceProvider, s DeliveryPoint, n Notification) NotificationTooBigError{
    return NotificationTooBigError{RemoteServerError{"Notification Too Big"}, sp, s, n}
}

type InvalidDeliveryPointError struct {
    RemoteServerError
    PushServiceProvider
    DeliveryPoint
}

func NewInvalidDeliveryPointError(sp PushServiceProvider, s DeliveryPoint) InvalidDeliveryPointError {
    return InvalidDeliveryPointError{
        RemoteServerError{"Invalid DeliveryPoint - " +
            s.Name + " is not a valid subscriber for service " + sp.Name},
            sp, s}
}

type InvalidPushServiceProviderError struct {
    RemoteServerError
    PushServiceProvider
}

func NewInvalidPushServiceProviderError(s PushServiceProvider) InvalidPushServiceProviderError {
    return InvalidPushServiceProviderError{RemoteServerError{"Inalid Service Provider: " + s.Name}, s}
}

type RetryError struct {
    RemoteServerError
    RetryAfter int
}

func NewRetryError(RetryAfter int) RetryError {
    return RetryError{RemoteServerError{"Retry"}, RetryAfter}
}

