# 1. Deterministic APNs provider tokens

Date: 2026-09-01
Status: accepted

## Context

APNs token authentication uses a short-lived JWT, signed with the team's `.p8`
key. Apple bounds it from both sides:

- a token whose `iat` is more than **one hour** old is rejected with
  `ExpiredProviderToken`;
- minting more than **one token per signing key per twenty minutes** is rejected
  with `TooManyProviderTokenUpdates`.

Both rejections apply to the push, not just to the token, so either edge takes a
provider offline rather than degrading it.

uniqush originally cached the signed JWT in process memory and refreshed it
every 45 minutes — inside both bounds while a process keeps running.
The problem is what happens when one does not:

- **Restart.** A new process cannot obtain the token its predecessor was using.
  It can only mint, and if the restart falls inside the 20-minute window, that
  mint is refused.
- **More than one instance.** Each mints on its own schedule. Two instances
  sharing a key can exceed the floor between them without either behaving badly.

The shape of the problem is worth stating precisely, because it points at the
answer: **after a restart there exists a valid token that the process cannot
reach.** It is not a rate-limiting problem, it is a recovery problem. So the fix
is either to *share* the token or to make it *recomputable*.

## Decision

**Sign provider tokens deterministically, and quantise `iat` into 35-minute
buckets.** Every process, on every restart, independently computes a
byte-identical JWT for the current bucket. Apple sees one token per bucket
regardless of how many processes exist. Nothing is shared, nothing is locked,
and no credential is stored anywhere it was not already.

This requires deterministic ECDSA, which the standard library will not do (see
below), so `srv/apns/es256` implements RFC 6979 nonce derivation over
`filippo.io/nistec` for the curve arithmetic.

Separately and independently, **APNs failures are now classified**: transient
conditions become `push.RetryError` with a backoff, and credential or
configuration failures become `push.BadPushServiceProvider`. Previously every
non-permanent reason became a `BadNotification` and the push was dropped — APNs
had no retry handling at all, while FCM has had it since its rewrite.

### Why bucketing makes clock skew harmless

Instances round the wall clock down to a shared boundary rather than counting
from their own start time. An instance running a few seconds behind stays in the
previous bucket slightly longer and keeps using the token the others are already
using; when it crosses, it computes the token they have already computed. Skew
delays adoption of an identical string — it does not produce a second token.

**Except at a cold start.** That reasoning assumes the older token is one Apple
has already seen, which is what makes presenting it harmless. Before any
instance has pushed, nothing has been seen: two instances whose clocks straddle a
boundary then compute two *different* buckets' tokens, both unfamiliar, and
Apple refuses the second. Neither has a predecessor to fall back on, because the
bucket before is equally unfamiliar.

The window is narrow — it needs a cold start, within the clock skew of a
boundary — and it clears itself once the floor passes. It is reported as a
retryable error rather than a dropped push, and covered by
`TestMultiInstanceColdStartAcrossABoundaryWithSkew`. But "skew is harmless" is
true of a running fleet, not of one starting up, and the difference is worth
knowing before an incident rather than during one.

### What bucketing does *not* fix

An earlier draft of this ADR claimed the only exposure was a process starting
moments before a boundary — "roughly a one-second window in every 2700". That
was wrong, and wrong in the optimistic direction.

Apple measures its floor from when it **observes** a token, not from the token's
`iat`. Bucketing controls when a token is minted; it has no say in when one is
first presented. The first push of a bucket can land anywhere inside it, and if
it lands in the final 20 minutes the next boundary follows within the floor:
Apple sees one token late in a bucket and a different one shortly after, and
answers `TooManyProviderTokenUpdates`.

So the exposure is not a startup race. It is **any bucket whose first push falls
in its last 20 minutes** — most of a 35-minute bucket. For a service pushing
steadily it is rare, because the bucket's first push arrives near its start. For
a service pushing a few times an hour it approaches a coin flip, and low-traffic
services are the common case.

Retrying does not fix it either: a retry inside the floor fails for the same
reason however long it waits, and the floor outlasts any sensible retry budget.

The fix is **recovery rather than avoidance**. When Apple refuses the current
bucket's token, uniqush presents the previous bucket's token — the one Apple
actually observed, still valid, and reproducible by any instance precisely
because signing is deterministic. This is the clearest payoff from the crypto
module: without deterministic signing a second instance could not reconstruct
the token the first one sent, and there would be nothing to fall back to.

That constrains the bucket length. The previous token must still be alive when
the floor clears, the worst case being a first use at the very end of a bucket:

```
worst-case first use     u  <= bucketStart + bucket
the floor clears at      u + floor
the older token dies at  bucketStart + lifetime
therefore                bucket + floor <= lifetime
```

which bounds the bucket at `lifetime - floor` = 40 minutes. The original 45 sat
outside it, leaving a five-minute window in which the current token was refused
and the previous had expired — no usable token at all.

Sitting exactly *on* that bound is also wrong, and less obviously so. Both ends
of it are measured on **Apple's** clock, not ours: the floor starts when Apple
observes a token, while the expiry is judged against the `iat` we wrote. Any
disagreement between the two clocks — plus the time a request spends in flight —
comes straight out of the gap. With uniqush a minute behind, a fallback first
observed at local +39:30 reaches the end of the floor at local +59:00 while Apple
already considers it over an hour old; the recovery then returns
`ExpiredProviderToken` and the push is dropped as a credential failure, which is
precisely the outage the fallback exists to prevent.

So the bucket takes an explicit margin off the bound:

```
bucket + floor + skew margin <= lifetime
35m    + 20m   + 5m          == 60m
```

**35 minutes.** It still comfortably exceeds the floor at the other end, so two
promptly-observed tokens remain far enough apart. The margin is a named constant
rather than arithmetic folded into the interval, so that anyone widening the
bucket has to decide what happens to it, and both constraints are asserted in
`TestProviderTokenRefreshIntervalRespectsApplesBounds` rather than left to a
comment.

A refusal at a boundary is unavoidable, since whether the floor has passed is
only knowable by asking. What is bounded is how often that question gets asked.

The memo lives on the processor, so the guarantee is per instance and only from
the moment that instance has observed its own refusal: **one probe per instance
per boundary**, not one per fleet — there is nothing to share it through, which
is the same constraint that ruled out sharing the token itself — and not one per
push, which is what the memo prevents.

Requests already in flight when the refusal arrives are not covered by it, for
the obvious reason that they were sent before the answer existed. That is why a
batch whose bucket has not yet been confirmed sends its first push alone and lets
the rest wait: without that, every device in a batch asks simultaneously and each
one is refused. `TestConformanceLateFirstUseInABucketRecovers` covers the
sequence for a single instance,
`TestConformanceABatchAtABoundaryCostsOneRefusal` covers the batch, and
`TestMultiInstanceLateFirstUseRecoversAcrossInstances` covers the fleet.

## Alternatives considered

### A. Share the signed token through redis

Store `{jwt, iat}` under a key fingerprint, refresh under a lock, expire at
`iat + 1h`. This was the first idea and it does work.

Rejected because of a specific, concrete regression. **Redis currently holds no
usable APNs credential.** It holds the *path* to the `.p8`. An attacker with
redis access today gets device tokens and a filename; they cannot push. Storing
the JWT changes that into an hour-lived bearer credential for the entire team,
in a store that in most uniqush deployments is unauthenticated on a trusted
network and persisted to disk by RDB or AOF. That is a real widening of the
blast radius, and it would be inherited silently by every operator who turned on
token auth.

It also needs a lock, so that two instances refreshing at the same moment do not
both mint — reintroducing coordination that the chosen approach removes entirely.

### A′. Cache the token in a local file beside the `.p8`

Fixes restart only. Attractive because it adds **no new trust domain**: the
signing key is already on that filesystem, so a token derived from it is
strictly less sensitive than what sits next to it.

Rejected as the primary answer because it does nothing for multiple instances,
which is half the problem. It remains a reasonable fallback if the deterministic
signer ever has to be withdrawn.

### C alone. Handle 429 and expiry reactively

Retry on transient failures, re-mint on `ExpiredProviderToken`. Genuinely
worthwhile, and adopted — but not sufficient on its own. A freshly started
process has no previous token to fall back to, so backing off does not give it
one; it can only wait out the window. C converts an outage into a delay, it does
not remove the cause.

### Do nothing, and document it

Considered seriously. The limitation is narrow: a single instance restarted
rarely will never notice. It was rejected because the failure is silent,
affects every push while it lasts, and lands on whoever is least equipped to
diagnose it — the operator who followed the README and enabled token auth.

## Why this needs code we own

The honest summary is that Go used to look like it was about to make this
unnecessary, and then went the other way.

`crypto/ecdsa` produces "hedged" signatures, mixing entropy into the nonce. An
accepted proposal ([golang/go#64802][], milestone Go 1.24) would have produced
RFC 6979 signatures when `rand` was nil. It did not ship in that form. Measured
directly against the toolchains rather than the documentation:

- **Go 1.25.0**, the repository's floor: `ecdsa.Sign(nil, …)` panics inside
  `randutil.MaybeReadByte`. The doc states the signature "is randomized … and
  may change between calls and/or between versions".
- **Go 1.26.0**: the `Reader` argument is *ignored entirely* — "a secure source
  of random bytes is always used, and the Reader is ignored unless
  `GODEBUG=cryptocustomrand=1` is set. This setting will be removed in a future
  Go release." The package documentation now states plainly that signatures are
  not deterministic.

RFC 6979 does exist in the tree, at `crypto/internal/fips140/ecdsa`, but it is
internal and there is no exported path to it. The direction of travel is away
from letting callers influence signing randomness at all.

So the choice was between owning a small amount of cryptographic code and
abandoning determinism. We chose to own it, with the scope kept deliberately
narrow:

- **The nonce** comes from RFC 6979, a published standard with published test
  vectors. It is HMAC-SHA256 in a loop — no field arithmetic, no branching on
  secrets.
- **The curve arithmetic** comes from `filippo.io/nistec`, the constant-time
  P-256 implementation written by Go's crypto maintainer and the same code the
  standard library vendors internally as `crypto/internal/fips140/nistec`. We do
  not implement point multiplication.
- **What we wrote** is the glue between them, plus the ECDSA closing formula.

### How it is verified

The risk with hand-written crypto is an implementation that is self-consistent
and wrong: stable, verifiable, and deriving nonces nobody else derives. Every
test built on its own output would pass. So correctness is established against
external references:

| Check | What it rules out |
|---|---|
| RFC 6979 appendix A.2.5 nonce vectors | a non-standard derivation |
| RFC 6979 appendix A.2.5 `(r, s)` vectors | a wrong closing formula |
| `crypto/ecdsa.Verify` on every signature | producing invalid signatures |
| repeat signing, 32 rounds | blinding leaking into the output |
| distinct messages produce distinct `r` | nonce reuse, which leaks the key |
| sign-then-verify inside `Sign` | a fault or a bit flip reaching the wire |

The nonce vectors are tested separately from the full signature so that a
failure says *which* half is wrong.

### What is deliberately not claimed

The modular arithmetic uses `math/big` and is **not constant time**. The nonce
inversion — the one operation where a timing leak recovers the private key — is
blinded, so the value actually inverted is uniformly random and independent of
the nonce. The remaining operations are not hardened.

That is acceptable here and would not be in a general-purpose library. uniqush
signs at most one token per key per 35 minutes, on a server, over a message an
attacker does not choose and cannot request on demand. A signing oracle that
slow gives a timing attack nothing to work with. The package comment says so, so
that nobody lifts it into a context where the reasoning does not hold.

## Consequences

**Good.** Restart and multi-instance both stop minting. No new credential
exists anywhere. No lock, no shared store, no coordination. Rotating a key in
place now takes effect without a restart, because the key is read per push batch
in order to fingerprint it. APNs gains retry handling it never had.

**Bad.** We maintain ~200 lines of cryptographic code, against a standard
library that is actively removing the seam we work around. If Go ever exports
deterministic signing, this package should be deleted in favour of it — that is
the intended end state, not a permanent fixture.

**Watch.** One new dependency, `filippo.io/nistec`. If it is ever abandoned, the
fallback is `elliptic.P256().ScalarBaseMult`, which is deprecated but subject to
Go's compatibility promise and routes to the same implementation.

**Unchanged.** Certificate authentication has none of these concerns and is not
touched by any of this.

[golang/go#64802]: https://github.com/golang/go/issues/64802
