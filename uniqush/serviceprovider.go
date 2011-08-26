package uniqush

const (
    sender_id = iota
    auth_token
)

type ServiceProvider struct {
    ServiceType
    data map[int]string
}

type C2DMServiceProvider interface {
    SenderID() string
    AuthToken() string
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

