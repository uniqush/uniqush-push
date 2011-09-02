package uniqush

import (
    "strings"
)

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

var (
    OS_ANDROID OSType
    OS_IOS OSType
)

func init() {
    OS_ANDROID = OSType{OSTYPE_ANDROID}
    OS_IOS = OSType{OSTYPE_IOS}
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

func OSNameToID(os string) int {
    switch (strings.ToLower(os)) {
    case "android":
        return OSTYPE_ANDROID
    case "ios":
        return OSTYPE_IOS
    case "windowsphone":
        return OSTYPE_WP
    case "blackberry":
        return OSTYPE_BLKBERRY
    }
    return OSTYPE_UNKOWN
}

func (o *OSType) OSID() int {
    return o.id
}

func (o *OSType) String() string {
    return o.OSName()
}


