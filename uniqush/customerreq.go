package uniqush

const (
    ACTION_REGISTER = iota
    ACTION_UNREGISTER
    ACTION_PUSH
)

type CustomerRequest struct {
    action int
}
