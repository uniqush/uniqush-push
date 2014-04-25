package push

import (
	"encoding/json"
	"reflect"
	"testing"
)

type simpleDeliveryPoint struct {
	DevToken string `json:"devtoken"`
	BasicDeliveryPoint
}

func (self *simpleDeliveryPoint) PushService() string {
	return "apns"
}

func (self *simpleDeliveryPoint) Provider() string {
	return ""
}

func (self *simpleDeliveryPoint) UniqId() string {
	return self.DevToken
}

func (self *simpleDeliveryPoint) PairProvider(p Provider) bool {
	return false
}

func TestSimpleDeliveryPoint(t *testing.T) {
	var dp DeliveryPoint
	sdp := &simpleDeliveryPoint{
		DevToken: "token",
	}
	sdp.ServiceName = "service"
	sdp.SubscriberName = "somebody"
	dp = sdp
	data, err := json.Marshal(dp)
	if err != nil {
		t.Error(err)
	}
	var sdp2 simpleDeliveryPoint
	err = json.Unmarshal(data, &sdp2)
	if err != nil {
		t.Error(err)
	}
	if !reflect.DeepEqual(&sdp2, sdp) {
		t.Errorf("%+v != %+v", &sdp2, sdp)
	}
}
