package uniqush

import (
    "os"
)

type Pusher interface {
    Push(*ServiceProvider, *Notification, *Subscriber) (string, os.Error)
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
}

func NewQuotaExceededError() QuotaExceededError {
    return QuotaExceededError{remoteServerError{"Service Quota Exceeded"}}
}

type DeviceQuotaExceededError struct {
    remoteServerError
}

func NewDeviceQuotaExceededError() DeviceQuotaExceededError {
    return DeviceQuotaExceededError{remoteServerError{"Device Quota Exceeded"}}
}

type UnregisteredError struct {
    remoteServerError
}

func NewUnregisteredError() UnregisteredError {
    return UnregisteredError{remoteServerError{"Device Unsubcribed"}}
}

type MessageTooBigError struct {
    remoteServerError
}

func NewMessageTooBigError() MessageTooBigError {
    return MessageTooBigError{remoteServerError{"Message Too Big"}}
}

type InvalidSubscriberError struct {
    remoteServerError
}

func NewInvalidSubscriberError() InvalidSubscriberError {
    return InvalidSubscriberError{remoteServerError{"Invalid Subscriber"}}
}

type InvalidServiceProviderError struct {
    remoteServerError
}

func NewInvalidServiceProviderError() InvalidServiceProviderError {
    return InvalidServiceProviderError{remoteServerError{"Inalid Service Provider"}}
}

