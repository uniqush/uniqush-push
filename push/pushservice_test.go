package push

import (
	"reflect"
	"testing"
)

type nopPusher struct {
}

func (self *nopPusher) Push(req *PushRequest, resChan chan<- *PushResult) {
	return
}

type simpleProvider struct {
	ApiKey    string `json:"apikey"`
	OtherInfo string `json:"other"`
	BasicProvider
}

func (self *simpleProvider) UniqId() string {
	return self.ApiKey
}

func (self *simpleProvider) PushService() string {
	return "gcm"
}

type simplePushService struct {
	BasicPushService
	UnmarshalFromMapToStructPushService
	nopPusher
}

func (self *simplePushService) Name() string {
	return "badfruit"
}

func (self *simplePushService) EmptyProvider() Provider {
	return &simpleProvider{}
}

func (self *simplePushService) EmptyDeliveryPoint() DeliveryPoint {
	return &simpleDeliveryPoint{}
}

func TestPushServiceBuildDeliveryPoint(t *testing.T) {
	var ps PushService
	ps = &simplePushService{}
	sdp := &simpleDeliveryPoint{
		DevToken: "token",
	}
	sdp.ServiceName = "service"
	sdp.SubscriberName = "somebody"
	var sdp2 simpleDeliveryPoint

	data, err := ps.MarshalDeliveryPoint(sdp)

	if err != nil {
		t.Error(err)
	}

	err = ps.UnmarshalDeliveryPoint(data, &sdp2)
	if err != nil {
		t.Error(err)
	}
	if !reflect.DeepEqual(&sdp2, sdp) {
		t.Errorf("%+v != %+v", &sdp2, sdp)
	}
}

func TestPushServiceBuildDeliveryPointBackwardCompatibility(t *testing.T) {
	var ps PushService
	sps := &simplePushService{}
	sps.This = sps
	ps = sps
	sdp := &simpleDeliveryPoint{
		DevToken: "token",
	}
	sdp.ServiceName = "service"
	sdp.SubscriberName = "somebody"
	var sdp2 simpleDeliveryPoint

	data := []byte("[{\"devtoken\":\"token\",\"service\":\"service\",\"subscriber\":\"somebody\"},{}]")

	err := ps.UnmarshalDeliveryPoint(data, &sdp2)
	if err != nil {
		t.Error(err)
	}
	if !reflect.DeepEqual(&sdp2, sdp) {
		t.Errorf("%+v != %+v", &sdp2, sdp)
	}
}

func TestPushServiceBuildProvider(t *testing.T) {
	var ps PushService
	ps = &simplePushService{}
	sp := &simpleProvider{
		ApiKey: "apikey",
	}
	sp.ServiceName = "service"
	var sp2 simpleProvider

	data, err := ps.MarshalProvider(sp)

	if err != nil {
		t.Error(err)
	}

	err = ps.UnmarshalProvider(data, &sp2)
	if err != nil {
		t.Error(err)
	}
	if !reflect.DeepEqual(&sp2, sp) {
		t.Errorf("%+v != %+v", &sp2, sp)
	}
}

func TestPushServiceBuildProviderBackwardCompatibility(t *testing.T) {
	var ps PushService
	sps := &simplePushService{}
	sps.This = sps
	ps = sps
	sp := &simpleProvider{
		ApiKey: "apikey",
	}
	sp.ServiceName = "service"
	sp.OtherInfo = "foobar"
	var sp2 simpleProvider

	data := []byte("[{\"apikey\":\"apikey\",\"service\":\"service\"},{\"other\":\"foobar\"}]")

	err := ps.UnmarshalProvider(data, &sp2)
	if err != nil {
		t.Error(err)
	}
	if !reflect.DeepEqual(&sp2, sp) {
		t.Errorf("%+v != %+v", &sp2, sp)
	}
}
