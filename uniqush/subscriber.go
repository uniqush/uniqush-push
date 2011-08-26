package uniqush

const (
    token = iota
    account_name
)

type Subscriber struct {
    OSType
    Name string
    data map[int]string
    groups []string
}

type AndroidSubscriber interface {
    GoogleAccount() string
    RegistrationID() string
    Groups() []string
}

type IOSSubscriber interface {
    AppleAccount() string
    DeviceToken() string
    Groups() []string
}

func (s *Subscriber) Groups() []string {
    return s.groups
}

func (s *Subscriber) GoogleAccount() string {
    if s.OSID() == OSTYPE_ANDROID {
        return s.data[account_name]
    }
    return ""
}

func (s *Subscriber) RegistrationID() string {
    if s.OSID() == OSTYPE_ANDROID {
        return s.data[token]
    }
    return ""
}


