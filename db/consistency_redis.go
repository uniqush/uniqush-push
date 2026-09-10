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
// Every keyspace walk here goes through scanKeys, and takes SCAN's duplicates
// rather than paying a keyspace-sized set to remove them: every finding is
// re-checked against the keys it names before it is reported, so a key that has
// gone raises no alarm and a key seen twice is merely checked twice. That can
// nudge a total up by one, on a report whose actionable content is its problem
// counts.
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
	if err := r.checkDeliveryPointRecords(report); err != nil {
		return nil, err
	}
	if err := r.checkSubscriberIndex(report); err != nil {
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
			if len(names) > 0 {
				if e := r.checkSubscriberIsIndexed(report, service, subscriber); e != nil {
					return e
				}
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

				pushServiceType := deliveryPointTypeFromName(dpName)
				listed, e := r.client.SIsMember(r.ctx, typeDeviceSetKey(service, pushServiceType), dpName).Result()
				if e != nil {
					return fmt.Errorf("could not check the %s delivery points of service %q: %w", pushServiceType, service, e)
				}
				if !listed {
					r.report(report, ProblemMissingIndexEntry, service, dpName,
						"subscriber %q lists this delivery point, but it is not in the service's set of %s "+
							"delivery points, so /stats undercounts it. Run /rebuildsubscriberindex.",
						subscriber, pushServiceType)
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

// checkDeliveryPointRecords finds records that no subscriber's set names.
//
// The other direction from checkSubscriberSets, and the one that covers the
// window in a subscribe: AddDeliveryPointToService writes the record, then the
// set entry, then the provider binding, so an interruption after the first
// write leaves a record nothing refers to. Nothing collects those -- a read
// starts from the subscriber's set, so it never meets them -- and they are
// invisible to /subscriptions and to /unsubscribe alike.
//
// The check costs no memory beyond one record at a time, because a delivery
// point already carries the service and subscriber it belongs to in its fixed
// data. The alternative -- collecting every name every subscriber set holds and
// subtracting -- would be a set with an entry per device on the one database
// where this is worth running.
func (r *PushRedisDB) checkDeliveryPointRecords(report *ConsistencyReport) error {
	err := r.scanKeys(DeliveryPointPrefix+"*", func(page []string) error {
		for _, key := range page {
			dpName := strings.TrimPrefix(key, DeliveryPointPrefix)
			dp, e := r.GetDeliveryPoint(dpName)
			if e != nil {
				if isErrCausedByMissingKey(e) {
					// Unsubscribed between the scan and this read. Expected on a
					// live database, and there is nothing left to check.
					continue
				}
				// A record that exists but will not unserialize. It cannot be
				// pushed to and it names no subscriber to check it against, so it
				// is dead either way; reported rather than fatal, so that one
				// corrupt record does not hide the rest of the walk.
				r.report(report, ProblemUnreferencedDeliveryPoint, "", dpName,
					"this delivery point record could not be read, so nothing can push to it: %v", e)
				continue
			}
			if dp == nil {
				continue
			}

			service, subscriber := dp.FixedData["service"], dp.FixedData["subscriber"]
			if service == "" || subscriber == "" {
				r.report(report, ProblemUnreferencedDeliveryPoint, service, dpName,
					"this delivery point record names no service or subscriber, so nothing can push to it")
				continue
			}

			listed, e := r.client.SIsMember(r.ctx, deviceSetKey(service, subscriber), dpName).Result()
			if e != nil {
				return fmt.Errorf("could not check whether %q lists delivery point %q: %w",
					deviceSetKey(service, subscriber), dpName, e)
			}
			if !listed {
				r.report(report, ProblemUnreferencedDeliveryPoint, service, dpName,
					"subscriber %q does not list this delivery point, so nothing will push to it or remove it. "+
						"An interrupted /subscribe leaves this. Re-subscribing the device adopts the record; "+
						"otherwise it is safe to delete.", subscriber)
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("could not check delivery point records: %w", err)
	}
	return nil
}

// checkSubscriberIsIndexed checks one subscriber against their service's index.
//
// Split out because it is asked once per subscriber set, from the middle of a
// walk that is already three levels deep.
func (r *PushRedisDB) checkSubscriberIsIndexed(report *ConsistencyReport, service, subscriber string) error {
	_, err := r.client.ZScore(r.ctx, subscriberIndexKey(service), subscriber).Result()
	if err == nil {
		return nil
	}
	if !isErrCausedByMissingKey(err) {
		return fmt.Errorf("could not check whether service %q indexes subscriber %q: %w", service, subscriber, err)
	}
	r.report(report, ProblemMissingIndexEntry, service, subscriber,
		"this subscriber has delivery points but is not in the service's subscriber index, so a wildcard "+
			"push would miss them and /stats undercounts. Run /rebuildsubscriberindex.")
	return nil
}

// checkSubscriberIndex reads the index the other way round: entries with
// nothing behind them.
//
// A stale entry is not a delivery failure -- a wildcard push resolves each
// subscriber's devices before sending -- but it inflates every count /stats
// reports, which is the one thing the index is there to make cheap.
//
// This is also where the marker is checked, because "the index has not been
// built" is the finding that explains all the others: on such a database every
// subscriber written before the upgrade is missing from it, and reporting a
// million missing entries instead of one cause would be useless.
func (r *PushRedisDB) checkSubscriberIndex(report *ConsistencyReport) error {
	built, err := r.subscriberIndexBuilt()
	if err != nil {
		return err
	}
	if !built {
		r.report(report, ProblemIndexNotBuilt, "", "",
			"the subscriber index has never been rebuilt over this database, so it covers only what has been "+
				"written since. Wildcard pushes fall back to a full keyspace scan, which is slow, and /stats "+
				"refuses to answer. Run /rebuildsubscriberindex once.")
	}

	if err := r.checkIndexedSubscribers(report); err != nil {
		return err
	}
	return r.checkIndexedDeliveryPoints(report)
}

// checkIndexedSubscribers finds indexed subscribers with no devices left.
func (r *PushRedisDB) checkIndexedSubscribers(report *ConsistencyReport) error {
	err := r.scanKeys(ServiceToSubscribersPrefix+"*", func(page []string) error {
		for _, key := range page {
			service := strings.TrimPrefix(key, ServiceToSubscribersPrefix)
			// Streamed rather than read whole: this index has a member per
			// subscriber, which on the database where the check is worth running
			// is the largest thing in it.
			e := r.scanSubscriberIndex(service, "*", func(subscriber string) error {
				report.Subscribers++
				devices, e := r.client.SCard(r.ctx, deviceSetKey(service, subscriber)).Result()
				if e != nil {
					return fmt.Errorf("could not count the delivery points of %q in service %q: %w", subscriber, service, e)
				}
				if devices == 0 {
					r.report(report, ProblemStaleIndexEntry, service, subscriber,
						"the subscriber index lists this subscriber, who has no delivery points, so /stats "+
							"overcounts. Run /rebuildsubscriberindex.")
				}
				return nil
			})
			if e != nil {
				return e
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("could not check the subscriber indexes: %w", err)
	}
	return nil
}

// checkIndexedDeliveryPoints finds per-type device sets naming devices that are
// no longer there.
//
// Only the record's existence is checked. A record that exists but whose
// subscriber does not list it is already reported, from the other side, as an
// unreferenced delivery point.
func (r *PushRedisDB) checkIndexedDeliveryPoints(report *ConsistencyReport) error {
	err := r.scanKeys(ServiceTypeToDeliveryPointsPrefix+"*", func(page []string) error {
		for _, key := range page {
			rest := strings.TrimPrefix(key, ServiceTypeToDeliveryPointsPrefix)
			// Neither a service name nor a push service type may contain a colon.
			parts := strings.SplitN(rest, ":", 2)
			if len(parts) != 2 {
				continue
			}
			service, pushServiceType := parts[0], parts[1]

			e := r.scanSetMembers(key, func(dpName string) error {
				exists, e := r.client.Exists(r.ctx, DeliveryPointPrefix+dpName).Result()
				if e != nil {
					return fmt.Errorf("could not check delivery point %q: %w", dpName, e)
				}
				if exists == 0 {
					r.report(report, ProblemStaleIndexEntry, service, dpName,
						"the service's set of %s delivery points names this one, which has no record, so /stats "+
							"overcounts. Run /rebuildsubscriberindex.", pushServiceType)
				}
				return nil
			})
			if e != nil {
				return e
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("could not check the per-type delivery point sets: %w", err)
	}
	return nil
}

// scanSetMembers walks one set a page at a time, handing each member to visit.
//
// SMEMBERS would be one round trip instead of several, and would hold every
// member of the set in memory at once. The sets walked here have a member per
// subscriber or per device, which is exactly the size the streaming in scanKeys
// exists to avoid.
//
// SSCAN can return a member twice, for the same reason SCAN can return a key
// twice. Every visitor here re-checks what it is told against redis before
// reporting anything, so a repeat costs a second check rather than a second
// finding -- with the same caveat as CheckConsistency's walks: a count can be
// nudged up by one.
func (r *PushRedisDB) scanSetMembers(key string, visit func(member string) error) error {
	var cursor uint64
	for {
		page, next, err := r.client.SScan(r.ctx, key, cursor, "*", scanKeysCount).Result()
		if err != nil {
			return fmt.Errorf("could not scan the set %q: %w", key, err)
		}
		for _, member := range page {
			if err := visit(member); err != nil {
				return err
			}
		}
		if next == 0 {
			return nil
		}
		cursor = next
	}
}

// checkCounters finds the refcounts an older uniqush wrote.
//
// Every one of them is debris now. Subscribe and unsubscribe are redis scripts
// that never touch these keys, and the count was only ever 0 or 1 in the first
// place: a delivery point's name hashes its service and subscriber, so it
// belongs to exactly one subscription.
func (r *PushRedisDB) checkCounters(report *ConsistencyReport) error {
	err := r.scanKeys(DeliveryPointCounterPrefix+"*", func(page []string) error {
		for _, key := range page {
			dpName := strings.TrimPrefix(key, DeliveryPointCounterPrefix)
			r.report(report, ProblemLeakedCounter, "", dpName,
				"a subscriber counter, which nothing has written since subscribe and unsubscribe became "+
					"redis scripts. It is read by nothing and safe to delete.")
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("could not check delivery point counters: %w", err)
	}
	return nil
}
