package main

import (
	"bytes"
	"fmt"
	"io"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/uniqush/uniqush-push/db"
	"github.com/uniqush/uniqush-push/push"
)

// metricsDatabaseTimeout bounds the redis part of a scrape. A scrape that
// times out loses every metric, including the push counters that show what a
// redis outage is doing; a slow redis is reported as down instead.
var metricsDatabaseTimeout = 2 * time.Second

// Values of the result label on uniqush_push_results_total.
const (
	pushResultSuccess       = "success"
	pushResultRetry         = "retry"
	pushResultDeviceRemoved = "device_removed"
	pushResultUpdated       = "updated"
	pushResultError         = "error"
)

// metrics holds the counters /metrics reports. The gauges are read from redis
// when scraped instead, so they cannot drift from what /stats says.
//
// Written by hand rather than with prometheus/client_golang, which would add
// seven modules (protobuf and procfs among them) for a few counters.
type metrics struct {
	mutex          sync.Mutex
	pushResults    map[pushResultKey]uint64
	retriesDropped map[pushTypeKey]uint64
	requests       map[string]uint64
}

type pushTypeKey struct{ service, pushServiceType string }

type pushResultKey struct {
	pushTypeKey
	result string
}

func newMetrics() *metrics {
	return &metrics{
		pushResults:    make(map[pushResultKey]uint64),
		retriesDropped: make(map[pushTypeKey]uint64),
		requests:       make(map[string]uint64),
	}
}

func pushResultOf(err error) string {
	switch err.(type) {
	case nil:
		return pushResultSuccess
	case *push.RetryError:
		return pushResultRetry
	case *push.InvalidRegistrationUpdate, *push.UnsubscribeUpdate:
		return pushResultDeviceRemoved
	case *push.DeliveryPointUpdate, *push.PushServiceProviderUpdate:
		return pushResultUpdated
	default:
		return pushResultError
	}
}

func pushTypeOf(service string, provider *push.PushServiceProvider, dp *push.DeliveryPoint) pushTypeKey {
	key := pushTypeKey{service: service, pushServiceType: "unknown"}
	if provider != nil {
		key.pushServiceType = provider.PushServiceName()
	} else if dp != nil {
		key.pushServiceType = dp.PushServiceName()
	}
	return key
}

func (m *metrics) countPushResult(key pushTypeKey, err error) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.pushResults[pushResultKey{key, pushResultOf(err)}]++
}

func (m *metrics) countRetryDropped(key pushTypeKey) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.retriesDropped[key]++
}

// countRequest is only reached for the paths Run registers, so a scanner
// probing for URLs cannot grow the series.
func (m *metrics) countRequest(path string) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.requests[path]++
}

// writeMetrics writes everything in the Prometheus text exposition format.
func (api *RestAPI) writeMetrics(w io.Writer) {
	// Rendered into a buffer, so a scraper that stops reading cannot hold the
	// lock every push and request has to take.
	var counters bytes.Buffer
	e := &metricsEncoder{w: &counters}
	m := api.backend.metrics

	m.mutex.Lock()
	e.family("uniqush_push_results_total", "counter",
		"Results reported by push services, usually one per device per attempt, by outcome.")
	for _, k := range sortedKeys(m.pushResults, func(k pushResultKey) string { return k.service + "\x00" + k.pushServiceType + "\x00" + k.result }) {
		e.sample("uniqush_push_results_total", m.pushResults[k], "service", k.service, "push_service_type", k.pushServiceType, "result", k.result)
	}
	e.family("uniqush_push_retries_abandoned_total", "counter",
		"Notifications dropped for a device after retrying for as long as allowed.")
	for _, k := range sortedKeys(m.retriesDropped, func(k pushTypeKey) string { return k.service + "\x00" + k.pushServiceType }) {
		e.sample("uniqush_push_retries_abandoned_total", m.retriesDropped[k], "service", k.service, "push_service_type", k.pushServiceType)
	}
	e.family("uniqush_http_requests_total", "counter", "HTTP requests, by path.")
	for _, path := range sortedKeys(m.requests, func(k string) string { return k }) {
		e.sample("uniqush_http_requests_total", m.requests[path], "path", path)
	}
	m.mutex.Unlock()
	if _, err := counters.WriteTo(w); err != nil {
		return
	}

	e = &metricsEncoder{w: w}
	e.family("uniqush_build_info", "gauge", "Always 1, labeled with the running version.")
	e.sample("uniqush_build_info", 1, "version", strings.TrimPrefix(api.version, "uniqush-push "))
	e.family("go_goroutines", "gauge", "Number of goroutines that currently exist.")
	e.sample("go_goroutines", runtime.NumGoroutine())

	type snapshot struct {
		up    bool
		stats map[string]*db.ServiceStats
	}
	// Buffered, so a call that answers after the timeout can still finish.
	done := make(chan snapshot, 1)
	go func() {
		var s snapshot
		if s.up = api.backend.PingDatabase() == nil; s.up {
			// Left out, rather than reported as zero, while the subscriber
			// index is incomplete -- for the reason /stats refuses then.
			if stats, err := api.backend.SubscriberStats(nil, nil); err == nil {
				s.stats = stats
			}
		}
		done <- s
	}()
	var s snapshot
	select {
	case s = <-done:
	case <-time.After(metricsDatabaseTimeout):
	}

	up := 0
	if s.up {
		up = 1
	}
	e.family("uniqush_database_up", "gauge", "1 if redis answered within this scrape, 0 if not.")
	e.sample("uniqush_database_up", up)
	if s.stats == nil {
		return
	}
	stats := s.stats
	services := sortedKeys(stats, func(k string) string { return k })
	e.family("uniqush_subscribers", "gauge", "Subscribers with at least one device, by service.")
	for _, service := range services {
		e.sample("uniqush_subscribers", stats[service].Subscribers, "service", service)
	}
	e.family("uniqush_delivery_points", "gauge", "Devices, by service and push service type.")
	for _, service := range services {
		dps := stats[service].DeliveryPoints
		for _, pushServiceType := range sortedKeys(dps, func(k string) string { return k }) {
			e.sample("uniqush_delivery_points", dps[pushServiceType], "service", service, "push_service_type", pushServiceType)
		}
	}
}

func sortedKeys[K comparable, V any](m map[K]V, order func(K) string) []K {
	keys := make([]K, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return order(keys[i]) < order(keys[j]) })
	return keys
}

type metricsEncoder struct{ w io.Writer }

func (e *metricsEncoder) family(name, kind, help string) {
	fmt.Fprintf(e.w, "# HELP %s %s\n# TYPE %s %s\n", name, help, name, kind)
}

// sample writes one line; labels alternate name and value.
func (e *metricsEncoder) sample(name string, value any, labels ...string) {
	var b strings.Builder
	b.WriteString(name)
	for i := 0; i+1 < len(labels); i += 2 {
		if i == 0 {
			b.WriteByte('{')
		} else {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%s=\"%s\"", labels[i], escapeLabelValue(labels[i+1]))
	}
	if len(labels) > 0 {
		b.WriteByte('}')
	}
	fmt.Fprintf(e.w, "%s %v\n", b.String(), value)
}

var labelValueEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)

func escapeLabelValue(v string) string { return labelValueEscaper.Replace(v) }
