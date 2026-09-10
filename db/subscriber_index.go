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
	"time"

	"github.com/uniqush/uniqush-push/log"
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
