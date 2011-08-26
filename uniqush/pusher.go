package uniqush

import (
    "os"
)

type Pusher interface {
    Push(Notification, Subscriber) (int, os.Error)
}

