package push

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
)

type ProviderDeliveryPointPair struct {
	Provider      Provider
	DeliveryPoint DeliveryPoint
}

func (self *ProviderDeliveryPointPair) PushService() string {
	return self.Provider.PushService()
}

func (self *ProviderDeliveryPointPair) Bytes() (data []byte, err error) {
	if self.Provider.PushService() != self.DeliveryPoint.PushService() {
		err = fmt.Errorf("PushServiceProvider=%v DeliveryPoint=%v They are not under same type: %v v.s. %v",
			self.Provider.UniqId(), self.DeliveryPoint.UniqId(),
			self.Provider.PushService(), self.DeliveryPoint.PushService())
		return
	}
	ps, err := GetPushService(self)
	if err != nil {
		return
	}
	pdata, err := ps.MarshalProvider(self.Provider)
	if err != nil {
		return
	}
	ddata, err := ps.MarshalDeliveryPoint(self.DeliveryPoint)
	if err != nil {
		return
	}
	var l uint32
	l = uint32(len(pdata))

	var buf bytes.Buffer
	io.WriteString(&buf, self.PushService())
	io.WriteString(&buf, ":")
	binary.Write(&buf, binary.LittleEndian, l)

	buf.Write(pdata)
	buf.Write(ddata)
	data = buf.Bytes()
	return
}

func (self *ProviderDeliveryPointPair) Load(data []byte) error {
	s := -1
	for i, ch := range data {
		if ch == byte(':') {
			s = i
			break
		}
		// a-zA-Z0-9_.
		if !((ch >= byte('a') && ch <= byte('z')) ||
			(ch >= byte('A') && ch <= byte('Z')) ||
			(ch >= byte('0') && ch <= byte('9')) ||
			ch == byte('_') || ch == byte('.')) {
			return fmt.Errorf("the push service name has unexpected character: %v", string(data[:i+1]))
		}

	}
	if s <= 0 {
		return fmt.Errorf("corrupted data: no push service name in the pair. %v", data)
	}
	psname := string(data[:s])
	data = data[s+1:]
	var n uint32
	buf := bytes.NewBuffer(data)
	err := binary.Read(buf, binary.LittleEndian, &n)
	if err != nil {
		return fmt.Errorf("unable to recover the pair: %v. %v", err, data)
	}
	if len(data) < int(n)+4 {
		return fmt.Errorf("corrupted data: len(data) == %v; < %v", len(data), n+4)
	}
	pdata := data[4 : int(n)+4]
	ddata := data[int(n)+4:]

	ps, err := GetPushServiceByName(psname)
	if err != nil {
		return err
	}
	provider := ps.EmptyProvider()
	err = ps.UnmarshalProvider(pdata, provider)
	if err != nil {
		return err
	}
	self.Provider = provider

	deliveryPoint := ps.EmptyDeliveryPoint()
	err = ps.UnmarshalDeliveryPoint(ddata, deliveryPoint)
	if err != nil {
		return err
	}
	self.DeliveryPoint = deliveryPoint

	if self.Provider.PushService() != self.DeliveryPoint.PushService() {
		return fmt.Errorf("unable to pair the provider and the delivery point: not under same push service. provider %v is under %v; delivery point %v is under %v",
			self.Provider.UniqId(),
			self.Provider.PushService(),
			self.DeliveryPoint.UniqId(),
			self.DeliveryPoint.PushService())
	}
	return nil
}
