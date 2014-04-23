package push

import (
	"reflect"
	"testing"
)

type simplePushService struct {
	BasicPushService
	UnmarshalFromMapToStructPushService
}

func (self *simplePushService) Type() string {
	return "badfruit"
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

func TestPushServiceBuildDeliveryPointBackwardCompatible(t *testing.T) {
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
