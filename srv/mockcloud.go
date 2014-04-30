package srv

import (
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/kr/pretty"
	"github.com/uniqush/uniqush-push/push"
)

type CloudMessage struct {
	SenderId    string
	ReceiverIds []string
	Message     map[string]string
	ResultChan  chan<- *push.PushResult
}

type MockCloud interface {
	NextMessage() (*CloudMessage, error)
	SameProvider(provider push.Provider, senderId string) bool
	SameDeliveryPoint(dp push.DeliveryPoint, recvId string) bool
	SameNotification(notif *push.Notification, msg map[string]string) bool
	Close() error
}

type reqResPair struct {
	Request    *push.PushRequest
	Results    []*push.PushResult
	NrMentions uint64
}

func areSameDestinations(cloud MockCloud, recvIds []string, dps []push.DeliveryPoint) bool {
	if len(recvIds) != len(dps) {
		return false
	}
	for _, dp := range dps {
		found := false
		for _, recvId := range recvIds {
			if cloud.SameDeliveryPoint(dp, recvId) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func (self *reqResPair) getResults(
	cloud MockCloud,
	msg *CloudMessage,
) []*push.PushResult {
	req := self.Request
	if !cloud.SameNotification(req.Content, msg.Message) {
		return nil
	}
	if !cloud.SameProvider(req.Provider, msg.SenderId) {
		return nil
	}
	if !areSameDestinations(cloud, msg.ReceiverIds, req.Destinations) {
		return nil
	}
	atomic.AddUint64(&self.NrMentions, 1)
	ret := make([]*push.PushResult, 0, len(self.Results))

	for _, r := range self.Results {
		ret = append(ret, r)
	}
	return ret
}

type ExpectedResultSet struct {
	cloud MockCloud
	res   []*reqResPair
}

func (self *ExpectedResultSet) AssertMentions(f func(uint64, *push.PushRequest) bool) error {
	errMsg := make([]string, 0, 10)
	for _, r := range self.res {
		x := atomic.LoadUint64(&r.NrMentions)
		if !f(x, r.Request) {
			errMsg = append(errMsg, pretty.Sprintf("%# v mentioned %v times", r.Request, x))
		}
	}
	if len(errMsg) > 0 {
		return fmt.Errorf("%v", strings.Join(errMsg, "\n"))
	}
	return nil
}

func (self *ExpectedResultSet) AssertAllMentionedExactly(times int) error {
	t := uint64(times)
	return self.AssertMentions(func(x uint64, req *push.PushRequest) bool {
		return x == t
	})
}

func (self *ExpectedResultSet) Reply(msg *CloudMessage) error {
	nrRes := 0
	defer close(msg.ResultChan)
	for _, r := range self.res {
		rs := r.getResults(self.cloud, msg)
		for _, res := range rs {
			if res != nil {
				msg.ResultChan <- res
				nrRes++
			}
		}
	}
	if nrRes == 0 {
		return fmt.Errorf("cannot find result corresponds to %+v", msg)
	}
	return nil
}

func PushCloudWorker(exp *ExpectedResultSet, t *testing.T) {
	cloud := exp.cloud
	for {
		msg, err := cloud.NextMessage()
		if err == io.EOF {
			return
		}
		if err != nil {
			t.Errorf("Error: %v", err)
			return
		}
		err = exp.Reply(msg)
		if err != nil {
			t.Errorf("%v", err)
		}
	}
}
