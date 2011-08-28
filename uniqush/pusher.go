package uniqush

import (
    "os"
)

type Pusher interface {
    Push(*ServiceProvider, *Subscriber, *Notification) (string, os.Error)
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
    ServiceProvider
}

func NewQuotaExceededError(sp ServiceProvider) QuotaExceededError {
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
    Service ServiceProvider
    Subscriber Subscriber
}

func NewUnregisteredError(sp ServiceProvider, s Subscriber) UnregisteredError {
    return UnregisteredError{
        remoteServerError{"Device Unsubcribed: " + s.Name + " unsubscribed the service " + sp.Name},
        sp, s}
}

type NotificationTooBigError struct {
    remoteServerError
    Service ServiceProvider
    Subscriber Subscriber
    Notification Notification
}

func NewNotificationTooBigError(sp ServiceProvider, s Subscriber, n Notification) NotificationTooBigError{
    return NotificationTooBigError{remoteServerError{"Notification Too Big"}, sp, s, n}
}

type InvalidSubscriberError struct {
    remoteServerError
    Service ServiceProvider
    Subscriber Subscriber
}

func NewInvalidSubscriberError(sp ServiceProvider, s Subscriber) InvalidSubscriberError {
    return InvalidSubscriberError{
        remoteServerError{"Invalid Subscriber - " +
            s.Name + " is not a valid subscriber for service " + sp.Name},
            sp, s}
}

type InvalidServiceProviderError struct {
    remoteServerError
    Service ServiceProvider
}

func NewInvalidServiceProviderError(s ServiceProvider) InvalidServiceProviderError {
    return InvalidServiceProviderError{remoteServerError{"Inalid Service Provider: " + s.Name}, s}
}

type RetryError struct {
    remoteServerError
    RetryAfter int
}

func NewRetryError(RetryAfter int) RetryError {
    return RetryError{remoteServerError{"Retry"}, RetryAfter}
}

