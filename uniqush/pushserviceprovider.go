package uniqush

type PushServiceProvider struct {
    ServiceType
    Name string
    sender_id string
    auth_token string
}

type C2DMServiceProvider interface {
    SenderID() string
    AuthToken() string
}

func NewC2DMServiceProvider(name, senderid, auth string) *PushServiceProvider{
    s := &PushServiceProvider{SERVICE_C2DM, name, senderid, auth, make(map[int]string, 2)}
    return s
}

/* TODO Other service providers */

func (sp *PushServiceProvider) SenderID() string {
    if sp.ServiceID() == SRVTYPE_C2DM {
        return sp.sender_id
    }
    return ""
}

func (sp *PushServiceProvider) AuthToken() string {
    if sp.ServiceID() == SRVTYPE_C2DM {
        return sp.auth_token
    }
    return ""
}

