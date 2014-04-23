package push

type Provider interface {
	PushService() string
	UniqId() string
	Service() string
}

type BasicProvider struct {
	ServiceName string `json:"service"`
}

func (self *BasicProvider) Service() string {
	return self.ServiceName
}
