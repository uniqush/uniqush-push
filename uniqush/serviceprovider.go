package uniqush

const (
    SRVP_SENDER_ID = iota
    SRVP_AUTH_TOKEN
)

type ServiceProvider struct {
    ServiceType
    Data map[int]string
}

type C2DMServiceProvider interface {
    SenderID() string
    AuthToken() string
}

/* TODO Other service providers */

func (sp *ServiceProvider) SenderID() string {
    if sp.ServiceID() == SRVTYPE_C2DM {
        if id, ok := sp.Data[SRVP_SENDER_ID]; ok {
            return id
        }
        return ""
    }
    return ""
}

