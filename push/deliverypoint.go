package push

type DeliveryPoint interface {
	PushService() string
	Provider() string
	UniqId() string
	Service() string
	Subscriber() string
}

type BasicDeliveryPoint struct {
	ServiceName    string `json:"service"`
	SubscriberName string `json:"subscriber"`
}

func (self *BasicDeliveryPoint) Service() string {
	return self.ServiceName
}

func (self *BasicDeliveryPoint) Subscriber() string {
	return self.SubscriberName
}
