package uniqush

type DeliveryPoint struct {
    OSType
    Name string
    token string
    account string
    data map[int]string
}

type AndroidDeliveryPoint interface {
    GoogleAccount() string
    RegistrationID() string
}

type IOSDeliveryPoint interface {
    AppleAccount() string
    DeviceToken() string
}

func NewAndroidDeliveryPoint(name, account, regid string) *DeliveryPoint{
    s := new(DeliveryPoint)
    s.data = make(map[int]string, 2)
    s.groups = make([]string, 10)
    s.Name = name
    s.OSType = OS_ANDROID
    s.token = regid
    s.account = account
    return s
}

func (s *DeliveryPoint) DeviceToken() string {
    if s.OSID() == OSTYPE_IOS {
        return s.token
    }
    return ""
}

func (s *DeliveryPoint) AppleAccount() string {
    if s.OSID() == OSTYPE_ANDROID {
        return s.account
    }
    return ""
}


func (s *DeliveryPoint) GoogleAccount() string {
    if s.OSID() == OSTYPE_ANDROID {
        return s.account
    }
    return ""
}

func (s *DeliveryPoint) RegistrationID() string {
    if s.OSID() == OSTYPE_ANDROID {
        return s.token
    }
    return ""
}


