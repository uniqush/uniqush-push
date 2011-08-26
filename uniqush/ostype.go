package uniqush

const (
    OSTYPE_UNKOWN = iota
    OSTYPE_ANDROID
    OSTYPE_IOS
    OSTYPE_WP
    OSTYPE_BLKBERRY
)

/* TODO add version info */
type OSType struct {
    id int
}

func (o *OSType) OSName() string {
    switch (o.id) {
    case OSTYPE_ANDROID:
        return "Android"
    case OSTYPE_IOS:
        return "iOS"
    case OSTYPE_WP:
        return "Windows Phone"
    case OSTYPE_BLKBERRY:
        return "BlackBerry"
    }
    return "Unknown"
}

func (o *OSType) OSID() int {
    return o.id
}

func (o *OSType) String() string {
    return o.OSName()
}


