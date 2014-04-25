package db

import (
	"testing"

	"github.com/uniqush/uniqush-push/push"
)

type simpleDeliveryPoint struct {
	DevToken     string `json:"devtoken"`
	ProviderName string `json:"provider,omitempty"`
	push.BasicDeliveryPoint
}

func (self *simpleDeliveryPoint) PushService() string {
	return "gcm"
}

func (self *simpleDeliveryPoint) Provider() string {
	return self.ProviderName
}

func (self *simpleDeliveryPoint) UniqId() string {
	return self.DevToken
}

type nopPusher struct {
}

func (self *nopPusher) Push(req *push.PushRequest, resChan chan<- *push.PushResult) {
	return
}

type simpleProvider struct {
	ApiKey    string `json:"apikey"`
	OtherInfo string `json:"other"`
	push.BasicProvider
}

func (self *simpleProvider) UniqId() string {
	return self.ApiKey
}

func (self *simpleProvider) PushService() string {
	return "gcm"
}

type simplePushService struct {
	push.BasicPushService
	push.UnmarshalFromMapToStructPushService
	nopPusher
}

func (self *simplePushService) EmptyProvider() push.Provider {
	return &simpleProvider{}
}

func (self *simplePushService) EmptyDeliveryPoint() push.DeliveryPoint {
	return &simpleDeliveryPoint{}
}

func (self *simplePushService) Name() string {
	return "gcm"
}

func testAddDelProvider(db PushDatabase, t *testing.T) {
	ps := &simplePushService{}
	ps.This = ps
	push.RegisterPushService(ps)

	p := &simpleProvider{
		ApiKey: "key",
	}
	err := db.AddProvider(p)
	if err != nil {
		t.Fatal(err)
	}
	err = db.DelProvider(p)
	if err != nil {
		t.Fatal(err)
	}
}
