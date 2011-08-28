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

func NewAndroidSubscriber(name, account, regid string) *Subscriber{
    s := new(Subscriber)
    s.data = make(map[int]string, 2)
    s.groups = make([]string, 10)
    s.Name = name
    s.OSType = OS_ANDROID
    s.data[token] = regid
    s.data[account_name] = account
    return s
}

func (s *Subscriber) Groups() []string {
    return s.groups
}

func (s *Subscriber) DeviceToken() string {
    if s.OSID() == OSTYPE_IOS {
        return s.data[token]
    }
    return ""
}

func (s *Subscriber) AppleAccount() string {
    if s.OSID() == OSTYPE_ANDROID {
        return s.data[account_name]
    }
    return ""
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


