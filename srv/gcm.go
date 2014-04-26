package srv

import (
	"fmt"

	"github.com/uniqush/uniqush-push/push"
)

type gcmValidator struct {
}

func (self *gcmValidator) ValidateDeliveryPoint(dp push.DeliveryPoint) error {
	var gdp *gcmDeliveryPoint
	ok := false
	if gdp, ok = dp.(*gcmDeliveryPoint); !ok {
		return fmt.Errorf("%v is not a valid gcm delivery point.", dp.UniqId())
	}
	if dp.PushService() != "gcm" {
		return fmt.Errorf("%v is not a valid gcm delivery point. it is a %v device", dp.UniqId(), dp.PushService())
	}
	if gdp.RegId == "" {
		return fmt.Errorf("registration id is empty")
	}
	if gdp.RealRegId == "" {
		gdp.RealRegId = gdp.RegId
	}
	return nil
}

func (self *gcmValidator) ValidateProvider(p push.Provider) error {
	var gp *gcmProvider
	ok := false
	if gp, ok = p.(*gcmProvider); !ok {
		return fmt.Errorf("%v is not a valid gcm delivery point.", p.UniqId())
	}
	if p.PushService() != "gcm" {
		return fmt.Errorf("%v is not a valid gcm delivery point. it is a %v device", p.UniqId(), p.PushService())
	}
	if gp.ApiKey == "" {
		return fmt.Errorf("api key is empty")
	}
	if gp.RealApiKey == "" {
		gp.RealApiKey = gp.ApiKey
	}
	return nil
}

type gcmDeliveryPoint struct {
	RegId        string `json:"regid"`
	RealRegId    string `json:"realregid,omitempty"`
	ProviderName string `json:"provider,omitempty"`
	push.BasicDeliveryPoint
}

func (self *gcmDeliveryPoint) PushService() string {
	return "gcm"
}

func (self *gcmDeliveryPoint) Provider() string {
	return self.ProviderName
}

func (self *gcmDeliveryPoint) UniqId() string {
	return self.RegId
}

func (self *gcmDeliveryPoint) PairProvider(p push.Provider) bool {
	if p.UniqId() == self.ProviderName {
		return false
	}
	self.ProviderName = p.UniqId()
	return true
}

type gcmProvider struct {
	ApiKey     string `json:"apikey"`
	RealApiKey string `json:"realapikey,omitempty"`
	Name       string `json:"name,omitempty"`
	ProjectId  string `json:"projectid"`
	Address    string `json:"addr,omitempty"`
	push.BasicProvider
}

func (self *gcmProvider) PushService() string {
	return "gcm"
}

func (self *gcmProvider) UniqId() string {
	return self.ApiKey
}

type gcmPushService struct {
	push.BasicPushService
}

const (
	gcmServiceURL string = "https://android.googleapis.com/gcm/send"
)

func InstallGCM() {
	ps := &gcmPushService{}
	ps.This = ps
	ps.Validator = &gcmValidator{}
	push.RegisterPushService(ps)
}

func (self *gcmPushService) Name() string {
	return "gcm"
}
func (self *gcmPushService) EmptyDeliveryPoint() push.DeliveryPoint {
	return &gcmDeliveryPoint{}
}
func (self *gcmPushService) EmptyProvider() push.Provider {
	return &gcmProvider{}
}

func (self *gcmPushService) Push(req *push.PushRequest, resChan chan<- *push.PushResult) {
	return
}
