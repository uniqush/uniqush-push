package uniqush

const (
    ACTION_PUSH = iota
    ACTION_REGISTER
    ACTION_UNREGISTER
    ACTION_ADD_PUSH_SERVICE_PROVIDER
    ACTION_REMOVE_PUSH_SERVICE_PROVIDER
    NR_ACTIONS
)

type Request struct {
    ID string
    Action int
    Service string
    Subscribers []string

    PushServiceProvider *PushServiceProvider
    DeliveryPoint *DeliveryPoint
    Notification *Notification
}


