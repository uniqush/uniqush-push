package push

import (
	"fmt"
	"testing"
)

type namedPushService struct {
	name string
	BasicPushService
	UnmarshalFromMapToStructPushService
	nopPusher
}

func (self *namedPushService) Name() string {
	return self.name
}

func TestRegisterPushService(t *testing.T) {
	N := 10
	for i := 0; i < N; i++ {
		go func(n int) {
			ps := &namedPushService{
				name: fmt.Sprintf("%v", n),
			}
			RegisterPushService(ps)
		}(i)
	}
	for i := 0; i < N; i++ {
		go func(n int) {
			name := fmt.Sprintf("%v", n)
			ps, err := GetPushServiceByName(name)
			if err != nil {
				t.Errorf("Unable to get %v: %v", name, err)
			}
			if ps.Name() != name {
				t.Errorf("got a wrong service. should be %v. got %v", name, ps.Name())
			}
		}(i)
	}
}
