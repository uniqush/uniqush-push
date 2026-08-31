# Verifying the APNs backend

APNs is the one backend uniqush cannot currently verify. The HTTP/2 repairs in
[#278](https://github.com/uniqush/uniqush-push/pull/278) are covered by unit
tests against a mocked APNs, but nobody has run them against Apple's servers
with real credentials and a real device.

That gap is not going to close on its own, because **Apple charges for the
ability to close it**. Push Notifications is not among the capabilities
available to a free Apple ID: enabling it needs an App ID with the APS
entitlement, which needs a paid Apple Developer Program membership ($99/year).
That is true for both authentication methods — a `.p12` certificate and a `.p8`
token key are both minted from a paid account. There is no free tier and no
legitimate way around it.

So the plan is to get as close as possible without an account, and to make it
trivial for someone who *has* an account to cover the rest.

This document describes three pieces of work. They are independent and roughly
ordered by value per hour spent.

---

## 1. Point the HTTP/2 client somewhere other than Apple

**Why:** everything else here depends on it, and today it is impossible.

Two things are hardcoded:

- `srv/apns/http_api/processor.go:184-192` picks between
  `api.push.apple.com` and `api.development.push.apple.com` by string-matching
  the *binary protocol* `addr` field for `"sandbox"` or `"api.development."`.
  There is no way to express any other host. The existing `// TODO: Allow
  specifying http2 addr without string matching heuristics` is this problem.
- `createTLSConfig` at `srv/apns/http_api/processor.go:103-114` hardcodes
  `InsecureSkipVerify: false` and accepts no custom CA, so the `skipverify`
  option that `/addpsp` records is silently ignored on the HTTP/2 path.

The consequence is worth stating plainly: `srv/apns/apns-test/apns-test.sh` and
the `uniqush/apns-simulator` it drives only ever exercised the **binary**
protocol — the one Apple switched off on 31 March 2021. The repaired code path
has no end-to-end test at all, and cannot have one.

**What to do:**

- Add an explicit endpoint setting to the APNs provider, recorded in
  `VolatileData` by `buildBinaryPushServiceProviderFromMap` in
  `srv/apns/push_service.go`. Keep deriving the Apple hosts from `sandbox` /
  `addr` when it is absent, so no existing provider changes behaviour.
- Honour `skipverify` in `createTLSConfig`, and ideally accept a CA bundle so a
  local simulator can be trusted properly rather than by disabling verification.
- Reject a non-Apple endpoint unless `skipverify` or an explicit CA is set, so
  a typo cannot quietly redirect production pushes.

Note the security shape here. For every other backend the destination host is a
constant compiled into uniqush; making it configurable moves APNs closer to the
webpush backend, where the destination comes from outside. It is not as exposed
as webpush — this value comes from the operator via `/addpsp`, not from an
arbitrary `/subscribe` caller — but the SSRF reasoning in the webpush backend is
worth rereading before choosing where the value may come from.

---

## 2. A local APNs conformance simulator

**Why:** it verifies everything except Apple's own acceptance, needs no account,
and runs in CI on every commit.

`uniqush/apns-simulator` exists but speaks the dead binary protocol. What is
needed is an HTTP/2 server that implements Apple's documented contract strictly
enough that passing it means something:

- `POST /3/device/<lowercase hex token>`, and a 400 `BadDeviceToken` for
  anything else
- required headers: `apns-topic`, `apns-push-type`, `apns-expiration`,
  `apns-priority`, `apns-id`
- the header rules uniqush now depends on — a background push must use priority
  5, and priority 10 with `apns-push-type: background` is a 400 `BadPriority`
- lowercase header field names on the wire (HTTP/2 requires it; the comment at
  `processor.go:166` explains why uniqush writes `http.Header` entries directly
  rather than using `Set`)
- the 4096-byte payload limit, as 413 `PayloadTooLarge`
- error responses as `{"reason": "..."}` with the documented status codes
- 410 `Unregistered` with a `timestamp`
- 429 with `Retry-After`, and a GOAWAY, so the connection-level handling gets
  exercised too

Then drive the real `uniqush-push` binary against it over the REST API —
`/addpsp`, `/subscribe`, `/push` — so the test covers the whole stack rather
than just the processor. A rewritten `apns-test.sh` is the natural shape.

The assertions worth having are the ones that encode Apple's behaviour rather
than uniqush's: that a background push without `apns-push-type` would have been
silently dropped, that the four `permanentTokenFailureReasons` unsubscribe and
that `Forbidden` and `PayloadTooLarge` deliberately do not.

Prior art: [`bergusman/apnsmock-go`](https://github.com/bergusman/apnsmock-go).
Writing our own is probably still right, because the point is to encode Apple's
rules as assertions we control, but it is worth reading first.

---

## 3. A live probe against Apple's real sandbox

**Why:** it tests against genuine Apple infrastructure, costs nothing, needs no
account, and takes under an hour.

`api.sandbox.push.apple.com` answers unauthenticated requests. Confirmed:

```console
$ curl -sv --http2 -X POST https://api.sandbox.push.apple.com/3/device/aaaa -d '{}'
...
< HTTP/2 403
< apns-id: F4121B12-E0BF-3266-F079-D9253E98BF49
{"reason":"MissingProviderToken"}
```

That single exchange exercises a surprising amount: TLS to Apple, ALPN
negotiating h2, the hostname, the `/3/device/` path shape, HTTP/2 framing, and
the error-response parser running on a real Apple error body rather than a
fixture someone typed.

Build-tag it (`//go:build apns_live`) alongside `srv/fcm/live_test.go`, which is
the same idea for FCM and can be copied. Assert that a 403 comes back, that the
body parses as `APNSErrorResponse`, that the reason is one of the auth-related
values, and — importantly — that uniqush does **not** treat it as a dead token
and unsubscribe. `Forbidden` is on Apple's do-not-retry list but is deliberately
absent from `permanentTokenFailureReasons`, and this is a live check of that
distinction.

What it cannot tell you: whether a real payload is accepted or delivered.

---

## 4. Token (`.p8`) authentication

**Why:** it is what almost everyone uses now, and it widens the pool of people
who could test #5 for us.

uniqush only supports certificate authentication: `createTLSConfig` loads a
`cert`/`key` pair, and `/addpsp` requires both. Apple's token-based auth — an
ES256-signed JWT in an `authorization` header, from a `.p8` key plus a key id
and team id — is not implemented at all.

Certificates expire annually and are per-app; a `.p8` key does not expire and
works for every app in the team, which is why it is the default choice in
current documentation and in every actively maintained APNs library. Anyone
volunteering to test uniqush is likely to have a `.p8` and to find being asked
for a `.p12` odd.

**What to do:**

- Accept `authkey` (path to the `.p8`), `keyid` and `teamid` in
  `/addpsp`, as an alternative to `cert`/`key`.
- Mint a JWT with `alg: ES256`, `kid: <keyid>`, `iss: <teamid>` and `iat`, and
  send it as `authorization: bearer <jwt>`.
- Cache and refresh it. Apple rejects a token older than one hour with
  `ExpiredProviderToken`, and rejects *minting* more than one per 20 minutes
  per key with `TooManyProviderTokenUpdates`, so the refresh window has to sit
  between those two bounds — roughly every 40-50 minutes.
- Keep the credential out of `FixedData`. A provider's name is a hash of its
  fixed data and `/addpsp` refuses an update that changes it, so putting a
  rotatable credential there would make rotation impossible without
  re-subscribing every device. `srv/fcm/push_service.go` has the same
  reasoning written out for `credentialsfile`.
- `golang.org/x/oauth2` is already a dependency and `github.com/golang-jwt/jwt/v5`
  is already an indirect one, so this needs no new module.

---

## 5. The part that needs someone with an account

None of the above proves a notification arrives on an iPhone. That needs a paid
membership, and the most realistic route is asking someone who has one.

The README already invites reports. What would make that ask likely to succeed
is a kit: a script that takes a `.p8` (or `.p12`), a team id, a bundle id and a
device token, runs `/addpsp`, `/subscribe` and `/push` against a local uniqush,
and prints a filled-in report to paste into an issue. Ten minutes of a
stranger's time, rather than an open-ended favour.

Worth asking for specifically, since these are the changes with no coverage:

- an alert push to a foregrounded and a backgrounded app
- a **background** push (`apns-push-type: background`, priority 5) — the case
  that used to return 200 and then silently drop, and the main thing #278 fixed
- a VoIP push, via both `uniqush.apns_push_type=voip` and the older
  `uniqush.apns_voip=1`
- a push to a token from the other environment, which should come back
  `BadDeviceToken` and unsubscribe
- a push to an uninstalled app, which should come back 410 `Unregistered`
- a >4096-byte payload, which should be rejected locally, not by Apple

## Status

| | Needs an Apple account | Proves |
|---|---|---|
| 1. Configurable endpoint | no | nothing on its own; unblocks 2 |
| 2. Local simulator | no | protocol conformance, error mapping, the whole stack |
| 3. Live sandbox probe | no | reachability, TLS/ALPN/h2, real error parsing |
| 4. `.p8` auth | no to build, yes to test | nothing on its own; widens the pool for 5 |
| 5. Real device | yes | delivery |
