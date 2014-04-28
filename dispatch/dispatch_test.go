package dispatch

import (
	crand "crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/kr/pretty"
	"github.com/uniqush/uniqush-push/push"
)

func PushResultEq(a, b *push.PushResult) bool {
	if len(a.Destinations) != len(b.Destinations) {
		return false
	}
	if !reflect.DeepEqual(a.Provider, b.Provider) {
		return false
	}
	if !reflect.DeepEqual(a.Content, b.Content) {
		return false
	}
	if a.Error() != b.Error() {
		return false
	}
	for _, ad := range a.Destinations {
		found := false
		for _, bd := range b.Destinations {
			if reflect.DeepEqual(ad, bd) {
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

func PushRequestEq(a, b *push.PushRequest) bool {
	if len(a.Destinations) != len(b.Destinations) {
		return false
	}
	if !reflect.DeepEqual(a.Provider, b.Provider) {
		return false
	}
	if !reflect.DeepEqual(a.Content, b.Content) {
		return false
	}
	for _, ad := range a.Destinations {
		found := false
		for _, bd := range b.Destinations {
			if reflect.DeepEqual(ad, bd) {
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

type reqResPair struct {
	Request *push.PushRequest
	Results []*push.PushResult
}

type expectedResults struct {
	res  []*reqResPair
	lock sync.Mutex
}

func (self *expectedResults) registerPushServices() {
	pushServiceNames := make(map[string]struct{}, 10)
	for _, r := range self.res {
		req := r.Request
		provider := req.Provider
		var v struct{}
		pushServiceNames[provider.PushService()] = v
	}
	for name, _ := range pushServiceNames {
		ps := &simplePushService{
			ExpRes:          self,
			PushServiceName: name,
		}
		push.RegisterPushService(ps)
	}
}

func (self *expectedResults) dispatch(dispacher *Dispatcher) {
	for _, r := range self.res {
		req := r.Request
		notif := req.Content
		pairs := make([]*push.ProviderDeliveryPointPair, 0, len(req.Destinations))
		for _, dp := range req.Destinations {
			pair := &push.ProviderDeliveryPointPair{
				Provider:      req.Provider,
				DeliveryPoint: dp,
			}
			pairs = append(pairs, pair)
		}
		dispacher.Push(notif, pairs...)
	}
}

func (self *expectedResults) nrExpectedResults() int {
	ret := 0

	for _, r := range self.res {
		ret += len(r.Results)
	}
	return ret
}

func (self *expectedResults) appendResults(req *push.PushRequest, rs ...*push.PushResult) {
	self.lock.Lock()
	defer self.lock.Unlock()

	var res *reqResPair
	for _, r := range self.res {
		if PushRequestEq(r.Request, req) {
			res = r
		}
	}
	if res == nil {
		res = &reqResPair{
			Request: req,
		}
		self.res = append(self.res, res)
	}
	res.Results = append(res.Results, rs...)
	return
}

func (self *expectedResults) getResults(req *push.PushRequest) (res []*push.PushResult, err error) {
	if self == nil {
		err = pretty.Errorf("unexpected request: %# v", req)
		return
	}
	self.lock.Lock()
	defer self.lock.Unlock()

	for _, r := range self.res {
		if PushRequestEq(r.Request, req) {
			res = r.Results
			return
		}
	}
	res = nil
	err = pretty.Errorf("unexpected request: %# v", req)
	return
}

func (self *expectedResults) isExpectedResult(res *push.PushResult) error {
	self.lock.Lock()
	defer self.lock.Unlock()

	for _, r := range self.res {
		for _, er := range r.Results {
			if PushResultEq(er, res) {
				return nil
			}
		}
	}
	return pretty.Errorf("unable to find result: %# v", res)
}

type simplePushService struct {
	push.BasicPushService
	PushServiceName string
	ExpRes          *expectedResults
}

func testDispatcher(exp *expectedResults, t *testing.T) {
	exp.registerPushServices()
	resChan := make(chan *push.PushResult)

	dispacher := &Dispatcher{
		ResultChannel: resChan,
	}

	N := exp.nrExpectedResults()

	stop := make(chan bool)

	go func(n int) {
		for i := 0; i < n; i++ {
			fmt.Printf("receiving %v/%v res\n", i, N)
			select {
			case res := <-resChan:
				err := exp.isExpectedResult(res)
				if err != nil {
					t.Error(err)
				}
			case <-time.After(5 * time.Second):
				t.Errorf("timeout: received %v results", i)
				break
			}
		}
		stop <- true
	}(N)

	exp.dispatch(dispacher)
	<-stop
}

func (self *simplePushService) Push(
	req *push.PushRequest,
	resChan chan<- *push.PushResult,
) {
	res, err := self.ExpRes.getResults(req)
	if err != nil {
		panic(err)
	}
	for _, r := range res {
		resChan <- r
	}
	return
}

func (self *simplePushService) EmptyProvider() push.Provider {
	return &simpleProvider{}
}

func (self *simplePushService) EmptyDeliveryPoint() push.DeliveryPoint {
	return &simpleDeliveryPoint{}
}

func (self *simplePushService) Name() string {
	return self.PushServiceName
}

type simpleDeliveryPoint struct {
	DevToken        string `json:"devtoken"`
	PushServiceName string
	push.BasicDeliveryPoint
}

func (self *simpleDeliveryPoint) PushService() string {
	return self.PushServiceName
}

func (self *simpleDeliveryPoint) Provider() string {
	return ""
}

func (self *simpleDeliveryPoint) UniqId() string {
	return self.DevToken
}

func (self *simpleDeliveryPoint) PairProvider(p push.Provider) bool {
	return false
}

type simpleProvider struct {
	ApiKey          string `json:"apikey"`
	PushServiceName string
	push.BasicProvider
}

func (self *simpleProvider) UniqId() string {
	return self.ApiKey
}

func (self *simpleProvider) PushService() string {
	return self.PushServiceName
}

func seedMathRand() {
	var seed int64
	binary.Read(crand.Reader, binary.LittleEndian, &seed)
	rand.Seed(seed)
}

func randBool() bool {
	return rand.Intn(2) == 0
}

func seqStrings(N int, prefix string) []string {
	ret := make([]string, 0, N)
	for i := 0; i < N; i++ {
		ret = append(ret, fmt.Sprintf("%v-%v", prefix, i))
	}
	return ret
}

func randomString() string {
	var d [16]byte
	io.ReadFull(crand.Reader, d[:])
	return fmt.Sprintf("%x-%v", time.Now().Unix(), base64.URLEncoding.EncodeToString(d[:]))
}

func TestDispatcher(t *testing.T) {
	exp := &expectedResults{}
	N := 5
	M := 20
	P := 10
	pushServiceNames := seqStrings(N, "ps")
	devtokens := seqStrings(M, "dp")
	apikeys := seqStrings(P, "provider")
	seedMathRand()
	reqs := make([]*push.PushRequest, 0, M*N*P)

	for _, ps := range pushServiceNames {
		for _, psp := range apikeys {
			nrMsgs := rand.Intn(10)
			for i := 0; i < nrMsgs; i++ {
				provider := &simpleProvider{
					ApiKey:          psp,
					PushServiceName: ps,
				}
				data := map[string]string{
					"ps":   ps,
					"psp":  psp,
					"time": time.Now().String(),
					"rand": randomString(),
				}
				notif := &push.Notification{
					Data: data,
				}
				req := &push.PushRequest{
					Provider: provider,
					Content:  notif,
				}
				for _, dp := range devtokens {
					if randBool() {
						sdp := &simpleDeliveryPoint{
							DevToken:        dp,
							PushServiceName: ps,
						}
						req.Destinations = append(req.Destinations, sdp)
					}
				}
				if len(req.Destinations) > 0 {
					reqs = append(reqs, req)
				}
			}
		}
	}

	N = 0
	for _, req := range reqs {
		if randBool() {
			res := req.AllSuccess()
			N++
			exp.appendResults(req, res)
		} else {
			for _, dp := range req.Destinations {
				if randBool() {
					res := req.OneSuccess(dp, randomString())
					N++
					exp.appendResults(req, res)
				} else {
					err := errors.New(randomString())
					res := req.OneError(dp, err)
					N++
					exp.appendResults(req, res)
				}
			}
		}
	}
	fmt.Printf("waiting for %v results\n", N)
	testDispatcher(exp, t)
}
