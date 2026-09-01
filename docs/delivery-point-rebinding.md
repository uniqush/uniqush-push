# Scope: unbinding delivery points from a provider's credential hash

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

**The read path deletes user data, incompletely.** In
`GetPushServiceProviderDeliveryPointPairs`, a missing provider triggers
`RemoveDeliveryPoint(dpName)`, which deletes `delivery.point:<dp>` and nothing
else. The name stays in `srv.sub-2-dp:<service>:<subscriber>`, and
`delivery.point.counter:<dp>` is leaked. Compare
`RemoveDeliveryPointFromServiceSubscriber`, which decrements the counter and
removes the set membership — the correct teardown. So the current behaviour is
both destructive and inconsistent: a garbage-collection step that creates
garbage.

**Three config options are inert.** `cachesize`, `everysec` and `leastdirty` are
parsed in `configparser.go` and describe `NewPushDatabaseOpts`, which is
commented out. Everything runs through `NewPushDatabaseWithoutCache`. Good news
for this work — there is no cache coherence problem to reason about — but the
config file promises something it does not do.

## Proposed change

### Phase 0 — stop deleting delivery points on read

Independent of everything else, and worth landing first.

A read must not destroy data. When a delivery point's provider is missing, skip
it and report it; do not delete it. If a provider is later restored, the device
starts working again instead of having quietly vanished.

Where a delivery point genuinely is orphaned (its `delivery.point:` blob is
gone), tear it down completely — counter and set membership included — rather
than half of it.

- `db/pushdb.go`, one function.
- Existing behaviour is covered by `db/missingkey_test.go`, so those tests state
  the current contract and will need to change deliberately.
- After this, `/rmpsp` followed by `/addpsp` is recoverable rather than
  destructive. That alone makes the auth migration *survivable*, though still
  manual.

**Small. Half a day, most of it in tests.**

### Phase 1 — derive the provider instead of reading it

Add a single lookup used by the read path:

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

Keep writing `srv.dp-2-psp` throughout this phase. It costs nothing and means a
rollback is a one-line revert rather than a data-repair exercise.

**Small-to-medium. A day or two.** The risk is behavioural, not structural: this
changes which provider serves a push in the ambiguous case, so the doctor
command below should come first for anyone with old data.

### Phase 2 — a doctor command

Before anyone can trust Phase 1 on real data, they need to know whether their
database contains the ambiguous case. Extend `/rebuildserviceset`, or add
`/checkdb`, to report:

- services with more than one provider of the same push service type
- `srv.dp-2-psp` entries naming a provider that no longer exists
- `srv.dp-2-psp` entries disagreeing with what the derivation would choose
- delivery points in a subscriber set with no `delivery.point:` blob
- leaked `delivery.point.counter:` keys (the Phase 0 bug's existing debris)

Read-only, and useful independently of this work — it is the tool for answering
"is my database consistent?", which today has no answer.

**Medium. Two or three days**, mostly output design and test fixtures.

### Phase 3 — let `/addpsp` replace a provider

This is the actual goal, and it is small once Phase 1 has landed.

Relax the conflict check in `AddPushServiceProviderToService` so that a provider
of the same *(service, push service type)* whose fixed data differs **replaces**
the existing one: write the new provider, add it to `srv-2-psp`, remove the old
name from the set, delete the old `push.service.provider:` record. Delivery
points are untouched, because nothing references the old name any more.

Guard it behind an explicit opt-in — `-d replace=true` — because the existing
check is also what catches an operator pasting the wrong certificate path into
the wrong service, and that protection should not be lost as a side effect.

Ordering matters here. The `redisClient` interface exposes no `MULTI` or
pipeline, so this is three separate commands and a crash mid-sequence is
possible. Write the new provider *first* and remove the old one last: an
interruption then leaves a service with two providers of one type, which the
doctor command reports and which Phase 1 resolves via the stored binding — not
zero providers, which would fail every push.

**Small. A day**, including the API surface and docs.

### Phase 4 — retire the index

Stop writing `srv.dp-2-psp`, keep reading it for one release as the
ambiguity tie-breaker, then delete both the reads and the keys.

Worth doing only after a release has shipped with Phases 1-3, so that a
downgrade remains possible. **Small, and not urgent.**

## Risks

**Multi-process deployments.** `pushDatabaseOpts` serialises everything behind
one `sync.RWMutex`, which is per process. Two uniqush instances against one
redis have no mutual exclusion at all today, and Phase 3's replace sequence is a
read-modify-write. This is a pre-existing hazard rather than a new one — the
current `/addpsp` conflict check has the same shape — but Phase 3 makes the
consequence larger. Worth stating in the docs; a redis lock is a separate piece
of work.

**Ambiguous legacy data.** Mitigated by Phase 2, and by keeping the stored
binding as a tie-breaker through Phase 4.

**Silent behaviour change.** After Phase 1, a delivery point whose stored
binding disagreed with the derivation moves to a different provider. This should
be impossible under the post-PR-#201 invariant. The doctor command exists to
turn "should be impossible" into "checked".

## Test plan

`db` tests already run against a real redis in CI, so these can be genuine
integration tests rather than mocked:

- a delivery point whose provider was removed — survives a read (Phase 0)
- a delivery point orphaned for real — fully torn down, no leaked counter
- a service with two providers of one type — reported, and resolved via the
  stored binding
- `/addpsp` with `replace=true` — subscriptions survive; the old provider record
  and set membership are gone
- `/addpsp` without it — still rejected, with the existing message
- the end-to-end case that motivated this: subscribe devices to a
  certificate-based APNs service, replace it with a `.p8` provider, and push to
  the same devices without re-subscribing

That last one is the acceptance test, and it can run against the simulator in
`srv/apns/apnstest` — no Apple account needed.

## Sizing

| Phase | Effort | Value on its own |
|---|---|---|
| 0. Stop deleting on read | ½ day | High — fixes data loss today |
| 1. Derive the provider | 1-2 days | Low alone; enables 3 |
| 2. Doctor command | 2-3 days | High — no such tool exists |
| 3. `/addpsp` replace | 1 day | **The goal** |
| 4. Retire the index | ½ day, later | Cleanup |

Roughly a week of focused work for Phases 0-3, and Phase 0 is worth doing
immediately whatever happens to the rest.

## Alternative considered

Keep the binding, and add a `/repointpsp` admin endpoint that rewrites every
`srv.dp-2-psp` entry for a service from an old provider name to a new one.

Smaller, and it solves the migration case without touching the read path. It was
rejected because it leaves the underlying arrangement intact — a provider's
credentials stay part of its identity, the read path keeps deleting delivery
points, and every future credential change needs an operator to remember to run
a repair command. It trades a week of work for a permanent operational
obligation.

Worth revisiting only if Phase 1 turns up ambiguous data in the wild that cannot
be resolved automatically.
