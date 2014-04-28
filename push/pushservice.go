package push

import (
	"encoding/json"
	"fmt"
	"reflect"
	"time"
)

type PushResult struct {
	Provider     Provider
	Destinations []DeliveryPoint
	Content      *Notification
	MsgIds       []string
	Err          error
	NrRetries    int
}

func (self *PushResult) Wait() error {
	if self == nil || self.Err == nil {
		return nil
	}
	if retry, ok := self.Err.(*ErrorRetry); ok {
		after := time.Duration(retry.RetryAfter)
		if after == 0*time.Second {
			pow := 1
			if self.NrRetries > 0 {
				pow = 1 << (uint(self.NrRetries))
			}
			after = time.Duration(2*pow) * time.Second
		}
		time.Sleep(after)
		return nil
	}
	return nil
}

func (self *PushResult) Retry(resChan chan<- *PushResult) {
	if _, ok := self.Err.(*ErrorRetry); !ok {
		return
	}
	req := &PushRequest{
		Provider:     self.Provider,
		Destinations: self.Destinations,
		Content:      self.Content,
		NrRetries:    self.NrRetries + 1,
	}
	ps, err := GetPushService(self.Provider)
	if err != nil {
		res := *self
		res.Err = err
		resChan <- &res
	}
	ps.Push(req, resChan)
}

func (self *PushResult) Error() string {
	dps := make([]string, 0, len(self.Destinations))
	for _, dp := range self.Destinations {
		dps = append(dps, dp.UniqId())
	}
	if self.Error != nil {
		return fmt.Sprintf("Failed PushServiceProvider=%s DeliveryPoints=%+v %v",
			self.Provider.UniqId(), dps, self.Err)
	}
	return fmt.Sprintf("Success PushServiceProvider=%s DeliveryPoints=%+v Succsess!",
		self.Provider.UniqId(), dps)
}

type PushRequest struct {
	Provider     Provider
	Destinations []DeliveryPoint
	Content      *Notification
	NrRetries    int
}

func (self *PushRequest) AllSuccess(msgIds ...string) *PushResult {
	res := &PushResult{
		Provider:     self.Provider,
		Destinations: self.Destinations,
		Content:      self.Content,
		MsgIds:       msgIds,
		NrRetries:    self.NrRetries,
	}
	return res
}

func (self *PushRequest) OneSuccess(dst DeliveryPoint, msgId string) *PushResult {
	res := &PushResult{
		Provider:     self.Provider,
		Destinations: []DeliveryPoint{dst},
		Content:      self.Content,
		MsgIds:       []string{msgId},
		NrRetries:    self.NrRetries,
	}
	return res
}

func (self *PushRequest) AllError(err error) *PushResult {
	res := &PushResult{
		Provider:     self.Provider,
		Destinations: self.Destinations,
		Content:      self.Content,
		NrRetries:    self.NrRetries,
		Err:          err,
	}
	return res

}

func (self *PushRequest) OneError(dst DeliveryPoint, err error) *PushResult {
	res := &PushResult{
		Provider:     self.Provider,
		Destinations: []DeliveryPoint{dst},
		Content:      self.Content,
		NrRetries:    self.NrRetries,
		Err:          err,
	}
	return res
}

type PushService interface {
	Name() string
	MarshalDeliveryPoint(dp DeliveryPoint) (data []byte, err error)
	UnmarshalDeliveryPoint(data []byte, dp DeliveryPoint) error
	UnmarshalDeliveryPointFromMap(data map[string]string, dp DeliveryPoint) error

	MarshalProvider(p Provider) (data []byte, err error)
	UnmarshalProvider(data []byte, p Provider) error
	UnmarshalProviderFromMap(data map[string]string, p Provider) error

	EmptyProvider() Provider
	EmptyDeliveryPoint() DeliveryPoint

	Push(req *PushRequest, resChan chan<- *PushResult)
	Close() error
}

/*
type UnmarshalFromMapToStructPushService struct {
}

func (self *UnmarshalFromMapToStructPushService) UnmarshalProviderFromMap(data map[string]string, p Provider) error {
	d, err := json.Marshal(data)
	if err != nil {
		return err
	}
	err = json.Unmarshal(d, p)
	if err != nil {
		return err
	}
	return ValidateProvider(p)
}

func (self *UnmarshalFromMapToStructPushService) UnmarshalDeliveryPointFromMap(data map[string]string, dp DeliveryPoint) error {
	d, err := json.Marshal(data)
	if err != nil {
		return err
	}
	err = json.Unmarshal(d, dp)
	if err != nil {
		return err
	}
	return ValidateDeliveryPoint(dp)
}
*/

type mapToPushPeer interface {
	UnmarshalDeliveryPointFromMap(data map[string]string, dp DeliveryPoint) error
	UnmarshalProviderFromMap(data map[string]string, p Provider) error
}

type Validator interface {
	ValidateDeliveryPoint(dp DeliveryPoint) error
	ValidateProvider(p Provider) error
}

type BasicPushService struct {
	This      mapToPushPeer
	Validator Validator
}

func (self *BasicPushService) Close() error {
	return nil
}

func (self *BasicPushService) UnmarshalProviderFromMap(data map[string]string, p Provider) error {
	d, err := json.Marshal(data)
	if err != nil {
		return err
	}
	err = json.Unmarshal(d, p)
	if err != nil {
		return err
	}
	return self.UnmarshalProvider(d, p)
}

func (self *BasicPushService) UnmarshalDeliveryPointFromMap(data map[string]string, dp DeliveryPoint) error {
	d, err := json.Marshal(data)
	if err != nil {
		return err
	}
	err = json.Unmarshal(d, dp)
	if err != nil {
		return err
	}
	return self.UnmarshalDeliveryPoint(d, dp)
}

func (self *BasicPushService) MarshalDeliveryPoint(dp DeliveryPoint) (data []byte, err error) {
	data, err = json.Marshal(dp)
	if err != nil {
		err = fmt.Errorf("Unable to marshal data %+v: %v", dp, err)
		data = nil
	}
	return
}

func (self *BasicPushService) MarshalProvider(dp Provider) (data []byte, err error) {
	data, err = json.Marshal(dp)
	if err != nil {
		err = fmt.Errorf("Unable to marshal data %+v: %v", dp, err)
		data = nil
	}
	return
}

func (self *BasicPushService) oldDataToMap(data []byte) (m map[string]string, err error) {
	var mapslice []map[string]string
	err = json.Unmarshal(data, &mapslice)
	if err != nil {
		err = fmt.Errorf("Unable to use old unmarshal technique. %v: %v", err, string(data))
		return
	}

	// merge these data into one big map
	if len(mapslice) > 1 {
		for _, m := range mapslice[1:] {
			for k, v := range m {
				mapslice[0][k] = v
			}
		}
	} else {
		err = fmt.Errorf("Unable to use old unmarshal technique. Has to be a 2-element slice: %v", string(data))
		return
	}
	m = mapslice[0]
	return
}

func (self *BasicPushService) unmarshal(data []byte, i interface{}) error {
	// Backward compatible!
	if self.This != nil && len(data) > 0 && data[0] != '{' {
		m, err := self.oldDataToMap(data)
		if err != nil {
			err = fmt.Errorf("backward compatiblity issue: %v", err)
			return err
		}
		if dp, ok := i.(DeliveryPoint); ok {
			return self.This.UnmarshalDeliveryPointFromMap(m, dp)
		} else if p, ok := i.(Provider); ok {
			return self.This.UnmarshalProviderFromMap(m, p)
		}
		return fmt.Errorf("Unknown type to unmarshal: %v", reflect.TypeOf(i))
	}
	err := json.Unmarshal(data, i)
	if err != nil {
		err = fmt.Errorf("Unable to marshal data %v: %v", string(data), err)
		return err
	}
	return nil
}

func (self *BasicPushService) UnmarshalProvider(data []byte, p Provider) error {
	err := self.unmarshal(data, p)
	if err != nil {
		return err
	}
	err = ValidateProvider(p)
	if err != nil {
		return err
	}
	if self.Validator != nil {
		err = self.Validator.ValidateProvider(p)
	}
	return err
}

func (self *BasicPushService) UnmarshalDeliveryPoint(data []byte, dp DeliveryPoint) error {
	err := self.unmarshal(data, dp)
	if err != nil {
		return err
	}
	err = ValidateDeliveryPoint(dp)
	if err != nil {
		return err
	}
	if self.Validator != nil {
		err = self.Validator.ValidateDeliveryPoint(dp)
	}
	return err
}
