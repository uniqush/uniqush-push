package push

import "fmt"

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

func ValidateProvider(p Provider) error {
	if p == nil {
		return fmt.Errorf("nil delivery point")
	}
	if p.Service() == "" {
		return fmt.Errorf("empty service name")
	}
	if p.PushService() == "" {
		return fmt.Errorf("empty push service name")
	}
	if !isValidServiceName(p.Service()) {
		return fmt.Errorf("%v is not a valid service name.", p.Service())
	}
	if !isValidPushServiceName(p.PushService()) {
		return fmt.Errorf("%v is not a valid push service name.", p.PushService())
	}
	return nil
}
