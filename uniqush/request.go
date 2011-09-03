package uniqush

const (
    ACTION_PUSH = iota
    ACTION_SUBSCRIBE
    ACTION_UNSUBSCRIBE
    ACTION_ADD_PUSH_SERVICE_PROVIDER
    ACTION_REMOVE_PUSH_SERVICE_PROVIDER
    NR_ACTIONS
)

type Request struct {
    ID string
    Action int
    Service string
    RequestSenderAddr string
    Subscribers []string
    PreferedService int

    PushServiceProvider *PushServiceProvider
    DeliveryPoint *DeliveryPoint
    Notification *Notification
}

var (
    actionNames []string
)

func init() {
    actionNames = make([]string, NR_ACTIONS)
    actionNames[ACTION_PUSH] = "Push"
    actionNames[ACTION_SUBSCRIBE] = "Subcribe"
    actionNames[ACTION_UNSUBSCRIBE] = "Unsubcribe"
    actionNames[ACTION_ADD_PUSH_SERVICE_PROVIDER] = "AddPushServiceProvider"
    actionNames[ACTION_REMOVE_PUSH_SERVICE_PROVIDER] = "RemovePushServiceProvider"
}

func (req *Request) ValidAction() bool {
    if req.Action < 0 || req.Action >= len(actionNames) {
        return false
    }
    return true
}

func (req *Request) ActionName() string {
    if req.ValidAction() {
        return actionNames[req.Action]
    }
    return "InvalidAction"
}

