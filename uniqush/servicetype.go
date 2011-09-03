package uniqush

import (
    "strings"
)

const (
    /* For android */
    SRVTYPE_C2DM = iota

    /* For iOS */
    SRVTYPE_APNS

    /* For windows phone */
    SRVTYPE_MPNS

    /* For BlackBerry */
    SRVTYPE_BBPS

    SRVTYPE_NR_PUSH_SERVICE_TYPE
    SRVTYPE_UNKNOWN
)

type ServiceType struct {
    id int
}

var (
    SERVICE_C2DM ServiceType
    SERVICE_APNS ServiceType
)

func init() {
    SERVICE_C2DM = ServiceType{SRVTYPE_C2DM}
    SERVICE_APNS = ServiceType{SRVTYPE_APNS}
}

func ServiceNameToID(name string) int {
    switch(strings.ToLower(name)) {
    case "c2dm":
        return SRVTYPE_C2DM
    case "apns":
        return SRVTYPE_APNS
    case "mpns":
        return SRVTYPE_MPNS
    case "bbps":
        return SRVTYPE_BBPS
    }
    return SRVTYPE_UNKNOWN
}

func (s *ServiceType) ValidServiceType() bool {
    if s.id < 0 || s.id >= SRVTYPE_NR_PUSH_SERVICE_TYPE {
        return false
    }
    return true
}

func (s *ServiceType) ServiceName() string {
    switch (s.id) {
    case SRVTYPE_C2DM:
        return "C2DM"
    case SRVTYPE_APNS:
        return "APNS"
    case SRVTYPE_MPNS:
        return "MPNS"
    case SRVTYPE_BBPS:
        return "BBPS"
    }
    return "Unknown"
}

func (s *ServiceType) String() string {
    return s.ServiceName()
}

func (s *ServiceType) ServiceID() int {
    return s.id
}

func (s *ServiceType) IsCompatible(o *OSType) bool {
    if s.id == SRVTYPE_C2DM && o.id == OSTYPE_ANDROID {
        return true
    }
    if s.id == SRVTYPE_APNS && o.id == OSTYPE_IOS {
        return true
    }
    if s.id == SRVTYPE_MPNS && o.id == OSTYPE_WP {
        return true
    }
    if s.id == SRVTYPE_BBPS && o.id == OSTYPE_BLKBERRY {
        return true
    }
    return false
}

func (s *ServiceType) RelatedOS() *OSType{
    switch (s.id) {
    case SRVTYPE_C2DM:
        return &OSType{OSTYPE_ANDROID}
    case SRVTYPE_APNS:
        return &OSType{OSTYPE_IOS}
    case SRVTYPE_MPNS:
        return &OSType{OSTYPE_WP}
    case SRVTYPE_BBPS:
        return &OSType{OSTYPE_BLKBERRY}
    }
    return &OSType{OSTYPE_UNKOWN}
}


