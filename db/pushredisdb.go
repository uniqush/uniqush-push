/*
 * Copyright 2011 Nan Deng
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
 *
 */

package db

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/uniqush/uniqush-push/log"
	"github.com/uniqush/uniqush-push/push"
)

// PushRedisDB is currently the only uniqush pushRawDatabase implementation.
// It stores push service providers, delivery points, etc. in redis.
type PushRedisDB struct {
	client redisClient
	psm    *push.PushServiceManager

	// ctx is passed to every redis command. go-redis v9 requires one.
	//
	// This is deliberately a single background context rather than a per-request
	// one: threading the inbound *http.Request context down from the REST
	// handlers is a worthwhile follow-up, but it changes the db package's public
	// interface, and doing it in the same commit as the client upgrade would
	// make a bisect impossible to interpret. Note that v9's
	// ContextTimeoutEnabled defaults to false, so commands are bounded by
	// ReadTimeout/WriteTimeout regardless.
	ctx context.Context

	// indexBuilt caches a positive answer to "is the subscriber index built",
	// which is otherwise one EXISTS per wildcard push.
	//
	// Only ever set, never cleared: nothing deletes the marker, so a true answer
	// stays true for the life of the process. A false answer is deliberately not
	// cached, so that /rebuildsubscriberindex takes effect without a restart --
	// including a rebuild run against a different uniqush instance.
	indexBuilt atomic.Bool
}

// redisClient is the subset of go-redis this package uses. Method signatures
// mirror go-redis v9, which takes a context.Context as the first argument of
// every command.
//
// It embeds redis.Scripter, which is the set of commands *redis.Script needs to
// run a Lua script: EVALSHA first, falling back to EVAL and caching the script
// when the server has not seen it. Embedding the upstream interface rather than
// restating its methods means a change to it is a compile error here instead of
// a runtime one.
type redisClient interface {
	redis.Scripter

	// DBSize is asked once, at startup, to tell a database with nothing in it
	// from one that has never been indexed.
	DBSize(ctx context.Context) *redis.IntCmd
	Del(ctx context.Context, keys ...string) *redis.IntCmd
	Exists(ctx context.Context, keys ...string) *redis.IntCmd
	FlushDB(ctx context.Context) *redis.StatusCmd // for tests only
	Get(ctx context.Context, key string) *redis.StringCmd
	MGet(ctx context.Context, keys ...string) *redis.SliceCmd
	// Rename is how a rebuilt index replaces the live one: a reader sees the old
	// index or the new one, never a half-built one.
	Rename(ctx context.Context, key, newkey string) *redis.StatusCmd
	// Ping is the cheapest question redis answers, and the only one uniqush
	// asks purely to find out whether it is being answered at all.
	Ping(ctx context.Context) *redis.StatusCmd
	Save(ctx context.Context) *redis.StatusCmd
	// Scan is how anything here walks the keyspace, and KEYS is deliberately
	// absent so that there is no second way to do it: KEYS holds the redis event
	// loop for the whole walk, which on a database large enough to be worth
	// walking means stalling every push for the duration. See scanKeys.
	Scan(ctx context.Context, cursor uint64, match string, count int64) *redis.ScanCmd
	SAdd(ctx context.Context, key string, members ...interface{}) *redis.IntCmd
	SCard(ctx context.Context, key string) *redis.IntCmd
	SIsMember(ctx context.Context, key string, member interface{}) *redis.BoolCmd
	SRem(ctx context.Context, key string, members ...interface{}) *redis.IntCmd
	Set(ctx context.Context, key string, value interface{}, expiration time.Duration) *redis.StatusCmd
	SMembers(ctx context.Context, key string) *redis.StringSliceCmd
	// SScan walks one set, for the sets with a member per device.
	SScan(ctx context.Context, key string, cursor uint64, match string, count int64) *redis.ScanCmd
	ZAdd(ctx context.Context, key string, members ...redis.Z) *redis.IntCmd
	ZCard(ctx context.Context, key string) *redis.IntCmd
	ZCount(ctx context.Context, key, min, max string) *redis.IntCmd
	// ZScore answers whether one subscriber is indexed, and when they were last
	// seen. A missing member comes back as redis.Nil.
	ZScore(ctx context.Context, key, member string) *redis.FloatCmd
	// ZScan walks one service's subscriber index, which is how a wildcard push
	// finds its subscribers without walking the keyspace at all.
	ZScan(ctx context.Context, key string, cursor uint64, match string, count int64) *redis.ScanCmd
	// Watch runs fn with the given keys watched. A transaction that fn opens
	// then fails to commit if another client has written one of those keys in
	// the meantime, rather than overwriting the change. Reads inside fn go
	// through the *redis.Tx, which is a single dedicated connection to the
	// master.
	Watch(ctx context.Context, fn func(*redis.Tx) error, keys ...string) error
}

type redisMultiClient struct {
	masterClient *redis.Client
	slaveClient  *redis.Client
}

// The four scripting commands go to the master: every script uniqush runs
// writes. EVAL_RO and EVALSHA_RO are the read-only forms, which redis refuses to
// run a writing script under, so those are safe on the replica -- and nothing
// here uses them yet.
func (mc *redisMultiClient) Eval(ctx context.Context, script string, keys []string, args ...interface{}) *redis.Cmd {
	return mc.masterClient.Eval(ctx, script, keys, args...)
}

func (mc *redisMultiClient) EvalSha(ctx context.Context, sha1 string, keys []string, args ...interface{}) *redis.Cmd {
	return mc.masterClient.EvalSha(ctx, sha1, keys, args...)
}

func (mc *redisMultiClient) EvalRO(ctx context.Context, script string, keys []string, args ...interface{}) *redis.Cmd {
	return mc.slaveClient.EvalRO(ctx, script, keys, args...)
}

func (mc *redisMultiClient) EvalShaRO(ctx context.Context, sha1 string, keys []string, args ...interface{}) *redis.Cmd {
	return mc.slaveClient.EvalShaRO(ctx, sha1, keys, args...)
}

// ScriptExists and ScriptLoad ask about the master's script cache, because that
// is where the scripts run. Asking the replica would report a cache uniqush
// never uses.
func (mc *redisMultiClient) ScriptExists(ctx context.Context, hashes ...string) *redis.BoolSliceCmd {
	return mc.masterClient.ScriptExists(ctx, hashes...)
}

func (mc *redisMultiClient) ScriptLoad(ctx context.Context, script string) *redis.StringCmd {
	return mc.masterClient.ScriptLoad(ctx, script)
}

func (mc *redisMultiClient) DBSize(ctx context.Context) *redis.IntCmd {
	return mc.masterClient.DBSize(ctx)
}

func (mc *redisMultiClient) Del(ctx context.Context, keys ...string) *redis.IntCmd {
	return mc.masterClient.Del(ctx, keys...)
}

func (mc *redisMultiClient) Exists(ctx context.Context, keys ...string) *redis.IntCmd {
	return mc.slaveClient.Exists(ctx, keys...)
}

func (mc *redisMultiClient) FlushDB(ctx context.Context) *redis.StatusCmd {
	return mc.masterClient.FlushDB(ctx)
}

func (mc *redisMultiClient) Get(ctx context.Context, key string) *redis.StringCmd {
	return mc.slaveClient.Get(ctx, key)
}

// Ping checks both halves of a master/replica pair.
//
// Reads go to the replica and writes to the master, so uniqush is only healthy
// when both answer: a replica that has gone means every /push fails to read the
// devices it should send to, however well the master is doing. The master is
// checked first, and the first failure is what gets reported -- naming one
// unreachable server is more use than saying "something is unreachable".
func (mc *redisMultiClient) Ping(ctx context.Context) *redis.StatusCmd {
	if cmd := mc.masterClient.Ping(ctx); cmd.Err() != nil || mc.slaveClient == nil {
		return cmd
	}
	return mc.slaveClient.Ping(ctx)
}

func (mc *redisMultiClient) Scan(ctx context.Context, cursor uint64, match string, count int64) *redis.ScanCmd {
	return mc.slaveClient.Scan(ctx, cursor, match, count)
}

// Watch goes to the master, unlike most reads here. A transaction that read its
// inputs from a replica would be deciding on data that may already be stale by
// the time it commits, which is the thing WATCH exists to prevent.
func (mc *redisMultiClient) Watch(ctx context.Context, fn func(*redis.Tx) error, keys ...string) error {
	return mc.masterClient.Watch(ctx, fn, keys...)
}

func (mc *redisMultiClient) MGet(ctx context.Context, keys ...string) *redis.SliceCmd {
	return mc.slaveClient.MGet(ctx, keys...)
}

func (mc *redisMultiClient) Save(ctx context.Context) *redis.StatusCmd {
	return mc.masterClient.Save(ctx)
}

func (mc *redisMultiClient) Rename(ctx context.Context, key, newkey string) *redis.StatusCmd {
	return mc.masterClient.Rename(ctx, key, newkey)
}

func (mc *redisMultiClient) SAdd(ctx context.Context, key string, members ...interface{}) *redis.IntCmd {
	return mc.masterClient.SAdd(ctx, key, members...)
}

func (mc *redisMultiClient) SCard(ctx context.Context, key string) *redis.IntCmd {
	return mc.slaveClient.SCard(ctx, key)
}

func (mc *redisMultiClient) SRem(ctx context.Context, key string, members ...interface{}) *redis.IntCmd {
	return mc.masterClient.SRem(ctx, key, members...)
}

func (mc *redisMultiClient) Set(ctx context.Context, key string, value interface{}, expiration time.Duration) *redis.StatusCmd {
	return mc.masterClient.Set(ctx, key, value, expiration)
}

func (mc *redisMultiClient) SIsMember(ctx context.Context, key string, member interface{}) *redis.BoolCmd {
	return mc.slaveClient.SIsMember(ctx, key, member)
}

func (mc *redisMultiClient) SMembers(ctx context.Context, key string) *redis.StringSliceCmd {
	return mc.slaveClient.SMembers(ctx, key)
}

func (mc *redisMultiClient) SScan(ctx context.Context, key string, cursor uint64, match string, count int64) *redis.ScanCmd {
	return mc.slaveClient.SScan(ctx, key, cursor, match, count)
}

func (mc *redisMultiClient) ZAdd(ctx context.Context, key string, members ...redis.Z) *redis.IntCmd {
	return mc.masterClient.ZAdd(ctx, key, members...)
}

func (mc *redisMultiClient) ZCard(ctx context.Context, key string) *redis.IntCmd {
	return mc.slaveClient.ZCard(ctx, key)
}

func (mc *redisMultiClient) ZCount(ctx context.Context, key, min, max string) *redis.IntCmd {
	return mc.slaveClient.ZCount(ctx, key, min, max)
}

func (mc *redisMultiClient) ZScan(ctx context.Context, key string, cursor uint64, match string, count int64) *redis.ScanCmd {
	return mc.slaveClient.ZScan(ctx, key, cursor, match, count)
}

func (mc *redisMultiClient) ZScore(ctx context.Context, key, member string) *redis.FloatCmd {
	return mc.slaveClient.ZScore(ctx, key, member)
}

var _ redisClient = &redis.Client{}
var _ pushRawDatabase = &PushRedisDB{}

const (
	// DeliveryPointPrefix is the prefix of keys for a redis STRING - Maps the delivery point name to a json blob of information about a delivery point.
	DeliveryPointPrefix string = "delivery.point:"
	// PushServiceProviderPrefix is the prefix of keys for a redis STRING - Maps a push service provider name to a json blob of information about it.
	PushServiceProviderPrefix string = "push.service.provider:"
	// ServiceSubscriberToDeliveryPointsPrefix is the prefix of keys for a redis SET - Maps a service name + subscriber to a set of delivery point names
	ServiceSubscriberToDeliveryPointsPrefix string = "srv.sub-2-dp:"
	// ServiceDeliveryPointToPushServiceProviderPrefix is the prefix of keys for a redis STRING - Maps a service name + delivery point name to the push service provider
	ServiceDeliveryPointToPushServiceProviderPrefix string = "srv.dp-2-psp:"
	// ServiceToPushServiceProvidersPrefix is the prefix of keys for a redis SET - Maps a service name to a set of PSP names
	ServiceToPushServiceProvidersPrefix string = "srv-2-psp:"
	// ServiceToSubscribersPrefix is the prefix of keys for a redis ZSET - Maps a
	// service name to its subscribers, scored by the unix time of each
	// subscriber's most recent /subscribe.
	//
	// A sorted set rather than a plain one because the score turns "how many
	// subscribers have been seen since T" into one ZCOUNT, and /subscribe is
	// what an application calls when it launches, so the score is a usable
	// last-seen. It costs roughly twice a SET's memory per member; the device
	// set below has no such use and stays a SET.
	ServiceToSubscribersPrefix string = "srv-2-sub:"
	// ServiceTypeToDeliveryPointsPrefix is the prefix of keys for a redis SET -
	// Maps a service name + push service type to the names of that service's
	// delivery points of that type, so that counting them is one SCARD.
	ServiceTypeToDeliveryPointsPrefix string = "srv.type-2-dp:"
	// SubscriberIndexBuiltKey is the key for a redis STRING - it holds the unix
	// time at which the subscriber index above was last rebuilt in full.
	//
	// The presence of srv-2-sub:<service> cannot answer the same question. Redis
	// deletes an empty sorted set, so the key is absent on a database that has
	// never been indexed and is recreated, holding one member, by the first
	// /subscribe after an upgrade. A read path keyed on that would switch itself
	// on against an index covering only the subscribers who happened to
	// re-subscribe, and a wildcard push would silently miss everyone else.
	SubscriberIndexBuiltKey string = "subscriber.index:built"
	// SubscriberIndexStagingPrefix is prefixed to an index key while
	// /rebuildsubscriberindex is filling it, so that the rebuilt copy is renamed
	// over the live one in a single step and a concurrent reader sees one or the
	// other rather than a half-built index.
	//
	// A prefix rather than a suffix on purpose: nothing that walks the keyspace
	// for "srv-2-sub:*" or "srv.type-2-dp:*" can then mistake a key being built
	// for a service of its own.
	SubscriberIndexStagingPrefix string = "rebuilding:"
	// DeliveryPointCounterPrefix is the prefix of keys for a redis STRING - it
	// mapped a delivery point name to the number of subscribers using it.
	//
	// Deprecated: nothing writes these any more. A delivery point's name is the
	// SHA1 of its fixed data, and that data carries the service and the
	// subscriber, so a delivery point belongs to exactly one subscription and
	// the count was only ever 0 or 1. The prefix stays declared so that
	// CheckConsistency can still find the keys an older uniqush left behind.
	DeliveryPointCounterPrefix string = "delivery.point.counter:"
	// ServicesSet is the key for a redis SET - This is a set of service names.
	ServicesSet string = "services{0}"
)

// Subscribing and unsubscribing are each a single redis script.
//
// They used to be several commands with Go deciding in between: SADD and then
// INCR only if the SADD was new, SREM and then DECR and then two DELs if the
// count reached zero. A crash in either window left debris -- a counter with
// nothing behind it, or a delivery point record nothing referenced -- and
// /checkdb grew a problem class for each. A script runs to completion on the
// server or not at all, which removes the window instead of repairing it.
//
// Every key a script touches arrives in KEYS, and neither script builds a key
// name of its own. That is the redis convention, and it keeps the key layout in
// one place: the constants above. It also means a caller can say in advance
// which keys a call will touch, which is what a cluster client would need.
// The three keys are, in order: the subscriber's device set, the service's
// subscriber index, and the service's device set for this device's push service
// type. Both scripts take the same three, so the two paths cannot drift.
//
// The conditional ZREM on the unsubscribe path is why these are scripts rather
// than a MULTI: whether the subscriber leaves the index depends on how many
// devices are left, which is a read the transaction would have to make first.
var (
	// subscribeScript adds a delivery point to a subscriber's device set and
	// records the subscriber and the device in the service's indexes.
	//
	// ARGV[1] is the delivery point name, ARGV[2] the subscriber and ARGV[3] the
	// time to score the subscriber with. The result is the number of members
	// added to the device set, so 0 when the device was already subscribed --
	// the score is still refreshed, which is what makes it a last-seen.
	subscribeScript = redis.NewScript(`
local added = redis.call('SADD', KEYS[1], ARGV[1])
redis.call('ZADD', KEYS[2], ARGV[3], ARGV[2])
redis.call('SADD', KEYS[3], ARGV[1])
return added`)

	// unsubscribeScript takes it out again, returning the number removed.
	//
	// ARGV[1] is the delivery point name and ARGV[2] the subscriber. The
	// subscriber leaves the service's index only when the device removed was
	// their last one.
	unsubscribeScript = redis.NewScript(`
local removed = redis.call('SREM', KEYS[1], ARGV[1])
redis.call('SREM', KEYS[3], ARGV[1])
if redis.call('SCARD', KEYS[1]) == 0 then
  redis.call('ZREM', KEYS[2], ARGV[2])
end
return removed`)
)

// subscriptionIndexKeys are the keys both scripts take, for one subscription.
//
// The scripts name no key of their own, so this function is the only place the
// index layout is written down for the write path -- and a caller can therefore
// say in advance which keys a call will touch, which is what a cluster client
// would need.
func subscriptionIndexKeys(srv, sub, dp string) []string {
	return []string{
		deviceSetKey(srv, sub),
		subscriberIndexKey(srv),
		typeDeviceSetKey(srv, deliveryPointTypeFromName(dp)),
	}
}

// deviceSetKey names the SET of delivery points a subscriber has in a service.
func deviceSetKey(srv, sub string) string {
	return ServiceSubscriberToDeliveryPointsPrefix + srv + ":" + sub
}

// subscriberIndexKey names the ZSET of a service's subscribers.
func subscriberIndexKey(srv string) string {
	return ServiceToSubscribersPrefix + srv
}

// typeDeviceSetKey names the SET of a service's delivery points of one push
// service type.
func typeDeviceSetKey(srv, pushServiceType string) string {
	return ServiceTypeToDeliveryPointsPrefix + srv + ":" + pushServiceType
}

// deliveryPointTypeFromName reads the push service type out of a delivery point
// name, which is "<pushservicetype>:<sha1 of its fixed data>".
func deliveryPointTypeFromName(name string) string {
	if index := strings.Index(name, ":"); index > 0 {
		return name[:index]
	}
	return ""
}

// scanKeysCount is the COUNT hint on each SCAN: fewer round trips against less
// work per call for redis. Nobody should need to tune it.
const scanKeysCount = 500

// scanKeys walks every key matching pattern, handing each page to visit.
//
// This is the only way anything here walks the keyspace, and redisClient no
// longer offers KEYS so that it stays that way. KEYS answers the same question
// in one command, and that is the problem: redis runs it to completion on the
// single thread that serves every other client, so on a database large enough
// for the walk to take a while it stalls every push -- pushes to unrelated
// services included, and, when the server is shared, every other application's
// traffic with them.
//
// Streaming rather than returning the keys, because some of the patterns walked
// here -- one key per binding, one per counter -- have a key per device. A
// database big enough to be worth walking is exactly one where holding that
// list, plus a set to deduplicate it, is a way to run the process out of
// memory. A caller whose own result is already proportional to the matched keys
// has nothing to save by streaming and can use scanUniqueKeys instead.
//
// SCAN trades KEYS's single long stall for a series of short ones, and gives up
// the snapshot in exchange. A key added or removed mid-walk may or may not
// appear; a key present throughout appears at least once, and can appear twice
// if redis resizes its table underneath the cursor. Both are the caller's to
// handle -- scanUniqueKeys for the callers that cannot take a repeat, and see
// CheckConsistency for why one caller can.
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

// scanUniqueKeys collects every key matching pattern, without the repeats a
// SCAN can hand back.
//
// Only for callers that were going to hold a row per matched key regardless, so
// that the set kept here is bounded by an allocation they already make. A walk
// over a pattern with a key per device should stream through scanKeys instead
// and cope with the duplicates itself.
func (r *PushRedisDB) scanUniqueKeys(pattern string) ([]string, error) {
	var keys []string
	seen := make(map[string]bool)
	err := r.scanKeys(pattern, func(page []string) error {
		for _, key := range page {
			if seen[key] {
				continue
			}
			seen[key] = true
			keys = append(keys, key)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return keys, nil
}

// buildRedisSlaveClient will optionally returns a redis client for uniqush-push to use for read-only operations (such as fetching subscriptions and services).
func buildRedisSlaveClient(c *DatabaseConfig) (*redis.Client, error) {
	host := c.SlaveHost
	port := c.SlavePort
	name := c.Name
	if host == "" && port <= 0 {
		return nil, nil
	}

	if host == "" || port <= 0 {
		return nil, errors.New("Missing redis slave host or port")
	}

	db, err := strconv.ParseInt(name, 10, 64)
	if err != nil {
		db = 0
	}
	ret := redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%d", host, port),
		Password: c.Password,
		DB:       int(db),
		Protocol: 2, // see buildRedisClient
	})
	return ret, nil
}

// buildRedisClient will build the client used to fetch and update subscriptions, services, etc.
func buildRedisClient(c *DatabaseConfig) (redisClient, error) {
	if c == nil {
		return nil, errors.New("Invalid Database Config")
	}
	if strings.ToLower(c.Engine) != "redis" {
		return nil, errors.New("Unsupported Database Engine")
	}

	if c.Host == "" {
		c.Host = "localhost"
	}
	if c.Port <= 0 {
		c.Port = 6379
	}
	if c.Name == "" {
		c.Name = "0"
	}

	db, err := strconv.ParseInt(c.Name, 10, 64)
	if err != nil {
		db = 0
	}
	client := redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%d", c.Host, c.Port),
		Password: c.Password,
		DB:       int(db),
		// go-redis v9 negotiates RESP3 by default. None of the commands used
		// here decode differently under RESP3, but pinning RESP2 keeps this
		// upgrade to one variable: if something breaks, it is the context
		// refactor and not the wire protocol. Worth revisiting separately.
		Protocol: 2,
	})
	if slaveClient, err := buildRedisSlaveClient(c); slaveClient != nil || err != nil {
		if err != nil {
			return nil, fmt.Errorf("invalid Redis slave database config: %w", err)
		}
		dualClient := &redisMultiClient{
			masterClient: client,
			slaveClient:  slaveClient,
		}
		return dualClient, nil
	}
	return client, nil
}

func buildPushRedisDB(client redisClient, psm *push.PushServiceManager) *PushRedisDB {
	ret := new(PushRedisDB)
	ret.client = client
	ret.ctx = context.Background()
	ret.psm = psm
	if ret.psm == nil {
		ret.psm = push.GetPushServiceManager()
	}
	return ret
}

func newPushRedisDB(c *DatabaseConfig) (*PushRedisDB, error) {
	client, err := buildRedisClient(c)
	if err != nil {
		return nil, err
	}

	ret := buildPushRedisDB(client, c.PushServiceManager)
	return ret, nil
}

func (r *PushRedisDB) keyValueToDeliveryPoint(value []byte) (dp *push.DeliveryPoint, err error) {
	psm := r.psm
	dp, err = psm.BuildDeliveryPointFromBytes(value)
	if err != nil {
		dp = nil
	}
	return
}

func (r *PushRedisDB) keyValueToPushServiceProvider(value []byte) (psp *push.PushServiceProvider, err error) {
	psm := r.psm
	psp, err = psm.BuildPushServiceProviderFromBytes(value)
	if err != nil {
		psp = nil
	}
	return
}

func deliveryPointToValue(dp *push.DeliveryPoint) []byte {
	return dp.Marshal()
}

func pushServiceProviderToValue(psp *push.PushServiceProvider) []byte {
	return psp.Marshal()
}

func (r *PushRedisDB) mgetStrings(keys ...string) ([][]byte, error) {
	data, err := r.client.MGet(r.ctx, keys...).Result()
	if err != nil {
		return nil, err
	}
	results := make([][]byte, len(data))
	for i, result := range data {
		if r, ok := result.(string); ok {
			results[i] = []byte(r)
		} else if result == nil {
			results[i] = nil
		} else {
			// Nil?
			return nil, fmt.Errorf("Unexpected mget result type got %T %#v", result, result)
		}
	}
	return results, nil
}

func (r *PushRedisDB) mgetRawDeliveryPoints(deliveryPointNames ...string) ([][]byte, error) {
	var deliveryPointKeys []string
	for _, deliveryPointName := range deliveryPointNames {
		deliveryPointKeys = append(deliveryPointKeys, DeliveryPointPrefix+deliveryPointName)
	}

	deliveryPointData, err := r.mgetStrings(deliveryPointKeys...)
	if err != nil {
		return nil, fmt.Errorf("error getting deliveryPointKeys: %w", err)
	}
	return deliveryPointData, nil
}

// GetDeliveryPoint fetches the delivery point with a given generated name.
// GetDeliveryPoint returns the delivery point with the given name.
//
// A missing key is reported as an error wrapping redis.Nil. The wrapping uses
// %w rather than %v deliberately: pushdb.go's isErrCausedByMissingKey uses
// errors.Is to decide whether to garbage-collect an orphaned delivery point,
// and %v would flatten the sentinel and silently disable that cleanup.
func (r *PushRedisDB) GetDeliveryPoint(name string) (*push.DeliveryPoint, error) {
	b, err := r.client.Get(r.ctx, DeliveryPointPrefix+name).Bytes()
	if err != nil {
		return nil, fmt.Errorf("GetDeliveryPoint failed: %w", err)
	}
	if len(b) == 0 {
		return nil, nil
	}
	return r.keyValueToDeliveryPoint(b)
}

// SetDeliveryPoint sets (adds or updates) the delivery point representation in the database.
func (r *PushRedisDB) SetDeliveryPoint(dp *push.DeliveryPoint) error {
	err := r.client.Set(r.ctx, DeliveryPointPrefix+dp.Name(), deliveryPointToValue(dp), 0).Err()
	return err
}

// GetPushServiceProvider will fetch and unserialize the push service provider with the given name.
func (r *PushRedisDB) GetPushServiceProvider(name string) (*push.PushServiceProvider, error) {
	cmd := r.client.Get(r.ctx, PushServiceProviderPrefix+name)
	b, err := cmd.Bytes()
	if err != nil {
		return nil, fmt.Errorf("GetPushServiceProvider failed: %w", err)
	}
	if len(b) == 0 {
		return nil, nil
	}
	return r.keyValueToPushServiceProvider(b)
}

// GetPushServiceProviderConfigs will fetch and unserialize the push service providers with the given names.
func (r *PushRedisDB) GetPushServiceProviderConfigs(names []string) ([]*push.PushServiceProvider, []error) {
	if len(names) == 0 {
		return nil, nil
	}
	keys := make([]string, len(names))
	for i, name := range names {
		keys[i] = PushServiceProviderPrefix + name
	}
	values, err := r.mgetStrings(keys...)
	if err != nil {
		return nil, []error{fmt.Errorf("GetPushServiceProviderConfigs: %w", err)}
	}
	errors := make([]error, 0)
	psps := make([]*push.PushServiceProvider, 0)
	for i, value := range values {
		if value == nil {
			errors = append(errors, fmt.Errorf("Missing a PushServiceProvider for %q, key %q", names[i], keys[i]))
			continue
		}
		psp, err := r.keyValueToPushServiceProvider(value)
		if err != nil {
			errors = append(errors, fmt.Errorf("invalid psp for %s: %w", names[i], err))
		} else {
			psps = append(psps, psp)
		}
	}
	return psps, errors
}

// SetPushServiceProvider will add or update the push service provider psp. The redis key is based on a hash of FixedData.
func (r *PushRedisDB) SetPushServiceProvider(psp *push.PushServiceProvider) error {
	if err := r.client.Set(r.ctx, PushServiceProviderPrefix+psp.Name(), pushServiceProviderToValue(psp), 0).Err(); err != nil {
		return fmt.Errorf("SetPushServiceProvider %q failed: %w", psp.Name(), err)
	}
	return nil
}

// RemoveDeliveryPoint will remove the data for a delivery point.
func (r *PushRedisDB) RemoveDeliveryPoint(dp string) error {
	err := r.client.Del(r.ctx, DeliveryPointPrefix+dp).Err()
	if err != nil {
		return fmt.Errorf("RemoveDP %q failed: %w", dp, err)
	}
	return nil
}

// RemovePushServiceProvider will remove a push service provider's configuration
func (r *PushRedisDB) RemovePushServiceProvider(psp string) error {
	err := r.client.Del(r.ctx, PushServiceProviderPrefix+psp).Err()
	if err != nil {
		return fmt.Errorf("RemovePSP %q failed: %w", psp, err)
	}
	return nil
}

// GetDeliveryPointsNameByServiceSubscriber will get the delivery point for a service and it's subscriber
//
// A "*" in either name makes this a pattern covering many subscribers, which
// /push accepts and does not validate. That is the one keyspace walk uniqush
// does on a request path, and it ran KEYS: a single wildcard push held redis
// for the length of a full walk, so every other push -- and everything else
// sharing the server -- waited on it. It scans now.
//
// The keys come back deduplicated, which matters more here than the stall did.
// A SCAN can hand the same key back twice, and each repeat would be a second
// copy of every delivery point behind it: a duplicate notification on the
// subscriber's phone. The set that prevents that holds an entry per matched
// subscriber, which is what the returned map holds anyway.
func (r *PushRedisDB) GetDeliveryPointsNameByServiceSubscriber(srv, sub string) (map[string][]string, error) {
	pattern := deviceSetKey(srv, sub)
	keys := []string{pattern}
	if strings.Contains(sub, "*") || strings.Contains(srv, "*") {
		var err error
		keys, err = r.scanUniqueKeys(pattern)
		if err != nil {
			return nil, fmt.Errorf("GetDPsNameByServiceSubscriber dp lookup '%s:%s' failed: %w", srv, sub, err)
		}
	}

	ret := make(map[string][]string, len(keys))
	for _, k := range keys {
		m, err := r.client.SMembers(r.ctx, k).Result()
		if err != nil {
			return nil, fmt.Errorf("GetDPsNameByServiceSubscriber smembers %q failed: %w", k, err)
		}
		if m == nil {
			continue
		}
		elem := strings.Split(k, ":")
		s := elem[1]
		if l, ok := ret[s]; !ok || l == nil {
			ret[s] = make([]string, 0, len(keys))
		}
		for _, bm := range m {
			dpl := ret[s]
			dpl = append(dpl, bm)
			ret[s] = dpl
		}
	}
	return ret, nil
}

// GetPushServiceProviderNameByServiceDeliveryPoint returns the push service provider name of a delivery point belonging to a given service name.
func (r *PushRedisDB) GetPushServiceProviderNameByServiceDeliveryPoint(srv, dp string) (string, error) {
	b, err := r.client.Get(r.ctx, ServiceDeliveryPointToPushServiceProviderPrefix+srv+":"+dp).Result()
	if err != nil {
		return "", fmt.Errorf("GetPSPNameByServiceDP failed: %w", err)
	}
	return b, nil
}

// AddDeliveryPointToServiceSubscriber will associate the name of the given delivery point with the given service name and subscriber name.
func (r *PushRedisDB) AddDeliveryPointToServiceSubscriber(srv, sub, dp string) error {
	err := subscribeScript.Run(r.ctx, r.client, subscriptionIndexKeys(srv, sub, dp),
		dp, sub, time.Now().Unix()).Err()
	if err != nil {
		return fmt.Errorf("AddDPToServiceSubscriber failed: %w", err)
	}
	return nil
}

// RemoveDeliveryPointFromServiceSubscriber will remove the given delivery point's name from the subscriber of the provided service.
//
// The delivery point record goes with it, unconditionally. This used to be
// guarded by a refcount, and that guard was always satisfied: the record belongs
// to exactly one subscription, because the name it is stored under hashes the
// service and the subscriber along with the device token.
func (r *PushRedisDB) RemoveDeliveryPointFromServiceSubscriber(srv, sub, dp string) error {
	err := unsubscribeScript.Run(r.ctx, r.client, subscriptionIndexKeys(srv, sub, dp), dp, sub).Err()
	if err != nil {
		return fmt.Errorf("removing the delivery point pointer %q from \"%s:%s\" failed: %w", dp, srv, sub, err)
	}
	if err := r.client.Del(r.ctx, DeliveryPointPrefix+dp).Err(); err != nil {
		return fmt.Errorf("failed to remove delivery point info for %q: %w", dp, err)
	}
	return nil
}

// RemoveMissingDeliveryPointFromServiceSubscriber removes any associations from a subscription list to a dp with missing subscriptions.
//
// The precondition is that delivery.point:<dp> is already gone, so this is the
// unsubscribe path with the record deletion left out.
func (r *PushRedisDB) RemoveMissingDeliveryPointFromServiceSubscriber(service, subscriber, dpName string, logger log.Logger) {
	// The statement below logs only when redis fails, so a nil logger here would
	// panic exactly where something has already gone wrong -- the failure mode
	// that hides longest, because the happy path never touches it.
	logger = orDiscard(logger)

	// Precondition: DeliveryPointPrefix + dp was already missing. No need to remove it.
	err := unsubscribeScript.Run(r.ctx, r.client, subscriptionIndexKeys(service, subscriber, dpName), dpName, subscriber).Err()
	if err != nil {
		logger.Errorf("Error cleaning up delivery point with missing data for dp %q service %q FROM user %q's delivery points: %v", dpName, service, subscriber, err)
	}
}

// SetPushServiceProviderOfServiceDeliveryPoint will set the name of the push service provider
// to use when sending pushes to the given delivery point of this service name.
func (r *PushRedisDB) SetPushServiceProviderOfServiceDeliveryPoint(srv, dp, psp string) error {
	err := r.client.Set(r.ctx, ServiceDeliveryPointToPushServiceProviderPrefix+srv+":"+dp, psp, 0).Err()
	if err != nil {
		return fmt.Errorf("SetPSPOfServiceDP failed for \"%s:%s\": %w", srv, dp, err)
	}
	return nil
}

// RemovePushServiceProviderOfServiceDeliveryPoint is used when removing a push service provider, to clean up the association to the name of the push service provider for the delivery point+service name.
func (r *PushRedisDB) RemovePushServiceProviderOfServiceDeliveryPoint(srv, dp string) error {
	err := r.client.Del(r.ctx, ServiceDeliveryPointToPushServiceProviderPrefix+srv+":"+dp).Err()
	if err != nil {
		return fmt.Errorf("RemovePSPOfServiceDP failed for \"%s:%s\": %w", srv, dp, err)
	}
	return err
}

// GetPushServiceProvidersByService will return a list of the names of push service providers belonging to the given service name
func (r *PushRedisDB) GetPushServiceProvidersByService(srv string) ([]string, error) {
	m, err := r.client.SMembers(r.ctx, ServiceToPushServiceProvidersPrefix+srv).Result()
	if err != nil {
		return nil, fmt.Errorf("GetPSPsByService failed for %q: %w", srv, err)
	}
	if m == nil {
		return nil, nil
	}
	ret := append([]string{}, m...)
	return ret, nil
}

// RemovePushServiceProviderFromService will remove the given push service provider from the list of services (and remove the service from the list of services, if this results in the service having 0 subscriptions)
func (r *PushRedisDB) RemovePushServiceProviderFromService(srv, psp string) error {
	err := r.client.SRem(r.ctx, ServiceToPushServiceProvidersPrefix+srv, psp).Err()
	if err != nil {
		return fmt.Errorf("RemovePSPFromService failed for psp %q of service %q: %w", psp, srv, err)
	}
	// A service name can be associated with multiple push service providers, so we must first check if there are no more push service providers of that type
	// The API /addpsp allows psps with the same service name but different pushservicetypes (e.g. gcm, apns).
	exists, err := r.client.Exists(r.ctx, ServiceToPushServiceProvidersPrefix+srv).Result()
	if err != nil {
		return fmt.Errorf("unable to determine if service %q still exists after removing psp %q: %w", srv, psp, err)
	}
	if exists == 0 {
		err := r.client.SRem(r.ctx, ServicesSet, srv).Err() // Non-essential. Used to list services in API.
		if err != nil {
			return fmt.Errorf("unable to remove %q from set of services: %w", srv, err)
		}
	}
	return nil
}

// AddPushServiceProviderToService will add the push service provider's name to the list of PSPs for this service.
func (r *PushRedisDB) AddPushServiceProviderToService(srv, psp string) error {
	// TODO: pipelined
	err := r.client.SAdd(r.ctx, ServicesSet, srv).Err() // Used to list services in API.
	if err != nil {
		return fmt.Errorf("unable to add %q to set of services: %w", srv, err)
	}
	err = r.client.SAdd(r.ctx, ServiceToPushServiceProvidersPrefix+srv, psp).Err()
	if err != nil {
		return fmt.Errorf("AddPSPToService failed for psp %q of service %q: %w", psp, srv, err)
	}
	return nil
}

// setProviderAttempts is how many times a contended provider update is retried
// before giving up.
//
// Each retry means another client wrote the service's provider set between this
// one's read and its EXEC. That is rare -- it takes two concurrent /addpsp
// calls against the same service -- so a handful of attempts is the difference
// between "lost a race" and "something is wrong", and looping forever would
// turn a hot spot into a hang.
const setProviderAttempts = 5

// SetPushServiceProviderOfService installs psp as a provider of srv, removing
// whatever decide names, atomically.
//
// WATCH on the service's provider set, read, then MULTI/EXEC: if any other
// client touches that set in between, EXEC fails and the whole thing is retried
// from the read. Without it this is four separate commands, and an interruption
// in the middle leaves a service with two providers of one push service type --
// the one state where deriving a delivery point's provider is ambiguous, which
// is to say the state this branch exists to make impossible.
//
// The reads inside the transaction go through tx, so they hit the master even
// where a read replica is configured. The existing conflict check reads the set
// through the ordinary client, and therefore through the replica: replication
// lag alone could let a duplicate provider through.
func (r *PushRedisDB) SetPushServiceProviderOfService(srv string, psp *push.PushServiceProvider,
	decide func([]ServiceProvider) ([]string, error)) error {
	setKey := ServiceToPushServiceProvidersPrefix + srv

	attempt := func(tx *redis.Tx) error {
		names, err := tx.SMembers(r.ctx, setKey).Result()
		if err != nil && !errors.Is(err, redis.Nil) {
			return fmt.Errorf("could not read the providers of service %q: %w", srv, err)
		}

		existing := make([]ServiceProvider, 0, len(names))
		for _, name := range names {
			entry := ServiceProvider{Name: name}
			value, e := tx.Get(r.ctx, PushServiceProviderPrefix+name).Bytes()
			switch {
			case errors.Is(e, redis.Nil) || (e == nil && len(value) == 0):
				// Left as a nil Provider: a name with no record. decide is what
				// says whether that matters.
			case e != nil:
				return fmt.Errorf("could not read push service provider %q of service %q: %w", name, srv, e)
			default:
				entry.Provider, e = r.keyValueToPushServiceProvider(value)
				if e != nil {
					return fmt.Errorf("could not parse push service provider %q of service %q: %w", name, srv, e)
				}
			}
			existing = append(existing, entry)
		}

		superseded, err := decide(existing)
		if err != nil {
			return err
		}

		_, err = tx.TxPipelined(r.ctx, func(pipe redis.Pipeliner) error {
			pipe.Set(r.ctx, PushServiceProviderPrefix+psp.Name(), pushServiceProviderToValue(psp), 0)
			pipe.SAdd(r.ctx, ServicesSet, srv) // Used to list services in API.
			pipe.SAdd(r.ctx, setKey, psp.Name())
			for _, old := range superseded {
				// Superseding the provider being installed would delete the
				// record just written. decide should not name it, but the cost
				// of being sure here is one comparison.
				if old == psp.Name() {
					continue
				}
				pipe.SRem(r.ctx, setKey, old)
				pipe.Del(r.ctx, PushServiceProviderPrefix+old)
			}
			return nil
		})
		return err
	}

	for i := 0; i < setProviderAttempts; i++ {
		err := r.client.Watch(r.ctx, attempt, setKey)
		if err == nil {
			return nil
		}
		if errors.Is(err, redis.TxFailedErr) {
			continue
		}
		return err
	}
	return fmt.Errorf("could not set the provider of service %q: the service was modified by something else %d times in a row",
		srv, setProviderAttempts)
}

// Ping reports whether redis is reachable.
//
// One round trip and no keys read, so it is safe to call as often as a load
// balancer likes. Bounded by the client's own read and dial timeouts, which
// matters more than the cost: the caller is a health check, and a health check
// that hangs is worse than one that fails.
func (r *PushRedisDB) Ping() error {
	if err := r.client.Ping(r.ctx).Err(); err != nil {
		return fmt.Errorf("could not reach redis: %w", err)
	}
	return nil
}

// GetServiceNames will return the list of all services that have 1 or more push service providers.
func (r *PushRedisDB) GetServiceNames() ([]string, error) {
	serviceList, err := r.client.SMembers(r.ctx, ServicesSet).Result()
	if err != nil {
		return nil, fmt.Errorf("could not get services from redis: %w", err)
	}
	return serviceList, nil
}

// RebuildServiceSet builds the set of unique service. It should only be needed for migrating from old uniqush installations.
func (r *PushRedisDB) RebuildServiceSet() error {
	// Walk the provider keys, then add the services they name to the set. If
	// any step fails, then return an error.
	//
	// Collected whole rather than streamed: there is one key per provider per
	// service, tens of them on a large deployment, and the lookup below wants
	// them in one call. Deduplicated because a repeat would fetch and parse the
	// same provider twice for a set membership it already has.
	pspKeys, err := r.scanUniqueKeys(PushServiceProviderPrefix + "*")
	if err != nil {
		return fmt.Errorf("failed to scan for PSPs: %w", err)
	}

	if len(pspKeys) == 0 {
		return nil
	}

	pspNames := make([]string, len(pspKeys))
	N := len(PushServiceProviderPrefix)
	for i, key := range pspKeys {
		if len(key) < N || key[:N] != PushServiceProviderPrefix {
			return fmt.Errorf("SCAN MATCH %s* returned %q - this shouldn't happen", PushServiceProviderPrefix, key)
		}
		pspNames[i] = key[N:]
	}

	psps, errs := r.GetPushServiceProviderConfigs(pspNames)
	if len(errs) > 0 {
		return fmt.Errorf("RebuildServiceSet: found one or more invalid psps: %v", errs)
	}
	serviceNameSet := make(map[string]bool)
	for i, psp := range psps {
		serviceName, ok := psp.FixedData["service"]
		if !ok || serviceName == "" {
			return fmt.Errorf("RebuildServiceSet: found PSP %q with empty service name: data=%v", pspNames[i], psp)
		}
		serviceNameSet[serviceName] = true
	}
	var serviceNameList []interface{}
	for serviceName := range serviceNameSet {
		serviceNameList = append(serviceNameList, serviceName)
	}
	if len(serviceNameList) > 0 {
		err := r.client.SAdd(r.ctx, ServicesSet, serviceNameList...).Err()
		if err != nil {
			return err
		}
	}
	return nil
}

// FlushCache will ensure that redis data has been saved to disk.
func (r *PushRedisDB) FlushCache() error {
	// TODO: Make this configurable, allow uniqush configs to prevent redis flushes, e.g. if redis backups are set up already.
	return r.client.Save(r.ctx).Err()
}

// GetSubscriptions will fetch the subscriptions of the given subscriber belonging to the given service list.
// If queryServices is empty, then this will fetch subscriptions from all known services.
func (r *PushRedisDB) GetSubscriptions(queryServices []string, subscriber string, logger log.Logger) ([]map[string]string, error) {
	logger = orDiscard(logger)
	if len(queryServices) == 0 {
		definedServices, err := r.GetServiceNames()
		if err != nil {
			return nil, fmt.Errorf("GetSubscriptions: %w", err)
		}
		queryServices = definedServices
	}

	var serviceForDeliveryPointNames []string
	var deliveryPointNames []string
	for _, service := range queryServices {
		if service == "" {
			logger.Errorf("empty service defined")
			continue
		}

		deliveryPoints, err := r.client.SMembers(r.ctx, deviceSetKey(service, subscriber)).Result()

		if err != nil {
			return nil, fmt.Errorf("could not get delivery points for \"%s:%s\": %w", service, subscriber, err)
		}
		if len(deliveryPoints) == 0 {
			// it is OK to not have delivery points for a service
			continue
		}
		for _, deliveryPointName := range deliveryPoints {
			deliveryPointNames = append(deliveryPointNames, deliveryPointName)
			serviceForDeliveryPointNames = append(serviceForDeliveryPointNames, service)
		}
	}

	if len(deliveryPointNames) == 0 {
		// Return empty map without error.
		return make([]map[string]string, 0), nil
	}

	deliveryPointData, err := r.mgetRawDeliveryPoints(deliveryPointNames...)
	if err != nil {
		return nil, err
	}

	// Unserialize the subscriptions. If there are any invalid subscriptions, remove them and log it.
	// serviceForDeliveryPointNames, deliveryPointNames, and deliveryPointData all use the same index i.
	var subscriptions []map[string]string
	for i, data := range deliveryPointData {
		dpName := deliveryPointNames[i]
		service := serviceForDeliveryPointNames[i]
		if data != nil {
			subscriptionData, err := push.UnserializeSubscription(data)
			if err != nil {
				logger.Errorf("Error unserializing subscription for delivery point data for dp %q user %q service %q data %v: %v", dpName, subscriber, service, subscriptionData, err)
				continue
			}
			// DeliveryPointID is for use by clients which wish to remove subscriptions unambiguously
			subscriptionData[DeliveryPointID] = dpName
			subscriptions = append(subscriptions, subscriptionData)
		} else {
			logger.Errorf("Redis error fetching subscriber delivery point data for dp %q user %q service %q, removing...", dpName, subscriber, service)
			// The multi-get returned nil, so this key is missing.
			// Try to remove this delivery point as cleanly as possible, removing counts, etc.
			r.RemoveMissingDeliveryPointFromServiceSubscriber(service, subscriber, dpName, logger)
		}
	}

	return subscriptions, nil
}
