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
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/uniqush/uniqush-push/log"
	"github.com/uniqush/uniqush-push/push"
)

// ErrSubscriberIndexNotBuilt is returned by the operations that can only answer
// from the subscriber index, rather than have them answer wrongly.
//
// /stats is the one that matters: counting an index that covers only the
// subscribers who happen to have re-subscribed since the upgrade would report a
// service with a million devices as having eleven, and a dashboard has no way
// to tell that apart from a service that really has eleven.
var ErrSubscriberIndexNotBuilt = errors.New("the subscriber index has not been built; run /rebuildsubscriberindex once against this database")

// subscriberIndexBuilt reports whether the subscriber index covers the whole
// database, rather than only what has been written since it was introduced.
//
// The question cannot be answered by looking for srv-2-sub:<service>. Redis
// deletes a sorted set when its last member goes, so the key is absent on a
// database that has never been indexed -- and the first /subscribe after an
// upgrade recreates it holding exactly one subscriber. A read path keyed on the
// key's presence would switch itself on at that moment, against an index that
// covers one subscriber out of however many, and a wildcard push would then
// silently miss all the rest. So the signal is an explicit marker, written only
// by a completed rebuild or by a database that had nothing to rebuild.
func (r *PushRedisDB) subscriberIndexBuilt() (bool, error) {
	if r.indexBuilt.Load() {
		return true, nil
	}
	count, err := r.client.Exists(r.ctx, SubscriberIndexBuiltKey).Result()
	if err != nil {
		return false, fmt.Errorf("could not check whether the subscriber index is built: %w", err)
	}
	if count == 0 {
		return false, nil
	}
	r.indexBuilt.Store(true)
	return true, nil
}

// markSubscriberIndexBuilt records that the index covers the whole database.
//
// The value is the time it was built, which is for whoever is reading the
// database by hand; nothing parses it. Nothing deletes this key either: a
// restore from a snapshot taken before the index existed will not carry it, and
// that is the right answer, because such a database does need rebuilding.
func (r *PushRedisDB) markSubscriberIndexBuilt() error {
	value := strconv.FormatInt(time.Now().Unix(), 10)
	if err := r.client.Set(r.ctx, SubscriberIndexBuiltKey, value, 0).Err(); err != nil {
		return fmt.Errorf("could not record that the subscriber index is built: %w", err)
	}
	r.indexBuilt.Store(true)
	return nil
}

// PrepareSubscriberIndex settles the subscriber index once, at startup.
//
// A database with nothing in it has nothing to rebuild, and requiring a call to
// /rebuildsubscriberindex before wildcards work on a brand new installation
// would be a rite of passage rather than a safeguard. So an empty database is
// marked as built here.
//
// Anything else that is not marked gets the same line a wildcard push logs. An
// operator who never sends a wildcard push would otherwise find out that their
// /stats is refusing to answer only by calling it.
func (r *PushRedisDB) PrepareSubscriberIndex(logger log.Logger) error {
	logger = orDiscard(logger)

	built, err := r.subscriberIndexBuilt()
	if err != nil {
		return err
	}
	if built {
		return nil
	}

	size, err := r.client.DBSize(r.ctx).Result()
	if err != nil {
		return fmt.Errorf("could not measure the database: %w", err)
	}
	if size == 0 {
		return r.markSubscriberIndexBuilt()
	}

	// Endpoint names spelled out, as everything else in this package that tells
	// an operator what to do does. They are what somebody would type.
	logger.Errorf("The subscriber index has not been built. Wildcard pushes still work, over a full SCAN that " +
		"is slow on a large database, and /stats refuses to answer. Run /rebuildsubscriberindex once against " +
		"this database to fix it.")
	return nil
}

// scanSubscriberIndex walks a service's subscriber index, handing each name
// matching the glob pattern to visit.
//
// ZSCAN rather than ZRANGE, for the same reason every keyspace walk here uses
// SCAN: the index has a member per subscriber, and reading it whole means
// holding all of them, on the databases where doing so hurts most. MATCH is
// applied by redis with the same glob syntax KEYS used, which is what lets a
// wildcard push keep matching exactly the subscribers it always did.
//
// A member can come back twice, as with any SCAN. Every caller here either
// re-checks what it is handed or deduplicates it.
func (r *PushRedisDB) scanSubscriberIndex(srv, match string, visit func(subscriber string) error) error {
	key := subscriberIndexKey(srv)
	var cursor uint64
	for {
		page, next, err := r.client.ZScan(r.ctx, key, cursor, match, scanKeysCount).Result()
		if err != nil {
			return fmt.Errorf("could not scan the subscriber index of service %q: %w", srv, err)
		}
		// ZSCAN returns members and their scores flattened into one list.
		for i := 0; i < len(page); i += 2 {
			if err := visit(page[i]); err != nil {
				return err
			}
		}
		if next == 0 {
			return nil
		}
		cursor = next
	}
}

// RebuildSubscriberIndex rebuilds both index types from the subscriber sets,
// which are the source of truth, and then records that the index is complete.
//
// Idempotent, and safe to run against a live server. Each service's index is
// built under a staging name and renamed over the live key, so a reader sees
// the old index or the new one and never a partial one. The live scripts keep
// writing to the live keys throughout, so a subscription made during the walk
// is only lost if it lands between the walk visiting that subscriber and the
// rename -- and running /checkdb afterwards names the handful that did.
//
// This is a separate operation rather than something /checkdb does, matching
// /rebuildserviceset: a repair that runs unattended against a database nobody
// has looked at yet is how a consistency check turns into an outage.
func (r *PushRedisDB) RebuildSubscriberIndex() error {
	if err := r.clearSubscriberIndexStaging(); err != nil {
		return err
	}

	// One entry per service, so bounded by the number of services rather than
	// by anything per subscriber or per device.
	staged := make(map[string]bool)
	err := r.scanKeys(ServiceSubscriberToDeliveryPointsPrefix+"*", func(page []string) error {
		for _, key := range page {
			rest := strings.TrimPrefix(key, ServiceSubscriberToDeliveryPointsPrefix)
			// Neither a service nor a subscriber name may contain a colon.
			parts := strings.SplitN(rest, ":", 2)
			if len(parts) != 2 {
				continue
			}
			if err := r.stageSubscriber(parts[0], parts[1], staged); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("could not walk the subscriber sets: %w", err)
	}

	if err := r.publishSubscriberIndexStaging(staged); err != nil {
		return err
	}
	return r.markSubscriberIndexBuilt()
}

// clearSubscriberIndexStaging removes what an interrupted rebuild left.
//
// Without this a second run would add its members to the first run's leftovers,
// so a subscriber who unsubscribed in between would survive the rebuild that
// was meant to forget them -- which is to say the rebuild would not be
// idempotent, which is the one property it has to have.
func (r *PushRedisDB) clearSubscriberIndexStaging() error {
	err := r.scanKeys(SubscriberIndexStagingPrefix+"*", func(page []string) error {
		if err := r.client.Del(r.ctx, page...).Err(); err != nil {
			return fmt.Errorf("could not clear a partly built index: %w", err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("could not clear the index staging area: %w", err)
	}
	return nil
}

// stageSubscriber writes one subscriber's index entries to the staging keys.
//
// A subscriber's devices are read whole, which is the one thing here that is
// not streamed: a subscriber has a handful of devices, and the write below
// wants them grouped by push service type anyway.
func (r *PushRedisDB) stageSubscriber(service, subscriber string, staged map[string]bool) error {
	names, err := r.client.SMembers(r.ctx, deviceSetKey(service, subscriber)).Result()
	if err != nil {
		return fmt.Errorf("could not read the delivery points of %q in service %q: %w", subscriber, service, err)
	}
	if len(names) == 0 {
		// Redis deletes an empty set, so this is a subscriber who unsubscribed
		// between the scan and this read. They belong in no index.
		return nil
	}

	byType := make(map[string][]interface{}, 2)
	for _, dpName := range names {
		pushServiceType := deliveryPointTypeFromName(dpName)
		byType[pushServiceType] = append(byType[pushServiceType], dpName)
	}
	for pushServiceType, dpNames := range byType {
		key := SubscriberIndexStagingPrefix + typeDeviceSetKey(service, pushServiceType)
		if err := r.client.SAdd(r.ctx, key, dpNames...).Err(); err != nil {
			return fmt.Errorf("could not stage the %s delivery points of service %q: %w", pushServiceType, service, err)
		}
		staged[key] = true
	}

	key := SubscriberIndexStagingPrefix + subscriberIndexKey(service)
	score := r.subscriberScore(names)
	if err := r.client.ZAdd(r.ctx, key, redis.Z{Score: score, Member: subscriber}).Err(); err != nil {
		return fmt.Errorf("could not stage subscriber %q of service %q: %w", subscriber, service, err)
	}
	staged[key] = true
	return nil
}

// subscriberScore is the time to score a rebuilt subscriber with.
//
// The most recent subscribe_date any of their devices carries, so that a
// rebuild preserves the last-seen the live index would have recorded. It is an
// optional field, and the rest of uniqush treats it as client-supplied, so a
// device without one or with an unreadable one contributes nothing and a
// subscriber with none at all is scored with the rebuild time. That is honest:
// the truthful answer is "not before now, as far as this database knows".
func (r *PushRedisDB) subscriberScore(dpNames []string) float64 {
	values, err := r.mgetRawDeliveryPoints(dpNames...)
	if err != nil {
		// The records are not the source of truth for membership, only for the
		// score, so a failed read costs a stale-looking last-seen rather than a
		// missing subscriber.
		return float64(time.Now().Unix())
	}

	var newest float64
	for _, value := range values {
		if value == nil {
			continue
		}
		dp, e := r.keyValueToDeliveryPoint(value)
		if e != nil || dp == nil {
			continue
		}
		when, e := strconv.ParseFloat(dp.VolatileData[push.SubscribeDate], 64)
		if e == nil && when > newest {
			newest = when
		}
	}
	if newest == 0 {
		return float64(time.Now().Unix())
	}
	return newest
}

// publishSubscriberIndexStaging swaps the rebuilt indexes in.
//
// Live keys with no staged counterpart are deleted first: a service that has
// lost every subscriber, or a push service type nobody uses any more, would
// otherwise keep an index that the rebuild has just established is wrong, and
// /stats would go on counting it.
func (r *PushRedisDB) publishSubscriberIndexStaging(staged map[string]bool) error {
	for _, prefix := range []string{ServiceToSubscribersPrefix, ServiceTypeToDeliveryPointsPrefix} {
		err := r.scanKeys(prefix+"*", func(page []string) error {
			for _, key := range page {
				if staged[SubscriberIndexStagingPrefix+key] {
					continue
				}
				if e := r.client.Del(r.ctx, key).Err(); e != nil {
					return fmt.Errorf("could not remove the stale index %q: %w", key, e)
				}
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("could not remove stale indexes: %w", err)
		}
	}

	for stagingKey := range staged {
		live := strings.TrimPrefix(stagingKey, SubscriberIndexStagingPrefix)
		if err := r.client.Rename(r.ctx, stagingKey, live).Err(); err != nil {
			return fmt.Errorf("could not publish the rebuilt index %q: %w", live, err)
		}
	}
	return nil
}

// ServiceStats is what one service holds, counted rather than enumerated.
//
// Every field is one redis command against the subscriber index, which is what
// the index is for: the same numbers used to mean walking the keyspace and
// reading a record per device, so nobody could ask for them on a live server.
type ServiceStats struct {
	// Subscribers is how many subscribers the service has, at least one device
	// each. A subscriber with no devices is not in the index.
	Subscribers int64 `json:"subscribers"`
	// SubscribersSince is how many of them last subscribed at or after the
	// requested time. Omitted when no time was asked for, rather than reported
	// as zero, which would read as "nobody".
	SubscribersSince *int64 `json:"subscribers_since,omitempty"`
	// DeliveryPoints is the device count per push service type, for the types
	// the service has a provider for. A type with a provider and no devices is
	// reported as 0, which is a different statement from not being listed.
	DeliveryPoints map[string]int64 `json:"delivery_points"`
}

// SubscriberStats counts the subscribers and devices of each named service, or
// of every known service when none are named.
//
// It refuses, with ErrSubscriberIndexNotBuilt, until the index is known to
// cover the whole database. Answering from a partial index would report a
// service with a million devices as having eleven, and nothing in the answer
// would say so -- a dashboard cannot tell that apart from a service that really
// has eleven, which makes a wrong number worse than no number.
func (r *PushRedisDB) SubscriberStats(services []string, since *int64) (map[string]*ServiceStats, error) {
	built, err := r.subscriberIndexBuilt()
	if err != nil {
		return nil, err
	}
	if !built {
		return nil, ErrSubscriberIndexNotBuilt
	}

	if len(services) == 0 {
		services, err = r.GetServiceNames()
		if err != nil {
			return nil, fmt.Errorf("SubscriberStats: %w", err)
		}
	}

	stats := make(map[string]*ServiceStats, len(services))
	for _, service := range services {
		if service == "" {
			continue
		}
		entry, e := r.statsOfService(service, since)
		if e != nil {
			return nil, e
		}
		stats[service] = entry
	}
	return stats, nil
}

func (r *PushRedisDB) statsOfService(service string, since *int64) (*ServiceStats, error) {
	stats := &ServiceStats{DeliveryPoints: make(map[string]int64, 2)}

	count, err := r.client.ZCard(r.ctx, subscriberIndexKey(service)).Result()
	if err != nil {
		return nil, fmt.Errorf("could not count the subscribers of service %q: %w", service, err)
	}
	stats.Subscribers = count

	if since != nil {
		recent, e := r.client.ZCount(r.ctx, subscriberIndexKey(service), strconv.FormatInt(*since, 10), "+inf").Result()
		if e != nil {
			return nil, fmt.Errorf("could not count the recent subscribers of service %q: %w", service, e)
		}
		stats.SubscribersSince = &recent
	}

	// The types to count are the ones the service has a provider for. Counting
	// whatever type sets happen to exist would report a type whose provider has
	// been removed, and miss a type whose provider is there and unused.
	types, err := r.pushServiceTypesOfService(service)
	if err != nil {
		return nil, err
	}
	for _, pushServiceType := range types {
		devices, e := r.client.SCard(r.ctx, typeDeviceSetKey(service, pushServiceType)).Result()
		if e != nil {
			return nil, fmt.Errorf("could not count the %s delivery points of service %q: %w", pushServiceType, service, e)
		}
		stats.DeliveryPoints[pushServiceType] = devices
	}
	return stats, nil
}

// pushServiceTypesOfService lists the push service types a service can push
// through, from its provider set.
func (r *PushRedisDB) pushServiceTypesOfService(service string) ([]string, error) {
	names, err := r.GetPushServiceProvidersByService(service)
	if err != nil {
		return nil, fmt.Errorf("could not list the providers of service %q: %w", service, err)
	}

	seen := make(map[string]bool, len(names))
	types := make([]string, 0, len(names))
	for _, name := range names {
		psp, e := r.GetPushServiceProvider(name)
		if e != nil {
			if isErrCausedByMissingKey(e) {
				// A name in srv-2-psp with no record behind it, which /checkdb
				// reports as a dangling provider. Nothing can push through it, so
				// it contributes no type to count.
				continue
			}
			return nil, fmt.Errorf("could not read provider %q of service %q: %w", name, service, e)
		}
		if psp == nil || seen[psp.PushServiceName()] {
			continue
		}
		seen[psp.PushServiceName()] = true
		types = append(types, psp.PushServiceName())
	}
	return types, nil
}
