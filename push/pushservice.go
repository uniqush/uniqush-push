package push

import (
	"encoding/json"
	"fmt"
	"reflect"
)

type PushResult struct {
	Provider    Provider
	Destination DeliveryPoint
	Content     *Notification
	MsgId       string
	Err         error
}

func (self *PushResult) Error() string {
	if self.Error != nil {
		return fmt.Sprintf("Failed PushServiceProvider=%s DeliveryPoint=%s %v",
			self.Provider.UniqId(), self.Destination.UniqId(), self.Err)
	}
	return fmt.Sprintf("Success PushServiceProvider=%s DeliveryPoint=%s Succsess!",
		self.Provider.UniqId(), self.Destination.UniqId())
}

type PushRequest struct {
	Provider     Provider
	Destinations []DeliveryPoint
	Content      *Notification
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
