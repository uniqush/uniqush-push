# Upgrading from 2.7.0

2.8.0 repairs the two backends whose upstream APIs were shut down while the
project was dormant, adds a third, and changes how a delivery point finds its
provider. [NEWS.md](../NEWS.md) lists every change in one line each; this
document is the longer version for operators, and points at the documents
that go deeper still. [api.md](api.md) is the reference for the API as it is
now.

## Before you start

- **Go 1.25 or newer is required to build** (2.7.0 built with 1.14). The
  APNs HTTP/2 client is backed by `golang.org/x/net`, which was updated from a
  March 2020 revision to v0.57.0 -- picking up every HTTP/2 hardening fix since,
  including CVE-2023-44487 (rapid reset) and CVE-2023-45288 (CONTINUATION
  flood) -- and a non-vulnerable version of it is not reachable from Go 1.24.
- **If your database was created before uniqush 2.6.0, run `/checkdb` first.**
  2.6.0 is the release that stopped `/addpsp` accepting a second provider of one
  push service type for a service, and a service that still has two is the one
  case this release handles differently from the last. See
  [Database](#database) below.
- No device has to re-subscribe for any change in this release.

## APNs

Between them these changes are the difference between iOS notifications
arriving and silently not arriving; anyone running uniqush for APNs should treat
this as a required upgrade.

**They have not been verified against a real device.** They are covered by a
conformance suite that drives the real HTTP/2 transport against a simulator
enforcing Apple's documented contract, and by a live probe against Apple's
sandbox, but delivery to a device needs a paid Apple Developer Program
membership. [apns-verification-plan.md](apns-verification-plan.md) describes
what is covered and what is not; reports from anyone who can run the rest are
very welcome.

### HTTP/2 is the default transport

2.7.0 used Apple's binary protocol unless a push passed `uniqush.http2=1`.
Apple shut the binary protocol down on 31 March 2021, so the default path could
not deliver anything. HTTP/2 is now the default; `uniqush.http2=0` still selects
the binary protocol and logs a deprecation warning, and that fallback will be
removed in a future release.

### Headers that Apple now requires

- `apns-push-type` is sent on every request. Apple has required it on watchOS
  since watchOS 6 and recommends it everywhere. Its absence is worst for
  background pushes: on iOS 13 and later APNs accepts the request, returns 200,
  and then discards the notification, so the failure never appeared in logs.
- `apns-priority` is derived from the push type instead of being hardcoded to
  10. Apple's documentation for background pushes says "Always use priority 5.
  Using priority 10 is an error", and APNs enforces it with a 400
  `BadPriority`, so background pushes were rejected outright.
- `apns-id` is sent, unique per notification. APNs generates one when it is
  omitted, but only returns it in a response uniqush did not keep, which made
  "did this specific push arrive" unanswerable.

`uniqush.apns_push_type` can be set on `/push` to choose the push type. Valid
values are `alert` (the default), `background`, `complication`, `controls`,
`fileprovider`, `liveactivity`, `location`, `mdm`, `pushtotalk`, `voip` and
`widgets`. An unrecognised value is rejected by uniqush rather than sent on for
APNs to answer with an opaque 400. The older `uniqush.apns_voip=1` continues to
work and implies `voip`.

### Failures are classified

2.7.0 handled `BadDeviceToken` and a bare 410, and turned every other
non-permanent reason into a `BadNotification` -- so a 503 from Apple, or a
wrong signing key, was reported as though the payload were malformed, and the
push was dropped. Now:

- **Permanent token failures unsubscribe the device.** `Unregistered`,
  `ExpiredToken` and `DeviceTokenNotForTopic` join `BadDeviceToken` and 410,
  so dead tokens no longer accumulate. Provider and payload errors such as
  `PayloadTooLarge`, `BadTopic` and `BadCertificate` deliberately do *not*
  unsubscribe, since they indicate a problem on our side rather than a dead
  device.
- **Transient conditions are retried** with a backoff: `TooManyRequests`,
  `InternalServerError`, `ServiceUnavailable`, `Shutdown`, and
  `TooManyProviderTokenUpdates` (see below).
- **Credential and configuration failures are reported against the provider**
  with a message naming what to check: `InvalidProviderToken`,
  `ExpiredProviderToken`, `BadCertificate`, `Forbidden`, `BadTopic` and
  friends.

Known limitation: a 410 response carries a `timestamp` recording when APNs
last saw the token as invalid, and Apple's guidance is to keep the subscription
if the device re-registered the same token after that point. Acting on it
needs a reliable per-delivery-point registration time, which uniqush does not
yet track consistently, so the token is currently dropped unconditionally.

### Token (`.p8`) authentication

`/addpsp` accepts `authkey` (the path to the `.p8` from the developer portal),
`keyid` and `teamid` as an alternative to `cert` and `key`. A `.p8` does not
expire and covers every app in the team, unlike a certificate, which expires
annually and is per-app. The key must be P-256, since ES256 accepts nothing
else; a P-384 key is rejected at `/addpsp` rather than on the first push.

Two things about the implementation matter operationally, and both are
explained in full in [apns-verification-plan.md](apns-verification-plan.md#token-p8-authentication)
and [adr/0001-deterministic-apns-provider-tokens.md](adr/0001-deterministic-apns-provider-tokens.md):

- The token is refreshed every 35 minutes, a number bounded on both sides by
  Apple (an hour's lifetime, a 20-minute floor between mints per key) rather
  than a tuning choice. The cache is keyed on the signing key, not the
  provider or the path, because Apple's limit is per key.
- Tokens are signed deterministically with `iat` quantised into 35-minute
  buckets, so every process, restart and additional instance computes the same
  token for the same bucket. **You can run as many uniqush instances as you
  like against one `.p8` with nothing shared between them.** This needed
  `srv/apns/es256`, a small RFC 6979 implementation, because `crypto/ecdsa`
  cannot sign deterministically.

`TooManyProviderTokenUpdates` is retried after Apple's 20-minute floor rather
than failing the push, because the previous bucket's token -- the one Apple
actually saw, still valid -- can be recomputed and presented in the meantime.

**To move an existing certificate-based service to `.p8`, add `replace=true`
to the `/addpsp` call.** Do **not** use `/rmpsp` followed by `/addpsp`: in
2.7.0 that silently unsubscribed every device in the service. See
[Database](#database).

### Endpoint and certificate verification

`/addpsp` for `apns` accepts `endpoint` (the base URL HTTP/2 pushes go to) and
`cacert` (a PEM bundle to verify it against), which together make it possible
to point uniqush at a simulator or a relay without disabling certificate
verification. A provider that sets neither keeps sending exactly where it did
before: the environment is still inferred from the binary protocol's `addr`.
Both are cleared when omitted from a later `/addpsp`, the same way `bundleid`
has always behaved.

Two things are refused that were not before:

- **A non-Apple `endpoint`, unless `allow_non_apple_endpoints=true` is set in
  the `[apns]` section of uniqush.conf.** An endpoint decides where every push
  for a service goes, carrying device tokens and payloads, and on a certificate
  provider it is where the client certificate is presented; without a gate,
  anyone who can reach `/addpsp` could redirect a service's whole push stream.
  The setting is checked at push time as well as at registration, because a
  provider loaded from redis never passes through the code that validates it.
  Nothing existing is affected: `endpoint` is new, so no stored provider has
  one.
- **`skipverify` for Apple's own hosts.** It predates the HTTP/2 path and was
  silently ignored there, so an operator who set it years ago for the
  binary-protocol simulator still has it stored, and honouring it now would
  have disabled certificate verification on connections to Apple. The check
  normalises the hostname through IDNA before matching `push.apple.com`.

The [README](../README.md#apns) has the worked examples.

## FCM

### The legacy API is gone

2.7.0 posted to `https://fcm.googleapis.com/fcm/send` with an
`Authorization: key=` server key. Google decommissioned that endpoint on
20 June 2024; it now answers with an HTML 404, so every Android push has been
failing since. `fcm` now uses FCM's HTTP v1 API, which changes three things.
The first two need action from operators.

1. **Auth.** A static server key is replaced by an OAuth2 token minted from a
   Firebase service account. `/addpsp` takes `projectid` and `credentialsfile`
   (a path to the service-account JSON) instead of `apikey`. The file is read at
   push time and the access token is refreshed automatically.
2. **`data` values must all be strings.** The legacy API accepted arbitrary
   JSON and uniqush passed it through, so a `uniqush.payload.fcm` containing
   numbers, booleans or nested objects used to work and now does not. uniqush
   rejects it locally with a message naming the offending field, rather than
   letting FCM answer with an opaque 400.
3. **No more multicast.** The legacy API accepted up to 1000 registration ids
   per request; v1 takes exactly one. A push to N devices is now N requests
   over a shared HTTP/2 connection. This is Google's recommended replacement
   and needs no configuration, but it changes the shape of the traffic uniqush
   generates.

`/subscribe` still takes `regid`, so no device has to re-subscribe. The
migration is one call, for either name:

    curl http://localhost:9898/addpsp \
      -d service=myservice \
      -d pushservicetype=fcm \
      -d projectid=my-firebase-project \
      -d credentialsfile=/etc/uniqush/service-account.json

[examples/fcm-demo](../examples/fcm-demo) walks through setting up a Firebase
project and verifying the result end to end.

### `gcm` is an alias for `fcm`

The two backends have been identical since 2018, when uniqush repointed gcm at
the FCM endpoint. The name is kept because a delivery point's database key is
`<pushservicetype>:<hash>`: retiring it would strand every stored gcm
subscription, unpushable and not even removable through `/unsubscribe`. A gcm
provider keeps `projectid` in its fixed data and an fcm provider does not,
exactly as before, which is what lets existing providers of either kind be
updated in place.

### Dead registrations

Only `UNREGISTERED` and `SENDER_ID_MISMATCH` remove a subscription. v1
collapses much of what the legacy API reported separately into
`INVALID_ARGUMENT` -- including an oversized payload or a non-string data
value -- so treating that as a dead device would delete working subscriptions
because of a bad payload. `QUOTA_EXCEEDED`, `UNAVAILABLE` and `INTERNAL` are
retried, honouring `Retry-After`. `THIRD_PARTY_AUTH_ERROR` is reported against
the provider, since it means the APNs certificate or web push key uploaded to
the Firebase project is wrong rather than anything about the device.

### Why not the Firebase SDK

The implementation is hand-rolled against `net/http` and `golang.org/x/oauth2`,
adding one direct dependency. Google's firebase-admin-go SDK would have pulled
in roughly 55 indirect ones -- grpc, OpenTelemetry, Firestore, Cloud Storage,
monitoring -- for a daemon that makes a single API call.

## UnifiedPush / Web Push

A new backend, registered as both `webpush` and `unifiedpush`. It is the only
one with no vendor account, certificate or API key, and it reaches de-Googled
Android devices, Linux desktops and browsers. The
[README](../README.md#unifiedpush--web-push) has setup instructions,
`uniqush-push -generate-vapid-keys`, and the SSRF note explaining why pushes to
private addresses are refused by default and how `allow_private_addresses` and
`allowed_hosts` relax that.

## Retries

A push service's own requested delay now seeds the retry schedule. In 2.7.0
the first retry was always 5 seconds and the push was abandoned once the
doubling interval passed a minute, whatever the service asked for; APNs'
`TooManyProviderTokenUpdates` carries Apple's 20-minute floor and cannot
succeed before it clears, so the old schedule spent four pointless requests and
then dropped the notification. This affects every backend, not only APNs:
`fcm`, `unifiedpush` and `adm` all derive the delay from a `Retry-After`
header, so their first retry now lands when the server said to come back.

A requested delay is capped at 30 minutes. A retry is a live goroutine holding
a timer and the notification, and neither `fcm` nor `unifiedpush` bounds what
it parses out of `Retry-After`, so without a cap a remote server could pin
uniqush's memory by answering with a very large value. The cap sits above the
longest delay any backend legitimately asks for; a request beyond it is logged
and clamped.

## Database

A delivery point is no longer bound to its provider's credentials. In 2.7.0 a
provider's name was a hash of its fixed data, every delivery point was stored
against that exact name, and the read path deleted any delivery point whose
provider it could not find. Changing a provider's credentials in a way that
changed its fixed data -- or removing it with `/rmpsp` -- therefore silently
unsubscribed every device in the service on the next push, and re-adding the
provider did not bring them back.

Three changes, in the order you meet them:

- **A read no longer deletes delivery points.** One whose provider is missing
  is skipped and logged. The one case still cleaned up is a name in a
  subscriber's set whose record has already gone, and that teardown is now
  complete (it used to leave the set entry and leak the refcount).
- **`/addpsp` accepts `replace=true`.** A provider of the same service and
  push service type whose fixed data differs now replaces the existing one
  instead of being rejected, and the service's subscriptions survive. It is
  opt-in because the conflict it bypasses also catches a certificate path
  pasted into the wrong service. The replacement is a single redis
  transaction, so it cannot leave a service holding two providers of one type.
- **`/checkdb` reports inconsistencies.** It is read-only, walks the keyspace
  with `SCAN` and takes no lock, so it is safe to run against production. It
  reports services with more than one provider of a type -- the one case where
  the new derivation is ambiguous -- along with dangling and orphaned
  providers, stale bindings, orphaned delivery points and leaked counters.

The `srv.dp-2-psp` index is still written and still consulted to break a tie,
so this release can be rolled back without repairing anything.
[delivery-point-rebinding.md](delivery-point-rebinding.md) explains what
`/checkdb` reports and why each change is shaped the way it is.

## For embedders

`http_api.HTTPPushRequestProcessor.GetClient` now returns
`(HTTPClient, func(), error)`. The second value releases the borrow and must be
called exactly once, and never on the error path, where it is nil. Borrowing is
what lets a client superseded mid-push stay alive until its last request drains
instead of being closed underneath it.

`TryGetClient` is removed. It looked providers up by name after the cache moved
to a composite key, so it had been returning nil for every caller.

`Finalize` no longer deadlocks: it took the client cache's write lock and
returned still holding it, so anything touching the cache afterwards blocked
forever.
