# Unbinding delivery points from a provider's credential hash

> **Status: phases 0-4 are implemented.** An APNs service can be moved from a
> certificate to a `.p8` signing key without re-subscribing anything:
>
> ```
> curl http://localhost:9898/addpsp \
>   -d service=myservice -d pushservicetype=apns \
>   -d authkey=/etc/uniqush/AuthKey_ABCDE12345.p8 \
>   -d keyid=ABCDE12345 -d teamid=TEAM123456 \
>   -d bundleid=com.example.app \
>   -d replace=true
> ```
>
> Run `/checkdb` first on a database created before uniqush 2.6.0 — the release
> that stopped `/addpsp` accepting a second provider of one push service type
> for a service. Such a service is the only case where this release behaves
> differently from the last, and that is the report that finds it.
>
> Phase 5, retiring the `srv.dp-2-psp` index, is deliberately left for a later
> release so that a downgrade stays possible. The sections below explain what
> `/checkdb` reports and why each phase is shaped the way it is.

## The problem

A push service provider's database key is `<pushservicetype>:<sha1 of its
FixedData>`. Every delivery point is stored against that exact string, and the
read path **deletes any delivery point whose provider it cannot find**.

The consequence is that a provider's credentials are part of its identity.
Change them in a way that changes `FixedData` and the service's devices are
silently unsubscribed. This is what stops an existing certificate-based APNs
service from moving to a `.p8` signing key: the two auth modes necessarily have
different fixed data, so they are two different providers, and the obvious
workaround — `/rmpsp` then `/addpsp` — is precisely what destroys the
subscriptions.

Today `/addpsp` refuses the change outright, which is the safe answer but not a
useful one.

## What the data actually looks like

Six redis keys matter (`db/pushredisdb.go`):

| Key | Type | Holds |
|---|---|---|
| `push.service.provider:<pspName>` | string | the provider, as JSON |
| `delivery.point:<dpName>` | string | the device, as JSON |
| `srv-2-psp:<service>` | set | provider names for a service |
| `srv.sub-2-dp:<service>:<subscriber>` | set | delivery point names |
| `srv.dp-2-psp:<service>:<dpName>` | string | **the binding** |
| `delivery.point.counter:<dpName>` | string | subscriber refcount |

`srv.dp-2-psp` is the whole problem, and it has exactly three call sites in
`db/pushredisdb.go` — a get at line 429, a set at 494, a delete at 503.

### The key finding: the binding is already redundant

`AddDeliveryPointToService` (`db/pushdb.go:216`) decides which provider a
delivery point belongs to by listing the service's providers and picking the one
whose `PushServiceName()` matches the delivery point's. It then writes that
answer into `srv.dp-2-psp`.

`GetPushServiceProviderDeliveryPointPairs` reads it back.

So the stored binding is a cache of a pure function of *(service, push service
type)* — as long as a service has at most one provider per type. Which is
exactly the invariant `AddPushServiceProviderToService` has enforced since
PR #201, via the conflict check that is currently blocking us.

That makes this much smaller than "a database migration". Most of the work is
deleting an index, not populating a new one. **No backfill is required**, and
no key needs to be rewritten.

The caveat is data written before PR #201, which may have several providers of
one type in a service. There the derivation is ambiguous, and the set is
unordered, so "pick the matching one" is nondeterministic. That needs detecting
rather than guessing.

## Two bugs found while tracing this

Both are independent of the goal, and worth fixing regardless.

**The read path deletes user data, incompletely.**
`GetPushServiceProviderDeliveryPointPairs` has three garbage-collection
branches, and each is wrong in its own way:

- `delivery.point:<dp>` missing → `RemoveDeliveryPoint(dpName)`, which DELs a
  key that is already gone. A no-op that leaves the name in
  `srv.sub-2-dp:<service>:<subscriber>` and leaks
  `delivery.point.counter:<dp>`. This is the source of the debris.
- `srv.dp-2-psp` binding missing → `RemoveDeliveryPoint(dpName)`, deleting the
  blob of a perfectly valid delivery point. Destructive.
- provider missing → deletes the blob and the binding, leaves the set member
  and the counter. Destructive and incomplete.

The correct orphan teardown already exists:
`removeMissingDeliveryPointFromServiceSubscriber` (`db/pushredisdb.go:479`),
which `GetSubscriptions` uses. The two paths simply disagree. (Whether the
counter is decremented or deleted does not matter in practice: a delivery
point's name hashes `{service, subscriber, devtoken}` and subscribe uses the
same service and subscriber for the set key, so the counter never exceeds 1.)

All of these deletes also run under `dblock.RLock()`, so two concurrent reads
can both be writing. Removing the writes from the read path fixes that as a
side effect.

**Three config options are inert.** `cachesize`, `everysec` and `leastdirty` are
parsed in `configparser.go` and describe `NewPushDatabaseOpts`, which is
commented out. Everything runs through `NewPushDatabaseWithoutCache`. Good news
for this work — there is no cache coherence problem to reason about — but the
config file promises something it does not do.

## Proposed change

### Phase 0 — stop deleting delivery points on read

Independent of everything else, and worth landing first.

A read must not destroy data. When a delivery point's provider or binding is
missing, skip it and report it; do not delete it. If the provider is later
restored, the device starts working again instead of having quietly vanished.

Where a delivery point genuinely is orphaned (its `delivery.point:` blob is
gone), tear it down completely — counter and set membership included — by
calling the existing `removeMissingDeliveryPointFromServiceSubscriber`, so the
two read paths agree.

- `db/pushdb.go`, one function, plus the comment at `db/pushredisdb.go:301`
  which describes the garbage collection being removed.
- Nothing currently tests these branches. `db/missingkey_test.go` covers the
  `errors.Is` predicate and its wrapping, not what the caller does with the
  answer. Phase 0 needs new tests, not changed ones.
- After this, `/rmpsp` followed by `/addpsp` is recoverable rather than
  destructive. That alone makes the auth migration *survivable*, though still
  manual.

**Small. Half a day, most of it in tests.**

### Phase 1 — a minimal doctor command

Before anyone can trust the derivation on real data, they need to know whether
their database contains the ambiguous case. This has to ship *before* Phase 2,
not after it, so it is deliberately cut down to the two checks that gate
Phase 2:

- services with more than one provider of the same push service type
- `srv.dp-2-psp` entries disagreeing with what the derivation would choose

Add `/checkdb`. Read-only. Both checks walk `srv-2-psp:<service>` for every
service in `services{0}`, then the subscriber sets for each service. That
enumeration needs `SCAN`, not `KEYS` — `KEYS` blocks a production redis for
the duration of a full keyspace walk. `redisClient` only exposes `Keys` today
(`RebuildServiceSet` uses it); add `Scan` alongside.

**Small-to-medium. A day**, mostly test fixtures. The fuller consistency
report is Phase 4.

### Phase 2 — derive the provider instead of reading it

Add a single lookup used by **both** the read path and
`AddDeliveryPointToService`:

```go
// resolveProvider returns the provider a delivery point belongs to:
// the service's provider whose push service type matches the device's.
func (f *pushDatabaseOpts) resolveProvider(service string, dp *push.DeliveryPoint)
    (*push.PushServiceProvider, error)
```

- exactly one match — the normal case — return it
- no match — skip the delivery point with a reason naming the service and type
- more than one match — consult `srv.dp-2-psp`; if it names one of them, use it;
  otherwise report the ambiguity rather than picking arbitrarily

It must serve the write path too. `AddDeliveryPointToService` has the same
"first match in an unordered set" pick as the read path, and if only the read
path is fixed, the ambiguous case binds new subscriptions nondeterministically
while resolving reads deterministically.

Resolve once per service per call, not per delivery point: one `SMEMBERS
srv-2-psp:<service>` and one `MGET` of its providers, then a map lookup by
type. Today the read path does a `GET` of the binding and a `GET` of the
provider for every delivery point, so this is fewer round trips on the `/push`
hot path, not more.

Keep writing `srv.dp-2-psp` throughout this phase. It costs nothing and means a
rollback is a one-line revert rather than a data-repair exercise.

**Small-to-medium. A day or two.** The risk is behavioural, not structural: this
changes which provider serves a push in the ambiguous case, which is exactly
what Phase 1 exists to detect first.

### Phase 3 — let `/addpsp` replace a provider

This is the actual goal, and it is small once Phase 2 has landed.

Relax the conflict check in `AddPushServiceProviderToService` so that a provider
of the same *(service, push service type)* whose fixed data differs **replaces**
the existing one: write the new provider, add it to `srv-2-psp`, remove the old
name from the set, delete the old `push.service.provider:` record. Delivery
points are untouched, because nothing references the old name any more.

Guard it behind an explicit opt-in — `-d replace=true` — because the existing
check is also what catches an operator pasting the wrong certificate path into
the wrong service, and that protection should not be lost as a side effect.
Strip `replace` from `kv` in `restapi.go` before
`BuildPushServiceProviderFromMap` sees it. The APNs builder only reads keys it
knows, so nothing breaks today, but that is luck rather than a contract.

**Make it a transaction.** `redisClient` exposes no `MULTI` today, but it is a
project-owned subset of go-redis, and `*redis.Client` has `Watch` and
`TxPipelined`. Adding both to the interface, and forwarding them to the master
in `redisMultiClient`, is a dozen lines. Then the replace is:

```
WATCH srv-2-psp:<service>
  read the set, run the conflict check
MULTI
  SET  push.service.provider:<new>
  SADD srv-2-psp:<service> <new>
  SREM srv-2-psp:<service> <old>
  DEL  push.service.provider:<old>
EXEC
```

This is worth doing rather than ordering the writes defensively, for two
reasons. First, the "write new first, delete old last, let the doctor find the
leftovers" argument only works while `srv.dp-2-psp` still exists as a
tie-breaker; after Phase 5 an interrupted replace leaves two providers of one
type with nothing to choose between them, and every push to that service
fails. Second, `WATCH` on the set closes the multi-process race the Risks
section describes: two uniqush instances against one redis get an `EXEC`
failure instead of a silent double write. The existing conflict check has the
same race, and it also reads the set from the slave, so a replication lag can
let a duplicate through today; `WATCH` is master-only.

**Small. A day**, including the API surface and docs.

### Phase 4 — the full doctor command

Extend `/checkdb` with the rest of the consistency report:

- `srv.dp-2-psp` entries naming a provider that no longer exists
- delivery points in a subscriber set with no `delivery.point:` blob
- leaked `delivery.point.counter:` keys (the Phase 0 bug's existing debris)
- `push.service.provider:` records that are in no `srv-2-psp` set. These can
  appear after a Phase 3 replace: the push error path
  (`fixError` → `ModifyPushServiceProvider`, `pushbackend.go:235`) will
  happily resurrect the old provider's record from an in-flight push that
  started before the replace.

Still read-only, still `SCAN`. Useful independently of this work — it is the
tool for answering "is my database consistent?", which today has no answer —
but not on the critical path to the goal, which is why it is here and not
earlier.

**Medium. Two days**, mostly output design and test fixtures.

### Phase 5 — retire the index

Stop writing `srv.dp-2-psp`, keep reading it for one release as the
ambiguity tie-breaker, then delete the reads. Deleting the keys themselves is
a `SCAN srv.dp-2-psp:*` walk — a one-off `/checkdb` action, not something
that runs at startup.

Worth doing only after a release has shipped with Phases 0-3, so that a
downgrade remains possible. This is the phase that makes Phase 3's transaction
non-negotiable: once the tie-breaker is gone, nothing else can resolve two
providers of one type. **Small, and not urgent.**

## Risks

**Multi-process deployments.** `pushDatabaseOpts` serialises everything behind
one `sync.RWMutex`, which is per process. Two uniqush instances against one
redis have no mutual exclusion at all today, and the current `/addpsp` conflict
check is an unguarded read-modify-write — one that reads from the slave, when
there is one, so replication lag alone can let a duplicate through. Phase 3's
`WATCH`/`MULTI` closes this for the replace path. The rest of the write API
keeps the pre-existing hazard; a general fix is a separate piece of work.

**Ambiguous legacy data.** Mitigated by Phase 1, and by keeping the stored
binding as a tie-breaker through Phase 5.

**Silent behaviour change.** After Phase 2, a delivery point whose stored
binding disagreed with the derivation moves to a different provider. This should
be impossible under the post-PR-#201 invariant. Phase 1 exists to turn "should
be impossible" into "checked" before Phase 2 ships.

**In-flight pushes across a replace.** A push that started before Phase 3's
replace holds the old provider in memory; its retries keep using it, and a
`PushServiceProviderUpdate` from it re-creates the old record. Neither loses a
push. The resurrected record is an orphan that Phase 4 reports.

## Test plan

`db` tests already run against a real redis in CI, so these can be genuine
integration tests rather than mocked:

- a delivery point whose provider was removed — survives a read (Phase 0)
- a delivery point whose binding was removed — survives a read (Phase 0)
- a delivery point orphaned for real — fully torn down, no leaked counter, no
  stale set member
- a service with two providers of one type — reported by `/checkdb`, and
  resolved via the stored binding on both read and subscribe
- `/addpsp` with `replace=true` — subscriptions survive; the old provider record
  and set membership are gone
- `/addpsp` with `replace=true` racing a concurrent write to `srv-2-psp` —
  `EXEC` fails and nothing is half-applied
- `/addpsp` without it — still rejected, with the existing message
- the end-to-end case that motivated this: subscribe devices to a
  certificate-based APNs service, replace it with a `.p8` provider, and push to
  the same devices without re-subscribing

That last one is the acceptance test, and it can run against the simulator in
`srv/apns/apnstest` — no Apple account needed.

## Sizing

| Phase | Effort | Value on its own | State |
|---|---|---|---|
| 0. Stop deleting on read | ½ day | High — fixes data loss today | done |
| 1. Minimal doctor | 1 day | Medium — gates 2 | done, as `/checkdb` |
| 2. Derive the provider | 1-2 days | Low alone; enables 3 | done |
| 3. `/addpsp` replace | 1 day | **The goal** | done |
| 4. Full doctor | 2 days, later | High — no such tool exists | done, with phase 1 |
| 5. Retire the index | ½ day, later | Cleanup | deferred a release |

Roughly a week of focused work for Phases 0-3, and Phase 0 is worth doing
immediately whatever happens to the rest.

## What landed, against what was planned

Three things came out differently.

**The orphan cleanup moved out from under the read lock.** `pushDatabaseOpts`
guards everything with a `sync.RWMutex`, and the old code cleaned up orphans
while holding `RLock`, which admits concurrent readers. Completing the teardown
would have made that a double teardown whenever two readers found the same
orphan. The read pass now collects orphans and cleans them up afterwards under
the write lock, re-checking each one.

**Phase 4 landed with Phase 1.** The plan split the doctor in two to keep the
critical path short, but the remaining checks turned out to be a few lines each
on scaffolding the first two already needed. Splitting them would have cost
more than writing them. `/checkdb` also grew a check the plan did not list —
provider records in no service's set, which is what an in-flight push can
resurrect across a replace.

**`/checkdb` reports rather than repairs.** The scope left this open; it is
worth being explicit. Every problem it finds already has an operation that
fixes it, and a repair running unattended against a database nobody has looked
at is how a consistency check becomes an outage. It is also what makes it safe
to run against production — a check that repairs as it goes is one nobody dares
run on the database they are actually worried about.

The replace is the transaction the plan asked for, and the ordering argument it
replaced is gone: there is no interrupted state left to reason about, and
`WATCH` on the provider set means a second uniqush process gets a failed `EXEC`
rather than a silent double write.

## Still open

**Multi-process deployments, everywhere else.** `dblock` is per process, so two
uniqush instances against one redis have no mutual exclusion. Phase 3 closes
this for the replace path only. The rest of the write API keeps the
pre-existing hazard; a general fix is separate work.

**Phase 5.** Stop writing `srv.dp-2-psp`, then delete the keys. Wait for a
release with phases 0-4 in it, and for `/checkdb` to report
`binding_disagrees=0` on the databases that matter.

## Alternatives considered

**`/repointpsp`.** Keep the binding, and add an admin endpoint that rewrites
every `srv.dp-2-psp` entry for a service from an old provider name to a new
one. Smaller, and it solves the migration case without touching the read path.
Rejected because it leaves the underlying arrangement intact — a provider's
credentials stay part of its identity, and every future credential change
needs an operator to remember to run a repair command. It trades a week of
work for a permanent operational obligation.

**`/addpsp replace=true` rewrites the bindings itself.** The same rewrite,
done automatically as part of the replace, so no operator has to remember
anything. This is the more serious alternative and it is rejected on different
grounds: there is no index from a service to its delivery points, so the
rewrite has to enumerate `srv.sub-2-dp:<service>:*` — a keyspace walk on an
admin call, O(subscribers) commands behind a `SCAN`, and unbounded time during
which pushes go to whichever provider each device's binding currently names.
It also keeps an index that Phase 2 shows is redundant, so every future bug in
this area has two sources of truth to disagree between. Derivation costs
nothing at replace time and one `SMEMBERS` at read time.

Either is worth revisiting only if Phase 1 turns up ambiguous data in the wild
that cannot be resolved automatically.
