package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/uniqush/log"
	"github.com/uniqush/uniqush-push/db"
	"github.com/uniqush/uniqush-push/push"
)

// Tests for /addpsp's handling of replace=true.
//
// This is the boundary the whole feature reaches the outside world through, and
// it is three lines: recognise the flag, take it out of the request before the
// provider is built, and pass it on. The database tests exercise the replace
// itself and the acceptance test exercises the migration, but both call the
// backend directly -- so a regression in those three lines would leave the
// documented feature unreachable with every other test still green.

// silentLoggers is a full set of loggers that discard everything.
func silentLoggers() []log.Logger {
	loggers := make([]log.Logger, NumberOfLoggers)
	for i := range loggers {
		loggers[i] = log.NewLogger(io.Discard, "[test]", log.LOGLEVEL_SILENT)
	}
	return loggers
}

const addPSPTestType = "addpsptest"

var registerAddPSPTestTypeOnce sync.Once

// echoingPushServiceType copies every key it is handed into the provider's data.
//
// Deliberately indiscriminate, unlike the real push service types, which read
// the keys they know and ignore the rest. That is what makes it possible to see
// whether "replace" reached the builder: with a real type an unknown key is
// swallowed, so the test would pass whether or not the flag had been stripped.
type echoingPushServiceType struct{}

var _ push.PushServiceType = &echoingPushServiceType{}

func (t *echoingPushServiceType) Name() string { return addPSPTestType }

func (t *echoingPushServiceType) BuildPushServiceProviderFromMap(kv map[string]string, psp *push.PushServiceProvider) error {
	for key, value := range kv {
		if key == "pushservicetype" {
			continue
		}
		psp.FixedData[key] = value
	}
	return nil
}

func (t *echoingPushServiceType) BuildDeliveryPointFromMap(kv map[string]string, dp *push.DeliveryPoint) error {
	return dp.AddCommonData(kv)
}

func (t *echoingPushServiceType) Push(*push.PushServiceProvider, <-chan *push.DeliveryPoint, chan<- *push.Result, *push.Notification) {
}
func (t *echoingPushServiceType) Preview(*push.Notification) ([]byte, push.Error) { return nil, nil }
func (t *echoingPushServiceType) SetErrorReportChan(chan<- push.Error)            {}
func (t *echoingPushServiceType) SetPushServiceConfig(*push.PushServiceConfig)    {}
func (t *echoingPushServiceType) Finalize()                                       {}

// recordingDatabase is a db.PushDatabase that remembers what /addpsp asked of it.
//
// A stub rather than redis, because what is under test is one hop of argument
// passing. A real database would test the same three lines and additionally be
// able to fail for reasons that have nothing to do with them.
type recordingDatabase struct {
	mutex    sync.Mutex
	calls    int
	service  string
	provider *push.PushServiceProvider
	replace  bool
	err      error
}

func (d *recordingDatabase) AddPushServiceProviderToService(service string, psp *push.PushServiceProvider, replace bool) error {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	d.calls++
	d.service = service
	d.provider = psp
	d.replace = replace
	return d.err
}

func (d *recordingDatabase) RemovePushServiceProviderFromService(string, *push.PushServiceProvider) error {
	return nil
}
func (d *recordingDatabase) ModifyPushServiceProvider(*push.PushServiceProvider) error { return nil }
func (d *recordingDatabase) GetPushServiceProviderConfigs() ([]*push.PushServiceProvider, error) {
	return nil, nil
}
func (d *recordingDatabase) RebuildServiceSet() error { return nil }
func (d *recordingDatabase) AddDeliveryPointToService(string, string, *push.DeliveryPoint) (*push.PushServiceProvider, error) {
	return nil, nil
}
func (d *recordingDatabase) RemoveDeliveryPointFromService(string, string, *push.DeliveryPoint) error {
	return nil
}
func (d *recordingDatabase) ModifyDeliveryPoint(*push.DeliveryPoint) error { return nil }
func (d *recordingDatabase) GetPushServiceProviderDeliveryPointPairs(string, string, []string, log.Logger) ([]db.PushServiceProviderDeliveryPointPair, error) {
	return nil, nil
}
func (d *recordingDatabase) GetSubscriptions([]string, string, log.Logger) ([]map[string]string, error) {
	return nil, nil
}
func (d *recordingDatabase) CheckConsistency() (*db.ConsistencyReport, error) {
	return new(db.ConsistencyReport), nil
}
func (d *recordingDatabase) FlushCache() error { return nil }

var _ db.PushDatabase = &recordingDatabase{}

// postAddPSP drives the real HTTP handler, so the form parsing is covered too.
func postAddPSP(t *testing.T, database *recordingDatabase, form url.Values) {
	t.Helper()

	registerAddPSPTestTypeOnce.Do(func() {
		if err := push.GetPushServiceManager().RegisterPushServiceType(&echoingPushServiceType{}); err != nil {
			t.Fatalf("Could not register the test push service type: %v", err)
		}
	})

	psm := push.GetPushServiceManager()
	api := NewRestAPI(psm, silentLoggers(), "test", NewPushBackEnd(psm, database, silentLoggers()))

	request := httptest.NewRequest(http.MethodPost, AddPushServiceProviderToServiceURL, strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	api.ServeHTTP(httptest.NewRecorder(), request)
}

func addPSPForm() url.Values {
	return url.Values{
		"service":         {"restapi_test_service"},
		"pushservicetype": {addPSPTestType},
		"cert":            {"some.cert"},
	}
}

func TestAddPSPForwardsReplace(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		value    string
		expected bool
	}{
		{name: "absent", value: "", expected: false},
		{name: "true", value: "true", expected: true},
		// Only the exact string opts in. A replace is destructive of the old
		// provider, so anything ambiguous means no.
		{name: "1 is not true", value: "1", expected: false},
		{name: "True is not true", value: "True", expected: false},
		{name: "false", value: "false", expected: false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			database := new(recordingDatabase)
			form := addPSPForm()
			if testCase.value != "" {
				form.Set("replace", testCase.value)
			}

			postAddPSP(t, database, form)

			if database.calls != 1 {
				t.Fatalf("Expected /addpsp to reach the database once, got %d calls", database.calls)
			}
			if database.replace != testCase.expected {
				t.Errorf("Expected replace=%v to be forwarded, got %v", testCase.expected, database.replace)
			}
		})
	}
}

// TestAddPSPDoesNotMakeReplacePartOfTheProvider is the half that matters most.
//
// A provider's name is a hash of its fixed data, so a stray key there changes
// the name -- which is the identity every delivery point resolves against. An
// /addpsp with replace=true and one without would build two different providers
// out of the same credentials, and the flag meant to preserve subscriptions
// would be the thing that broke them.
func TestAddPSPDoesNotMakeReplacePartOfTheProvider(t *testing.T) {
	plain := new(recordingDatabase)
	postAddPSP(t, plain, addPSPForm())

	replacing := new(recordingDatabase)
	form := addPSPForm()
	form.Set("replace", "true")
	postAddPSP(t, replacing, form)

	if _, leaked := replacing.provider.FixedData["replace"]; leaked {
		t.Errorf("replace reached the provider builder: %v", replacing.provider.FixedData)
	}
	if plain.provider.Name() != replacing.provider.Name() {
		t.Errorf("The same credentials must build the same provider with or without replace: %q vs %q",
			plain.provider.Name(), replacing.provider.Name())
	}
}
