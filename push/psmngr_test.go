package push

import (
	"fmt"
	"sync"
	"testing"
)

type namedPushService struct {
	name string
	BasicPushService
	nopPusher
}

func (self *namedPushService) Name() string {
	return self.name
}

func (self *namedPushService) EmptyProvider() Provider {
	return &simpleProvider{}
}

func (self *namedPushService) EmptyDeliveryPoint() DeliveryPoint {
	return &simpleDeliveryPoint{}
}

func TestRegisterPushService(t *testing.T) {
	N := 10
	wg := &sync.WaitGroup{}
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			name := fmt.Sprintf("%v", n)
			ps := &namedPushService{
				name: fmt.Sprintf("%v", n),
			}
			err := RegisterPushService(ps)
			if err != nil {
				t.Errorf("Unable to register %v: %v", name, err)
			}
		}(i)
	}
	wg.Wait()
	for i := 0; i < N; i++ {
		go func(n int) {
			name := fmt.Sprintf("%v", n)
			ps, err := GetPushServiceByName(name)
			if err != nil {
				t.Errorf("Unable to get %v: %v", name, err)
			}
			if ps == nil {
				t.Fatalf("nil ps")
			}
			if ps.Name() != name {
				t.Errorf("got a wrong service. should be %v. got %v", name, ps.Name())
			}
		}(i)
	}
}
