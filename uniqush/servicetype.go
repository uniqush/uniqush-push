package uniqush

const (
    SRVTYPE_UNKNOW = iota
    /* For android */
    SRVTYPE_C2DM

    /* For iOS */
    SRVTYPE_APNS

    /* For windows phone */
    SRVTYPE_MPNS

    /* For BlackBerry */
    SRVTYPE_BBPS
)

type ServiceType struct {
    id int
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


