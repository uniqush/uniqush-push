/*
 * Copyright 2013-2026 Uniqush Contributors.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package db

import (
	"fmt"
	"sort"
	"strings"

	"github.com/uniqush/uniqush-push/push"
)

// CheckConsistency scans redis and reports what does not add up.
//
// This lives with the key layout rather than in pushdb.go on purpose: every
// check here is a statement about how the keys relate, and splitting it from
// the constants that define them would mean two files to keep in step.
//
// Every keyspace walk here uses SCAN. KEYS would hold the redis event loop for
// the length of the walk, and a database big enough to be worth checking is
// exactly the one where that stalls every push.
func (r *PushRedisDB) CheckConsistency() (*ConsistencyReport, error) {
	report := new(ConsistencyReport)

	providers, err := r.loadAllProviders(report)
	if err != nil {
		return nil, err
	}
	if err := r.checkServiceProviders(report, providers); err != nil {
		return nil, err
	}
	if err := r.checkDeliveryPointBindings(report, providers); err != nil {
		return nil, err
	}
	if err := r.checkSubscriberSets(report); err != nil {
		return nil, err
	}
	if err := r.checkCounters(report); err != nil {
		return nil, err
	}

	// Stable order, so two runs over an unchanged database produce identical
	// output and a repair can be judged by diffing them.
	sort.Slice(report.Problems, func(i, j int) bool {
		left, right := report.Problems[i], report.Problems[j]
		if left.Kind != right.Kind {
			return left.Kind < right.Kind
		}
		if left.Service != right.Service {
			return left.Service < right.Service
		}
		return left.Subject < right.Subject
	})
	return report, nil
}

// scanKeysCount is the COUNT hint on each SCAN: fewer round trips against less
// work per call for redis. Nobody should need to tune it.
const scanKeysCount = 500

// scanKeys walks every key matching pattern, handing each page to visit.
//
// Streaming rather than returning the keys, because two of the patterns walked
// here -- one key per binding, one per counter -- have a key per device. A
// database big enough to be worth checking is exactly one where holding that
// list, plus a set to deduplicate it, is a way to run the server out of memory:
// a diagnostic that kills the process it was run to diagnose.
//
// SCAN trades KEYS's single long stall for a series of short ones, and gives up
// the snapshot in exchange. A key added or removed mid-walk may or may not
// appear; a key present throughout appears at least once, and can appear twice
// if redis resizes its table underneath the cursor. Neither is worth preventing
// here. Every finding is re-checked against the keys it names before it is
// reported, so a key that has gone raises no alarm, and a key seen twice is
// merely checked twice -- which can nudge a total up by one, on a report whose
// actionable content is its problem counts. Preventing that would cost the
// keyspace-sized set this exists to avoid.
func (r *PushRedisDB) scanKeys(pattern string, visit func(page []string) error) error {
	var cursor uint64
	for {
		page, next, err := r.client.Scan(r.ctx, cursor, pattern, scanKeysCount).Result()
		if err != nil {
			return err
		}
		if len(page) > 0 {
			if err := visit(page); err != nil {
				return err
			}
		}
		// A zero cursor means the walk is complete. It is the only termination
		// condition: an empty page is normal, because SCAN's COUNT bounds the
		// work done rather than the rows returned.
		if next == 0 {
			return nil
		}
		cursor = next
	}
}

func (r *PushRedisDB) report(report *ConsistencyReport, kind, service, subject, format string, args ...interface{}) {
	report.add(ConsistencyProblem{
		Kind:    kind,
		Service: service,
		Subject: subject,
		Detail:  fmt.Sprintf(format, args...),
	})
}

// loadAllProviders reads every provider record, keyed by name.
//
// The one thing here held in memory whole, and deliberately: a provider per
// service per push service type is tens of entries on a large deployment, not
// one per device, and every check below needs to look providers up by name.
func (r *PushRedisDB) loadAllProviders(report *ConsistencyReport) (map[string]*push.PushServiceProvider, error) {
	providers := make(map[string]*push.PushServiceProvider)
	err := r.scanKeys(PushServiceProviderPrefix+"*", func(page []string) error {
		for _, key := range page {
			name := strings.TrimPrefix(key, PushServiceProviderPrefix)
			psp, e := r.GetPushServiceProvider(name)
			if e != nil {
				if isErrCausedByMissingKey(e) {
					// Deleted between the scan and this read. Nothing to report.
					continue
				}
				// A record that exists but will not unserialize. Reported rather
				// than fatal: one corrupt provider should not stop the scan that
				// would have told you about the other nine.
				r.report(report, ProblemDanglingProvider, "", name, "the provider record could not be read: %v", e)
				continue
			}
			if psp == nil {
				continue
			}
			providers[name] = psp
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("could not list push service providers: %w", err)
	}
	report.Providers = len(providers)
	return providers, nil
}

// checkServiceProviders finds the duplicates that make the derivation ambiguous.
func (r *PushRedisDB) checkServiceProviders(report *ConsistencyReport, providers map[string]*push.PushServiceProvider) error {
	// Bounded by the number of provider names, which loadAllProviders has
	// already established is small.
	listed := make(map[string]bool, len(providers))

	err := r.scanKeys(ServiceToPushServiceProvidersPrefix+"*", func(page []string) error {
		for _, key := range page {
			report.Services++
			service := strings.TrimPrefix(key, ServiceToPushServiceProvidersPrefix)
			names, e := r.client.SMembers(r.ctx, key).Result()
			if e != nil {
				return fmt.Errorf("could not read the providers of service %q: %w", service, e)
			}

			byType := make(map[string][]string, len(names))
			for _, name := range names {
				listed[name] = true
				psp, known := providers[name]
				if !known {
					r.report(report, ProblemDanglingProvider, service, name,
						"the service lists this provider, but it has no record; /rmpsp removes both, so this is an interrupted write")
					continue
				}
				byType[psp.PushServiceName()] = append(byType[psp.PushServiceName()], name)
			}

			for pushServiceType, sharing := range byType {
				if len(sharing) < 2 {
					continue
				}
				sort.Strings(sharing)
				r.report(report, ProblemDuplicateProvider, service, pushServiceType,
					"%d providers of type %s (%s). A delivery point not covered by a stored binding resolves "+
						"nondeterministically. Remove all but one with /rmpsp.",
					len(sharing), pushServiceType, strings.Join(sharing, ", "))
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("could not check service provider sets: %w", err)
	}

	// The mirror image of a dangling provider: a record no service points at.
	// Deliberately reported last, so both halves of the same inconsistency read
	// together.
	orphaned := make([]string, 0)
	for name := range providers {
		if !listed[name] {
			orphaned = append(orphaned, name)
		}
	}
	sort.Strings(orphaned)
	for _, name := range orphaned {
		r.report(report, ProblemOrphanedProvider, providers[name].FixedData["service"], name,
			"this provider record is in no service's provider set, so nothing can push through it. "+
				"Remove it with /rmpsp, or re-add it with /addpsp if it was meant to be in use.")
	}
	return nil
}

// checkDeliveryPointBindings inspects the srv.dp-2-psp index.
//
// Neither finding is urgent -- the binding is still authoritative today -- but
// the two counts are what say whether it is safe to stop reading.
func (r *PushRedisDB) checkDeliveryPointBindings(report *ConsistencyReport, providers map[string]*push.PushServiceProvider) error {
	// Providers grouped by service and type, to compare against what the
	// derivation would choose.
	byServiceAndType := make(map[string]map[string][]string)
	for name, psp := range providers {
		service := psp.FixedData["service"]
		if service == "" {
			continue
		}
		if byServiceAndType[service] == nil {
			byServiceAndType[service] = make(map[string][]string)
		}
		byServiceAndType[service][psp.PushServiceName()] = append(byServiceAndType[service][psp.PushServiceName()], name)
	}

	err := r.scanKeys(ServiceDeliveryPointToPushServiceProviderPrefix+"*", func(page []string) error {
		for _, key := range page {
			report.Bindings++
			rest := strings.TrimPrefix(key, ServiceDeliveryPointToPushServiceProviderPrefix)
			// A delivery point name is "<type>:<hash>" and so contains a colon; a
			// service name cannot, so the first colon is the separator.
			parts := strings.SplitN(rest, ":", 2)
			if len(parts) != 2 {
				continue
			}
			service, dpName := parts[0], parts[1]

			boundTo, e := r.client.Get(r.ctx, key).Result()
			if e != nil {
				if isErrCausedByMissingKey(e) {
					// Unsubscribed between the scan and this read. Expected on a
					// live database, and there is nothing left to check.
					continue
				}
				// Anything else means this read failed, not that the binding is
				// absent -- and a report that swallowed it would say the
				// database is healthy on the strength of a scan that did not
				// finish. An operator running this during an incident has to be
				// able to believe a clean answer.
				return fmt.Errorf("could not read the binding %q: %w", key, e)
			}
			if _, exists := providers[boundTo]; !exists {
				r.report(report, ProblemStaleBinding, service, dpName,
					"bound to provider %q, which no longer exists. Harmless: the provider is derived from the "+
						"service and the device's push service type now, and this binding is only a tie-breaker.",
					boundTo)
				continue
			}

			// Which provider would the derivation pick? Only meaningful when
			// exactly one candidate exists; otherwise the binding is the tie-break
			// and cannot disagree with itself.
			dpType := deliveryPointTypeFromName(dpName)
			candidates := byServiceAndType[service][dpType]
			if len(candidates) == 1 && candidates[0] != boundTo {
				r.report(report, ProblemBindingDisagrees, service, dpName,
					"bound to provider %q, but the derivation picks %q. The derivation wins today; "+
						"this becomes final when the binding index is retired.", boundTo, candidates[0])
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("could not check delivery point bindings: %w", err)
	}
	return nil
}

// deliveryPointTypeFromName reads the push service type out of a delivery point
// name, which is "<pushservicetype>:<sha1 of its fixed data>".
func deliveryPointTypeFromName(name string) string {
	if index := strings.Index(name, ":"); index > 0 {
		return name[:index]
	}
	return ""
}

// checkSubscriberSets finds delivery point names with no record behind them.
func (r *PushRedisDB) checkSubscriberSets(report *ConsistencyReport) error {
	err := r.scanKeys(ServiceSubscriberToDeliveryPointsPrefix+"*", func(page []string) error {
		for _, key := range page {
			rest := strings.TrimPrefix(key, ServiceSubscriberToDeliveryPointsPrefix)
			// Neither a service nor a subscriber name may contain a colon.
			parts := strings.SplitN(rest, ":", 2)
			if len(parts) != 2 {
				continue
			}
			service, subscriber := parts[0], parts[1]

			names, e := r.client.SMembers(r.ctx, key).Result()
			if e != nil {
				return fmt.Errorf("could not read the delivery points of %q: %w", key, e)
			}
			for _, dpName := range names {
				// Counted per membership, with no set of names seen so far. A
				// delivery point's name hashes its service and subscriber along
				// with the device token, and those are the two halves of this
				// key, so one name cannot appear under two subscribers. The set
				// that used to deduplicate this held an entry per device.
				report.DeliveryPoints++

				exists, e := r.client.Exists(r.ctx, DeliveryPointPrefix+dpName).Result()
				if e != nil {
					return fmt.Errorf("could not check delivery point %q: %w", dpName, e)
				}
				if exists == 0 {
					r.report(report, ProblemOrphanedDeliveryPoint, service, dpName,
						"subscriber %q lists this delivery point, but it has no record. "+
							"It is removed automatically the next time that subscriber is read.", subscriber)
				}
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("could not check subscriber sets: %w", err)
	}
	return nil
}

// checkCounters finds refcounts left behind by the old read path.
func (r *PushRedisDB) checkCounters(report *ConsistencyReport) error {
	err := r.scanKeys(DeliveryPointCounterPrefix+"*", func(page []string) error {
		for _, key := range page {
			dpName := strings.TrimPrefix(key, DeliveryPointCounterPrefix)
			exists, e := r.client.Exists(r.ctx, DeliveryPointPrefix+dpName).Result()
			if e != nil {
				return fmt.Errorf("could not check delivery point %q: %w", dpName, e)
			}
			if exists == 0 {
				r.report(report, ProblemLeakedCounter, "", dpName,
					"a subscriber counter with no delivery point behind it, left by a read that deleted the "+
						"record and not the counter. Safe to delete.")
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("could not check delivery point counters: %w", err)
	}
	return nil
}
