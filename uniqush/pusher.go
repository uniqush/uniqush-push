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

