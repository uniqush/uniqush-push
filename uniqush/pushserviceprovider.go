package uniqush

import (
    "fmt"
    "strings"
)

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
    s := &PushServiceProvider{SERVICE_C2DM, name, senderid, auth}
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

func (sp *PushServiceProvider) UniqStr() string {
    return sp.ServiceName() + ":" + sp.sender_id + "#" + sp.auth_token
}

func (sp *PushServiceProvider) Marshal() []byte {
    str := fmt.Sprintf("%d.%s:%s", sp.ServiceID(), sp.sender_id, sp.auth_token)
    return []byte(str)
}

func (psp *PushServiceProvider) Unmarshal(name string, value []byte) *PushServiceProvider{
    v := string(value)
    var substr string
    var srvtype int
    fmt.Sscanf(v, "%d.%s", &srvtype, &substr)

    psp.ServiceType.id = srvtype
    fields := strings.Split(substr, ":")
    if len(fields) < 2 {
        return nil
    }
    psp.Name = name
    psp.sender_id = fields[0]
    psp.auth_token = fields[1]
    return psp
}

