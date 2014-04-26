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
	p.ServiceName = "service"
	dp.ServiceName = "service"
	dp.SubscriberName = "sub"

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

func TestPairSerializationNoEnoughData(t *testing.T) {
	ps := &simplePushService{}
	ps.This = ps
	push.RegisterPushService(ps)

	p := &simpleProvider{
		ApiKey: "key",
	}
	dp := &simpleDeliveryPoint{
		DevToken: "sometoken",
	}
	p.ServiceName = "service"
	dp.ServiceName = "service"
	dp.SubscriberName = "sub"

	pair := &ProviderDeliveryPointPair{
		Provider:      p,
		DeliveryPoint: dp,
	}

	data, err := pair.Bytes()
	if err != nil {
		t.Fatal(err)
	}

	np := &ProviderDeliveryPointPair{}
	err = np.Load(data[:len(data)/2])
	if err == nil {
		t.Fatal("Should fail")
	}
}

func TestPairSerializationNoService(t *testing.T) {
	ps := &simplePushService{}
	ps.This = ps
	push.RegisterPushService(ps)

	p := &simpleProvider{
		ApiKey: "key",
	}
	dp := &simpleDeliveryPoint{
		DevToken: "sometoken",
	}
	p.ServiceName = "service"
	dp.ServiceName = "service"
	dp.SubscriberName = "sub"

	pair := &ProviderDeliveryPointPair{
		Provider:      p,
		DeliveryPoint: dp,
	}

	data, err := pair.Bytes()
	if err != nil {
		t.Fatal(err)
	}

	np := &ProviderDeliveryPointPair{}
	err = np.Load(data[:len(data)/2])
	if err == nil {
		t.Fatal("Should fail")
	}
}

func TestPairSerializationInvalidPushServiceName(t *testing.T) {
	ps := &simplePushService{}
	ps.This = ps
	push.RegisterPushService(ps)

	p := &simpleProvider{
		ApiKey: "key",
	}
	dp := &simpleDeliveryPoint{
		DevToken: "sometoken",
	}
	p.ServiceName = "service"
	dp.ServiceName = "service"
	dp.SubscriberName = "sub"

	pair := &ProviderDeliveryPointPair{
		Provider:      p,
		DeliveryPoint: dp,
	}

	data, err := pair.Bytes()
	if err != nil {
		t.Fatal(err)
	}

	np := &ProviderDeliveryPointPair{}
	data[1] = byte('{')
	err = np.Load(data)
	if err == nil {
		t.Fatal("Should fail")
	}
}

func TestPairSerializationUnknownPushServiceName(t *testing.T) {
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
	p.ServiceName = "service"
	dp.ServiceName = "service"
	dp.SubscriberName = "sub"

	data, err := pair.Bytes()
	if err != nil {
		t.Fatal(err)
	}

	np := &ProviderDeliveryPointPair{}
	data[1] = byte('0')
	err = np.Load(data)
	if err == nil {
		t.Fatal("Should fail")
	}
}

func TestPairSerializationInvalidData(t *testing.T) {
	ps := &simplePushService{}
	ps.This = ps
	push.RegisterPushService(ps)

	data := []byte("nonononono")
	np := &ProviderDeliveryPointPair{}
	err := np.Load(data)
	if err == nil {
		t.Fatal("Should fail")
	}
}

func TestPairSerializationNoContent(t *testing.T) {
	ps := &simplePushService{}
	ps.This = ps
	push.RegisterPushService(ps)

	data := []byte("nonononono:")
	np := &ProviderDeliveryPointPair{}
	err := np.Load(data)
	if err == nil {
		t.Fatal("Should fail")
	}
}
