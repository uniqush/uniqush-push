package uniqush

const (
    OSTYPE_UNKOWN = iota
    OSTYPE_ANDROID
    OSTYPE_IOS
    OSTYPE_WP
    OSTYPE_BLKBERRY
)

type Subscriber struct {
    ostype int
    name string
    data map[string]string
}

