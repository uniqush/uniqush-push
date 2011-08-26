package uniqush

type Notification struct {
    msg string

    is_loc bool
    badge int
    img string
    data map[string]string
}

func NewNotification(message string, data map[string]string) *Notification {
    n := &Notification{msg: message, data: data}
    n.badge = -1
    n.is_loc = false
    return n
}

