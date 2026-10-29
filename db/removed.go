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
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/redis/go-redis/v9"
	"github.com/uniqush/uniqush-push/push"
)

// RemovedDeliveryPoint is a delivery point uniqush deleted because its push
// service said the device was gone.
//
// Endpoint, DevToken and RegID carry the address the push service rejected, so
// that a caller whose devid has since been re-subscribed with a new address can
// tell the old registration from the live one. They are what /subscribe was
// given and what /subscriptions returns by default; the Web Push auth secret and
// p256dh key, which with the endpoint are enough to push to the browser, are
// never included.
type RemovedDeliveryPoint struct {
	// ID is the /removed cursor of this entry, and Time the unix time it was
	// recorded. Both are left out of a /push response, which is built before
	// the entry is recorded.
	ID              string `json:"id,omitempty"`
	Time            int64  `json:"time,omitempty"`
	Service         string `json:"service"`
	Subscriber      string `json:"subscriber"`
	PushServiceType string `json:"pushServiceType"`
	DeliveryPoint   string `json:"deliveryPoint"`
	DevID           string `json:"devid,omitempty"`
	Endpoint        string `json:"endpoint,omitempty"`
	DevToken        string `json:"devtoken,omitempty"`
	RegID           string `json:"regid,omitempty"`
	Reason          string `json:"reason"`
	// VendorReason distinguishes the push service's reasons without changing
	// uniqush's own Reason code. Older entries and callers may have none.
	VendorReason string `json:"vendorReason,omitempty"`
}

// NewRemovedDeliveryPoint describes dp, which was removed for reason.
func NewRemovedDeliveryPoint(dp *push.DeliveryPoint, reason string) RemovedDeliveryPoint {
	return RemovedDeliveryPoint{
		Service:         dp.FixedData[push.Service],
		Subscriber:      dp.FixedData[push.Subscriber],
		PushServiceType: dp.PushServiceName(),
		DeliveryPoint:   dp.Name(),
		DevID:           dp.VolatileData[push.DeviceID],
		Endpoint:        dp.FixedData["endpoint"],
		DevToken:        dp.FixedData["devtoken"],
		RegID:           dp.FixedData["regid"],
		Reason:          reason,
	}
}

// RemovedLogLength is how many removals /removed keeps per service.
//
// A count rather than an age, because removals come in bursts -- a broadcast to
// a service with many stale devices -- and a time-based cap would let one burst
// grow the log without bound.
const RemovedLogLength = 100000

const (
	// RemovedPrefix is the prefix of keys for a redis STREAM - the removals of
	// one service, oldest first.
	RemovedPrefix = "removed.dp:"
	// RemovedTrimmedPrefix is the prefix of keys for a redis STRING - the ID of
	// the newest removal trimmed from a service's stream, which is how /removed
	// knows whether a cursor has fallen off the end.
	RemovedTrimmedPrefix = "removed.dp.trimmed:"
)

// recordRemovedScript appends a removal and trims the stream to ARGV[1]
// entries, remembering the newest entry it trims.
//
// Trimmed here rather than with XADD MAXLEN, because redis does not report what
// MAXLEN removed, and without that a cursor older than the oldest entry is
// ambiguous: it is equally consistent with nothing having been missed.
var recordRemovedScript = redis.NewScript(`
local id = redis.call('XADD', KEYS[1], '*', 'entry', ARGV[2])
local excess = redis.call('XLEN', KEYS[1]) - tonumber(ARGV[1])
if excess > 0 then
  local oldest = redis.call('XRANGE', KEYS[1], '-', '+', 'COUNT', excess)
  redis.call('SET', KEYS[2], oldest[#oldest][1])
  redis.call('XTRIM', KEYS[1], 'MAXLEN', ARGV[1])
end
return id`)

// RecordRemovedDeliveryPoint appends a removal to its service's log.
func (r *PushRedisDB) RecordRemovedDeliveryPoint(removed RemovedDeliveryPoint) error {
	entry, err := json.Marshal(removed)
	if err != nil {
		return err
	}
	keys := []string{RemovedPrefix + removed.Service, RemovedTrimmedPrefix + removed.Service}
	if err := recordRemovedScript.Run(r.ctx, r.client, keys, r.removedLogLength, entry).Err(); err != nil {
		return fmt.Errorf("recording the removal of %q failed: %w", removed.DeliveryPoint, err)
	}
	return nil
}

// RemovedDeliveryPoints returns up to count removals from a service recorded
// after the cursor after, oldest first. truncated is whether removals after
// that cursor have already been trimmed.
func (r *PushRedisDB) RemovedDeliveryPoints(service, after string, count int64) (removed []RemovedDeliveryPoint, truncated bool, err error) {
	cursor, err := parseStreamID(after)
	if err != nil {
		return nil, false, err
	}
	// A cursor past the newest entry would otherwise be answered with nothing,
	// and with itself as the next cursor, for as long as it stays ahead.
	newest, err := r.client.XRevRangeN(r.ctx, RemovedPrefix+service, "+", "-", 1).Result()
	if err != nil {
		return nil, false, fmt.Errorf("reading the removals of %q failed: %w", service, err)
	}
	newestID := [2]uint64{}
	if len(newest) > 0 {
		if newestID, err = parseStreamID(newest[0].ID); err != nil {
			return nil, false, err
		}
	}
	if streamIDLess(newestID, cursor) {
		return nil, false, fmt.Errorf("%w %q: it is past the newest removal of %q, so it did not come from /removed for this service",
			ErrInvalidCursor, after, service)
	}

	// Read before the trim marker: a trim between the two then makes the answer
	// claim a truncation that did not cost this caller anything, rather than
	// hide one that did.
	streams, err := r.client.XRead(r.ctx, &redis.XReadArgs{
		Streams: []string{RemovedPrefix + service, after},
		Count:   count,
		Block:   -1,
	}).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return nil, false, fmt.Errorf("reading the removals of %q failed: %w", service, err)
	}
	removed = []RemovedDeliveryPoint{}
	for _, stream := range streams {
		for _, message := range stream.Messages {
			var entry RemovedDeliveryPoint
			if raw, ok := message.Values["entry"].(string); !ok || json.Unmarshal([]byte(raw), &entry) != nil {
				return nil, false, fmt.Errorf("removal %s of %q is not readable", message.ID, service)
			}
			entry.ID = message.ID
			if id, e := parseStreamID(message.ID); e == nil {
				entry.Time = int64(id[0] / 1000)
			}
			removed = append(removed, entry)
		}
	}

	trimmed, err := r.client.Get(r.ctx, RemovedTrimmedPrefix+service).Result()
	if errors.Is(err, redis.Nil) {
		return removed, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("reading how far the removals of %q were trimmed failed: %w", service, err)
	}
	trimmedID, err := parseStreamID(trimmed)
	if err != nil {
		return nil, false, err
	}
	return removed, streamIDLess(cursor, trimmedID), nil
}

func streamIDLess(a, b [2]uint64) bool {
	return a[0] < b[0] || (a[0] == b[0] && a[1] < b[1])
}

// ErrInvalidCursor is a /removed cursor that is not a redis stream ID.
var ErrInvalidCursor = errors.New("invalid cursor")

// parseStreamID parses a redis stream ID, "<ms>-<seq>" or "<ms>".
func parseStreamID(id string) ([2]uint64, error) {
	var parsed [2]uint64
	parts := strings.SplitN(id, "-", 2)
	for i, part := range parts {
		n, err := strconv.ParseUint(part, 10, 64)
		if err != nil {
			return parsed, fmt.Errorf("%w %q", ErrInvalidCursor, id)
		}
		parsed[i] = n
	}
	return parsed, nil
}
