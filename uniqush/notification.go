package uniqush

type Notification struct {
    Message string
    Badge int
    Image string
    Sound string

    // Defined but not used now
    IsLoc bool
    Delay bool
    Data map[string]string
}

func NewNotification(message string, data map[string]string) *Notification {
    n := &Notification{Message: message, Data: data}
    n.Badge = -1
    n.IsLoc = false
    n.Delay = false
    return n
}

