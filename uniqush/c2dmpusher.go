package uniqush

import (
    "os"
)

/* FIXME
 * Yes, it is http not https
 * Because:
 *  1) The certificate does not match the host name 
 *      android.apis.google.com
 *  2) Go does not support (until now) user defined
 *      verifier for TLS
 * The user defined verifier feature was submitted
 * and under reviewed:
 * http://codereview.appspot.com/4964043/
 *
 * However, even we can use a fake verifier, there
 * is still a security issue.
 *
 * Hope goole could fix the certificate problem
 * soon, or we have to use C2DM as an unsecure
 * service.
 */
const (
    service_url string = "http://android.apis.google.com/c2dm/send"
)

type C2DMPusher struct {
    ServiceType
}

func NewC2DMPusher() *C2DMPusher {
    ret := &C2DMPusher{ServiceType{SRVTYPE_C2DM}}
    return ret
}

func (n *Notification) toC2DMFormat() map[string]string {
    /* TODO do we need other field? */
    ret := make(map[string]string, len(n.data) + 5)
    ret["msg"] = n.msg
    return ret
}

func (p *C2DMPusher) Push(n *Notification, s *Subscriber) (string, os.Error) {
    ret_id := ""
    if !p.IsCompatible(&s.OSType) {
        return "", &PushErrorIncompatibleOS{p.ServiceType, s.OSType}
    }
    data := n.toC2DMFormat()
    ret_id = data["msg"]
    return ret_id, nil
}

