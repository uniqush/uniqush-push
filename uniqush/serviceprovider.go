package uniqush

const (
    sender_id = iota
    auth_token
)

type ServiceProvider struct {
    ServiceType
    Name string
    data map[int]string
}

type C2DMServiceProvider interface {
    SenderID() string
    AuthToken() string
}

func NewC2DMServiceProvider(name, senderid, auth string) *ServiceProvider{
    s := &ServiceProvider{SERVICE_C2DM, name, make(map[int]string, 2)}
    s.data[sender_id] = senderid
    s.data[auth_token] = auth
    return s
}

/* TODO Other service providers */

func (sp *ServiceProvider) SenderID() string {
    if sp.ServiceID() == SRVTYPE_C2DM {
        return sp.data[sender_id]
    }
    return ""
}

func (sp *ServiceProvider) AuthToken() string {
    if sp.ServiceID() == SRVTYPE_C2DM {
        return sp.data[auth_token]
    }
    return ""
}

