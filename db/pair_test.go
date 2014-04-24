package db

import (
	"reflect"
	"testing"

	"github.com/uniqush/uniqush-push/push"
)

func TestPairSerialization(t *testing.T) {
	ps := &simplePushService{}
	ps.This = ps
	push.RegisterPushService(ps)

	p := &simpleProvider{
		ApiKey: "key",
	}
	dp := &simpleDeliveryPoint{
		DevToken: "sometoken",
	}

	pair := &ProviderDeliveryPointPair{
		Provider:      p,
		DeliveryPoint: dp,
	}

	data, err := pair.Bytes()
	if err != nil {
		t.Fatal(err)
	}

	np := &ProviderDeliveryPointPair{}
	err = np.Load(data)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(np, pair) {
		t.Errorf("Not same!")
	}
}
