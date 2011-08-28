package uniqush

type ServiceProvider struct {
    ServiceType
    Name string
    sender_id string
    auth_token string
    data map[int]string
}

type C2DMServiceProvider interface {
    SenderID() string
    AuthToken() string
}

func NewC2DMServiceProvider(name, senderid, auth string) *ServiceProvider{
    s := &ServiceProvider{SERVICE_C2DM, name, senderid, auth, make(map[int]string, 2)}
    return s
}

/* TODO Other service providers */

func (sp *ServiceProvider) SenderID() string {
    if sp.ServiceID() == SRVTYPE_C2DM {
        return sp.sender_id
    }
    return ""
}

func (sp *ServiceProvider) AuthToken() string {
    if sp.ServiceID() == SRVTYPE_C2DM {
        return sp.auth_token
    }
    return ""
}

