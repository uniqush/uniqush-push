package dispatch

import (
	"fmt"

	"github.com/uniqush/uniqush-push/push"
)

type Dispatcher struct {
	ResultChannel chan<- *push.PushResult
}

func (self *Dispatcher) Push(
	notif *push.Notification,
	pairs ...*push.ProviderDeliveryPointPair,
) {
	resChan := self.ResultChannel
	n := 1024
	if len(pairs)/2 > n {
		n = len(pairs) / 2
	}
	pmap := make(map[string]*push.PushRequest, n)

	for _, pair := range pairs {
		key := fmt.Sprintf("%v:%v:%v", pair.Provider.Service(), pair.Provider.PushService(), pair.Provider.UniqId())
		var req *push.PushRequest
		ok := false
		if req, ok = pmap[key]; !ok {
			req = new(push.PushRequest)
			req.Content = notif
			req.Provider = pair.Provider
			req.Destinations = make([]push.DeliveryPoint, 0, 16)
			pmap[key] = req
		}
		req.Destinations = append(req.Destinations, pair.DeliveryPoint)
	}

	for _, req := range pmap {
		ps, err := push.GetPushService(req.Provider)
		if ps == nil && err == nil {
			err = fmt.Errorf("unable to get push service", req.Provider.PushService())
		}
		if err != nil {
			res := &push.PushResult{
				Provider:     req.Provider,
				Destinations: req.Destinations,
				Err:          err,
			}
			resChan <- res
			continue
		}
		go func(r *push.PushRequest) {
			ps.Push(r, resChan)
		}(req)
	}
	return
}
