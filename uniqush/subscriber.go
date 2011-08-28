package uniqush

type Subscriber struct {
    OSType
    Name string
    token string
    account string
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
    s.token = regid
    s.account = account
    return s
}

func (s *Subscriber) Groups() []string {
    return s.groups
}

func (s *Subscriber) DeviceToken() string {
    if s.OSID() == OSTYPE_IOS {
        return s.token
    }
    return ""
}

func (s *Subscriber) AppleAccount() string {
    if s.OSID() == OSTYPE_ANDROID {
        return s.account
    }
    return ""
}


func (s *Subscriber) GoogleAccount() string {
    if s.OSID() == OSTYPE_ANDROID {
        return s.account
    }
    return ""
}

func (s *Subscriber) RegistrationID() string {
    if s.OSID() == OSTYPE_ANDROID {
        return s.token
    }
    return ""
}


