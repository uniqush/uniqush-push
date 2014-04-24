package push

import (
	"fmt"
	"sync"
)

type PushServiceManager struct {
	lock  sync.RWMutex
	psmap map[string]PushService
}

func (self *PushServiceManager) RegisterPushService(ps PushService) error {
	self.lock.Lock()
	defer self.lock.Unlock()
	if self.psmap == nil {
		self.psmap = make(map[string]PushService, 5)
	}
	psname := ps.Name()
	for _, ch := range psname {
		if !((ch >= 'a' && ch <= 'z') ||
			(ch >= 'A' && ch <= 'Z') ||
			(ch >= '0' && ch <= '9') ||
			ch == '_' || ch == '.') {
			return fmt.Errorf("push service name contains invalid character: %v", psname)
		}
	}
	self.psmap[psname] = ps
	return nil
}

func (self *PushServiceManager) GetPushServiceByName(name string) (ps PushService, err error) {
	self.lock.RLock()
	defer self.lock.RUnlock()

	if p, ok := self.psmap[name]; ok {
		ps = p
		return
	}
	err = fmt.Errorf("Unknown push service: %v", name)
	return
}

type somethingBelongsToPushService interface {
	PushService() string
}

func (self *PushServiceManager) GetPushService(sth somethingBelongsToPushService) (ps PushService, err error) {
	return self.GetPushServiceByName(sth.PushService())
}

var psmngr PushServiceManager

func RegisterPushService(ps PushService) error {
	return psmngr.RegisterPushService(ps)
}

func GetPushServiceByName(typename string) (ps PushService, err error) {
	return psmngr.GetPushServiceByName(typename)
}

func GetPushService(sth somethingBelongsToPushService) (ps PushService, err error) {
	return psmngr.GetPushService(sth)
}
