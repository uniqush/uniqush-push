package uniqush

type DeliveryPoint struct {
    OSType
    Name string
    token string
    account string
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

func (s *DeliveryPoint) Debug() string {
    ret := "OS: "
    ret += s.OSName()
    ret += "\n"

    ret += "Name: " + s.Name + "\n"
    ret += "Account: " + s.account+ "\n"
    ret += "Token: " + s.token+ "\n"
    return ret
}


