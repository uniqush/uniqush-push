package push

import (
	"fmt"
	"regexp"
)

type DeliveryPoint interface {
	// APNS, GCM, ect.
	PushService() string
	Provider() string
	UniqId() string
	Service() string
	Subscriber() string

	// Return true if the content of the delivery point is changed
	// i.e. need to update in the database
	PairProvider(p Provider) bool
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

var validServiceNamePattern *regexp.Regexp
var validSubscriberNamePattern *regexp.Regexp
var validPushServiceNamePattern *regexp.Regexp

func init() {
	validServiceNamePattern = regexp.MustCompile(`[0-9a-zA-Z\._@]+`)
	validSubscriberNamePattern = regexp.MustCompile(`[0-9a-zA-Z\._@]+`)
	validPushServiceNamePattern = regexp.MustCompile(`[0-9a-zA-Z\._]+`)
}

func isValidServiceName(srv string) bool {
	return validServiceNamePattern.MatchString(srv)
}

func isValidSubscriberName(srv string) bool {
	return validSubscriberNamePattern.MatchString(srv)
}

func isValidPushServiceName(srv string) bool {
	return validPushServiceNamePattern.MatchString(srv)
}

func ValidateDeliveryPoint(dp DeliveryPoint) error {
	if dp == nil {
		return fmt.Errorf("nil delivery point")
	}
	if dp.Service() == "" {
		return fmt.Errorf("empty service name")
	}
	if dp.Subscriber() == "" {
		return fmt.Errorf("empty subscriber name")
	}
	if dp.PushService() == "" {
		return fmt.Errorf("empty push service name")
	}
	if !isValidServiceName(dp.Service()) {
		return fmt.Errorf("%v is not a valid service name.", dp.Service())
	}
	if !isValidSubscriberName(dp.Subscriber()) {
		return fmt.Errorf("%v is not a valid subscriber name.", dp.Subscriber())
	}
	if !isValidPushServiceName(dp.PushService()) {
		return fmt.Errorf("%v is not a valid push service name.", dp.PushService())
	}
	return nil
}
