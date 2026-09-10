# uniqush-push REST API

Everything `uniqush-push` does is driven over plain HTTP on the address in
`[WebFrontend] addr` (default `localhost:9898`). This document is the
reference for that API as of the current version; the README has worked
examples per backend and [upgrading.md](upgrading.md) covers what changed
from 2.7.0.

## Conventions

**Requests** are form-encoded key/value pairs. `POST` with a body is the norm
(`curl -d`), and the query endpoints also accept `GET` with a query string.
Values must be URL-encoded; `curl -d` does not do that for you, so use
`--data-urlencode` for anything containing `&`, `+` or `%`.

**Responses** are JSON followed by `\r\n`, except `/version` (a plain string)
and `/nrdp` (a plain integer). Every JSON response carries a `code`, either
`UNIQUSH_SUCCESS` or one of the error codes listed at the end. HTTP status is
always 200; look at `code`.

**Names.** A `service` groups providers and subscriptions; a `subscriber` is
your identifier for a user within a service. Both accept `a-z`, `A-Z`, `0-9`,
`-`, `_`, `@` and `.`. (A few other characters are still tolerated for
compatibility; do not rely on them.)

**Push service types.** `pushservicetype` is one of `apns`, `fcm`, `gcm` (an
alias for `fcm`, kept for existing subscriptions), `adm`, `webpush` or
`unifiedpush` (two names for one backend). The type is part of every
subscription's identity, so pick one name per service and keep it.

**Authentication.** There is none. Whoever can reach this API can register
providers and decide where a service's pushes go. Bind it to localhost or put
it behind something that authenticates.

## Endpoints

| Path | Purpose |
|---|---|
| [`/addpsp`](#addpsp) | Add or update a push service provider (credentials for one backend) |
| [`/rmpsp`](#rmpsp) | Remove a push service provider |
| [`/psps`](#psps) | List every provider |
| [`/subscribe`](#subscribe) | Register a device (delivery point) for a subscriber |
| [`/unsubscribe`](#unsubscribe) | Remove a device |
| [`/subscriptions`](#subscriptions) | List a subscriber's devices |
| [`/health`](#health) | Report whether this instance can serve, as an HTTP status code |
| [`/nrdp`](#nrdp) | Count a subscriber's devices |
| [`/stats`](#stats) | Count each service's subscribers and devices |
| [`/push`](#push) | Send a notification |
| [`/previewpush`](#previewpush) | Show the payload `/push` would send, without sending it |
| [`/checkdb`](#checkdb) | Report database inconsistencies (read-only) |
| [`/rebuildserviceset`](#rebuildserviceset) | One-time migration for databases created before 2.2.0 |
| [`/rebuildsubscriberindex`](#rebuildsubscriberindex) | Build the subscriber index; needed once after upgrading an existing database |
| [`/version`](#version) | Version string |
| [`/stop`](#stop) | Shut down cleanly |

### `/addpsp`

Creates a push service provider for a service, or updates the existing one of
that type. A provider's identity is a hash of its *fixed* fields (marked
below); the other fields can be changed freely by calling `/addpsp` again.
`/addpsp` replaces the provider wholesale rather than patching it, so an
optional field omitted from a later call is cleared, not kept.

If a provider of the same type already exists for the service and its fixed
fields differ, the call is rejected as a conflict — unless `replace=true`,
which supersedes the old provider and keeps every subscription. Use that for a
credential change such as moving APNs from a certificate to a `.p8` key. Do
**not** `/rmpsp` and `/addpsp` to change credentials: in uniqush 2.7.0 and
earlier that deleted every subscription in the service.

Common parameters:

| Parameter | |
|---|---|
| `service` | Required. Fixed. |
| `pushservicetype` | Required. |
| `replace` | Optional. `true` to supersede an existing provider whose fixed fields differ. |

#### `apns`

Exactly one of the two credential forms:

| Parameter | |
|---|---|
| `cert`, `key` | Paths to the PEM certificate and private key. Fixed. Validated as a key pair at `/addpsp`. |
| `authkey`, `keyid`, `teamid` | Path to the `.p8` signing key from the developer portal, its key ID, and your team ID. Not fixed, so the key can be rotated in place. The key must be P-256. |

and:

| Parameter | |
|---|---|
| `bundleid` | The app's bundle identifier, sent as the `apns-topic`. Effectively required for HTTP/2; cleared when omitted. |
| `sandbox` | `true` to use Apple's development environment. Otherwise production. Recorded as `environment`, which is what pushes are routed by when `endpoint` is unset. |
| `addr` | The retired binary protocol's gateway address. Still accepted, so a registration script that has always sent it keeps selecting the same environment, but no longer stored: a host naming a sandbox gateway, or one of Apple's `api.development.` hosts, records `environment=development`, and anything else production. A provider registered before this and not re-registered still carries an `addr` and no `environment`, is still routed by that `addr`, and still shows it in `/psps`; re-running `/addpsp` for it is what swaps one field for the other. |
| `endpoint` | Base URL HTTP/2 pushes go to (e.g. `https://api.sandbox.push.apple.com`), with no path, query or fragment. Omitted, the destination comes from `environment`. A host outside `push.apple.com` is refused unless `allow_non_apple_endpoints=true` is set in the `[apns]` section of the config. |
| `cacert` | PEM bundle to verify `endpoint` against instead of the system roots. Read and validated at `/addpsp`. |
| `skipverify` | `true` disables certificate verification for a non-Apple `endpoint`. Refused for Apple's hosts. |

Certificate example:

    curl http://localhost:9898/addpsp -d service=myservice -d pushservicetype=apns \
      -d cert=/etc/uniqush/apns.crt -d key=/etc/uniqush/apns.key -d bundleid=com.example.app

Token example, replacing the certificate provider above without losing its subscriptions:

    curl http://localhost:9898/addpsp -d service=myservice -d pushservicetype=apns \
      -d authkey=/etc/uniqush/AuthKey_ABCDE12345.p8 -d keyid=ABCDE12345 -d teamid=TEAM123456 \
      -d bundleid=com.example.app -d replace=true

#### `fcm` and `gcm`

| Parameter | |
|---|---|
| `projectid` | The Firebase project ID. Fixed for `gcm` providers, not for `fcm` — this is what lets a provider of either name created by an older uniqush be updated in place. |
| `credentialsfile` | Path to a Firebase service-account JSON file. Read and parsed at `/addpsp`, and again at push time, so it can be rotated in place. |

    curl http://localhost:9898/addpsp -d service=myservice -d pushservicetype=fcm \
      -d projectid=my-firebase-project -d credentialsfile=/etc/uniqush/service-account.json

The legacy `apikey` parameter is gone with the legacy API it authenticated to;
see [upgrading.md](upgrading.md#fcm).

#### `adm`

| Parameter | |
|---|---|
| `clientid` | From the Amazon developer console. Fixed. |
| `clientsecret` | Likewise. Fixed. |

#### `webpush` and `unifiedpush`

| Parameter | |
|---|---|
| `vapidpublickey` | The VAPID public key, base64url. Fixed. |
| `vapidprivatekey` | The matching private key, base64url. Not fixed. |
| `subscriber` | The VAPID contact: a bare email address or an `https://` URL (not a `mailto:` URI). Fixed. |

`uniqush-push -generate-vapid-keys` prints a pair in exactly this form. The
README walks through the whole setup.

### `/rmpsp`

Removes a provider. Takes the same parameters as `/addpsp` for that type,
because the provider is identified by rebuilding it and hashing its fixed
fields: `service`, `pushservicetype`, and the fixed credential fields
(`cert`/`key` for a certificate APNs provider, `projectid` for `gcm`,
`clientid`/`clientsecret` for `adm`, and so on).

Subscriptions are not deleted. Devices whose provider is gone are skipped and
logged at push time, and pushes to them resume when a provider of that type is
added back. If what you want is to change credentials, use `/addpsp` with
`replace=true` instead.

### `/psps`

No parameters. Returns every stored provider, grouped by service, with its
fixed and volatile fields merged into one object each:

    {"services":{"myservice":[{"service":"myservice","bundleid":"com.example.app","cert":"/etc/uniqush/apns.crt", ...}]},"code":"UNIQUSH_SUCCESS"}

Intended for checking a setup. It returns credential file *paths* rather than
their contents, and reports every other field as `[redacted]` — a Web Push
provider's VAPID private key, an ADM provider's `clientsecret` and its issued
`token`, and anything a future backend stores that is not on the list of fields
this endpoint may report. A redacted field is still present in the response, so
you can see that a provider carries one. Databases created before uniqush 2.2.0 need
[`/rebuildserviceset`](#rebuildserviceset) once before this returns anything.

This is still an API with no authentication in front of it. Redaction removes
the worst of what a reader gains, not the reason to keep the port closed:
`/subscriptions` returns the device tokens, registration IDs and Web Push
subscriptions of any subscriber whose name is guessed, and `/push` will send
notifications to them.

### `/subscribe`

Registers a device under a subscriber. The provider it belongs to is derived
from the service and the device's `pushservicetype`, so the service must
already have a provider of that type. Calling it again with the same device
updates the optional fields.

| Parameter | |
|---|---|
| `service` | Required. |
| `subscriber` | Required. |
| `pushservicetype` | Required. |
| `devid` | Optional. Your identifier for the physical device, for spotting the same device subscribed twice (e.g. after a token change). Stored and returned; uniqush does not interpret it. |
| `old_devid` | Optional. A previous `devid`, for the same purpose across an identifier change. |
| `subscribe_date` | Optional. Unix timestamp (seconds) of this subscription, for clients that want to keep the newest. Validated as a number. |
| `app_version` | Optional. Version of the app on the device. |
| `locale` | Optional. Stored; not currently returned by `/subscriptions`. |

Plus the device identifier for its type:

| Type | Parameter | |
|---|---|---|
| `apns` | `devtoken` | The device token, hex-encoded. An optional `bundleid` overrides the provider's for this device; see below. |
| `fcm`, `gcm` | `regid` | The FCM registration token. An optional `account` is stored alongside it. |
| `adm` | `regid` | The ADM registration ID. |
| `webpush`, `unifiedpush` | `endpoint`, `p256dh`, `auth` | The push subscription: endpoint URL, client public key and auth secret, as produced by the browser or UnifiedPush connector. |

    curl http://localhost:9898/subscribe -d service=myservice -d subscriber=alice \
      -d pushservicetype=apns -d devtoken=0123456789abcdef...

The response's `details` names the `deliveryPoint` (the device's identifier,
`<pushservicetype>:<hash>`) and the `pushServiceProvider` it was bound to.

An APNs `bundleid` on `/subscribe` is for the case where one certificate serves
several bundle ids — an app and its enterprise or release-testing builds. The
device's own is sent as `apns-topic`, and the provider's is used for every
device that does not name one, so an existing setup needs no change. Sending an
empty `bundleid` clears it, putting the device back on the provider's. A push to
a device with no bundle id from either is refused, and only that device is.

### `/unsubscribe`

Same parameters as `/subscribe` (the optional fields are ignored). Removes the
device from the subscriber. The device is identified by rebuilding it from
`pushservicetype` and its token, so those are required; a `delivery_point_id`
from `/subscriptions` is not accepted here.

`alldevices=1` instead removes every device the subscriber has in the service,
and then `service` and `subscriber` are the only parameters needed — no
`pushservicetype`, no token. It is meant for an account being deleted, where the
application knows the subscriber is finished and not which devices they had.
Removing nothing is a success, and the response reports how many devices went:

    curl http://localhost:9898/unsubscribe -d service=myservice -d subscriber=alice -d alldevices=1
    {"type":"UnsubscribeResponse","date":...,"status":0,"details":{"service":"myservice","subscriber":"alice","devicesRemoved":2,"code":"UNIQUSH_SUCCESS"}}

One service per call, and no wildcards in either name: `alldevices=1` deletes
every device behind a name, so a pattern is refused rather than expanded. The
removal is not atomic, so a failure reports how many devices it had already
removed and the call can safely be retried.

### `/subscriptions`

| Parameter | |
|---|---|
| `subscriber` | Required. |
| `services` | Optional. Comma-separated list of services to look in; default is every service. |
| `include_delivery_point_ids` | Optional. `1` to include each subscription's `delivery_point_id`, for use with `/push` and `/unsubscribe`. |
| `include_subscription_secrets` | Optional. `1` to include the Web Push `auth` secret, which is withheld by default. See below. |

Returns a JSON array with one object per device, carrying `service`,
`pushservicetype`, the device identifier for its type (`devtoken`, `regid`,
or `endpoint`/`p256dh`), and whichever of `devid`, `old_devid`,
`subscribe_date` and `app_version` were set:

    curl 'http://localhost:9898/subscriptions?subscriber=alice&include_delivery_point_ids=1'
    [{"service":"myservice","pushservicetype":"apns","devtoken":"0123...","app_version":"1.2.3","delivery_point_id":"apns:5f2c..."}]

An empty array is returned when the subscriber has nothing, and also on a
database error (which is logged). Databases created before uniqush 2.2.0 need
[`/rebuildserviceset`](#rebuildserviceset) once.

A Web Push subscription's `auth` secret is withheld unless
`include_subscription_secrets=1` is passed. A `devtoken` or a `regid` names a
device and is useless without the provider credentials uniqush holds, but
`endpoint`, `p256dh` and `auth` together are everything an application server
needs to encrypt a push for that browser — with nothing of uniqush's involved,
and no way for the subscriber to tell the difference. Ask for it when you need
to move a subscription to another push server or rebuild one after a restore;
`p256dh` and `endpoint` on their own cannot encrypt anything.

### `/health`

No parameters. Reports whether this instance can serve, as an HTTP status code
and a JSON body:

    curl -i http://localhost:9898/health
    HTTP/1.1 200 OK
    {"status":"ok","database":"ok","version":"uniqush-push 2.8.0","code":"UNIQUSH_SUCCESS"}

`200` when redis answers, `503` when it does not, with the reason in
`database`. It is the only endpoint here whose status code carries the answer,
because that is what a load balancer reads.

Redis is the only thing checked. uniqush can do nothing without it — every push
reads the devices to send to, and every subscription change writes one — and
nothing else it depends on belongs in a health check: an endpoint that probed
Apple or Google would report *their* outage as this instance being unhealthy,
and a load balancer would then remove capacity in response to a failure that
removing capacity cannot fix. What each provider is doing belongs in metrics.

Point a *readiness* probe at this. A liveness probe should not be pointed at
anything that depends on another service: during a redis outage it would
restart every uniqush repeatedly, which neither fixes redis nor helps the
queue drain when it comes back.

### `/nrdp`

`service` and `subscriber`. Returns the number of devices as a bare integer.

### `/stats`

| Parameter | |
|---|---|
| `service` | Optional. Comma-separated list of services; default is every service. |
| `since` | Optional. A unix timestamp. Adds `subscribers_since`, a count of the subscribers whose most recent `/subscribe` was at or after it. |

Counts what each service holds, from the per-service index rather than by
walking the database, so it is a handful of redis commands however large the
database is.

    curl 'http://localhost:9898/stats?service=myservice&since=1756900000'
    {"services":{"myservice":{"subscribers":12034,"subscribers_since":8811,
                              "delivery_points":{"apns":9000,"fcm":4100}}},
     "code":"UNIQUSH_SUCCESS"}

`subscribers` counts subscribers with at least one device. `delivery_points`
has an entry per push service type the service has a provider for, so a
configured type with no devices reads `0` rather than being absent.
`subscribers_since` is omitted when `since` is not given.

A subscriber's timestamp is the time of their last `/subscribe`, which is what
an application calls when it launches, so it is a usable last-seen rather than
a record of when they first appeared.

Until [`/rebuildsubscriberindex`](#rebuildsubscriberindex) has been run on a
database that predates the index, this answers `UNIQUSH_ERROR_INDEX_NOT_BUILT`
rather than counts. It refuses instead of reporting what the partial index
holds, because an undercount is not distinguishable from a small service.

### `/push`

Sends one notification to every device of the named subscribers in a service.
Delivery is asynchronous per backend, but the response waits for the first
attempt at each device, so it reports what actually happened.

Addressing:

| Parameter | |
|---|---|
| `service` | Required. |
| `subscriber` (or `subscribers`) | Required. One or more subscribers, comma-separated. `*` is a wildcard: `alice.*` matches every subscriber with that prefix, and `*` alone every subscriber in the service. A wildcard is matched against the service's subscriber index, so it costs the size of the service rather than the size of the database — unless [`/rebuildsubscriberindex`](#rebuildsubscriberindex) has yet to be run, in which case it falls back to a keyspace scan and logs an error saying so. |
| `delivery_point_id` | Optional. Comma-separated `delivery_point_id`s from `/subscriptions`, to push to some of a subscriber's devices and not others. |

Content — every parameter other than the addressing ones becomes part of the
payload. These have meaning to uniqush or a backend:

| Parameter | Backends | |
|---|---|---|
| `msg` | all | The message body. For APNs this is `aps.alert.body`; elsewhere it is a key in the data payload. |
| `ttl` | apns, fcm, adm | Seconds the push service may hold the notification for an offline device. Default one hour. `0` means deliver now or never. |
| `badge` | apns | Badge count. Non-numeric values become `0`. |
| `sound` | apns | `aps.sound`. |
| `img` | apns | `aps.alert.launch-image`. |
| `title`, `title-loc-key`, `title-loc-args`, `loc-key`, `loc-args`, `action-loc-key` | apns | Placed under `aps.alert`. The two `-args` values are comma-separated lists (escape a literal comma with `\`). |
| `content-available`, `mutable-content` | apns | `aps.content-available` and `aps.mutable-content`, as numbers: `1` wakes the app in the background, and `1` runs its notification service extension so it can rewrite the notification before it is shown. A value that is not a number is dropped, because iOS ignores the string `"1"` where it wants the number `1`. |
| `category`, `thread-id`, `target-content-id`, `interruption-level` | apns | Placed in `aps` as strings: the actions the notification offers, the conversation it groups under, the window it targets, and how far it may interrupt (`passive`, `active`, `time-sensitive` or `critical`). |
| `relevance-score` | apns | `aps.relevance-score`, a number from 0 to 1, ranking the notification within its summary. |
| `msggroup` | fcm, adm | The collapse key: a newer notification with the same group replaces an undelivered older one. |
| `uniqush.priority` | fcm | `high` or `normal`, becoming FCM's `android.priority`. A normal-priority message may be held until the device next leaves Doze; a high-priority one wakes it. Omitted by default, so FCM applies its own rule (high for a notification message, normal for a data-only one). Any other value is rejected. Not read by the other backends: APNs derives its priority from `uniqush.apns_push_type`. |
| `uniqush.apns_push_type` | apns | The `apns-push-type`: `alert` (default), `background`, `complication`, `controls`, `fileprovider`, `liveactivity`, `location`, `mdm`, `pushtotalk`, `voip` or `widgets`. Also sets the priority Apple requires for that type. |
| `uniqush.apns_voip` | apns | `1` is shorthand for `uniqush.apns_push_type=voip`, and allows the 5120-byte VoIP payload limit instead of 4096. |
| `uniqush.http2` | apns | Obsolete, and ignored. `0` used to select Apple's binary protocol, which Apple shut down in 2021 and uniqush no longer implements; the push goes over HTTP/2 and the response reports that the parameter does nothing. |
| `uniqush.payload.apns` | apns | A complete APNs payload as JSON, sent verbatim instead of building one from the parameters above (`ttl` still applies). It must contain an `aps` dictionary holding either an `alert` or, for a background push, `content-available` set to the number `1`. The string `"1"` is accepted too, for compatibility with callers who worked around that check once rejecting the number, but iOS ignores it and the payload is forwarded unchanged. |
| `uniqush.payload.fcm`, `uniqush.payload.gcm` | fcm, gcm | A JSON object used as the FCM `data` payload instead of the other parameters. Every value must be a string; nested objects, numbers and booleans are rejected with a message naming the field. |
| `uniqush.notification.fcm`, `uniqush.notification.gcm` | fcm, gcm | A JSON object sent as the FCM `notification` block, which the device displays itself, alongside (or instead of) the `data` payload. |
| `uniqush.payload.adm` | adm | A JSON object used as the ADM data payload instead of the other parameters. |
| `uniqush.payload.webpush` | webpush, unifiedpush | A raw body delivered verbatim (after encryption) instead of the JSON encoding of the other parameters. |
| `uniqush.perdp.<key>` | all | Repeat the parameter to give a list of values; each provider group in the push gets the next value for `<key>`, in turn. Rarely needed. |
| anything else | all | A user-defined key, passed through to the device: as a key/value in the FCM/ADM data payload, a top-level key beside `aps` for APNs, or a field in the JSON body for Web Push. |

Names beginning with `uniqush.` are reserved. A push with no content at all is
rejected with `UNIQUSH_ERROR_EMPTY_NOTIFICATION`. APNs payloads are capped at
4096 bytes (5120 for VoIP); FCM's own limit is 4096 bytes of data.

    curl http://localhost:9898/push -d service=myservice -d subscriber=alice,bob -d msg="Hello" -d badge=1
    curl http://localhost:9898/push -d service=myservice -d subscriber=alice \
      -d uniqush.notification.fcm='{"title":"Hi","body":"Hello"}' -d uniqush.payload.fcm='{"kind":"greeting"}'

The response counts and lists the outcome per device:

    {"type":"Push","date":1756900000,"successCount":1,"failureCount":0,"droppedCount":1,
     "successDetails":[{"requestId":"...","service":"myservice","subscriber":"alice","pushServiceProvider":"apns:...","deliveryPoint":"apns:...","messageId":"...","code":"UNIQUSH_SUCCESS"}],
     "failureDetails":[],
     "droppedDetails":[{"...":"...","code":"UNIQUSH_REMOVE_INVALID_REG"}]}

`droppedDetails` are devices uniqush unsubscribed because the push service
reported the token dead (`UNIQUSH_REMOVE_INVALID_REG`) or replaced with a new
one (`UNIQUSH_UPDATE_UNSUBSCRIBE`); `modifiedDp: true` on a success means the
stored device was updated in passing (an FCM canonical-token change).
`failureDetails` entries carry an `errorMsg`. A transient failure is retried
in the background with a backoff, honouring the delay the push service asked
for up to 30 minutes; the response reports the first attempt only, and a
retry that is finally abandoned is logged with `UNIQUSH_ERROR_FAILED_RETRY`.

### `/previewpush`

Takes `pushservicetype` plus any of the content parameters of `/push`, and
returns the payload that would be sent, without sending it or touching the
database:

    curl http://localhost:9898/previewpush -d pushservicetype=apns -d msg=Hello -d badge=3
    {"code":"UNIQUSH_SUCCESS","payload":{"aps":{"alert":{"body":"Hello"},"badge":3}}}

For Web Push the preview is the plaintext before encryption.

### `/checkdb`

No parameters. Scans the whole database and reports what does not add up,
changing nothing. It walks the keyspace with `SCAN` and takes no lock, so it
can be run against a live server. Run it before upgrading a database created
before uniqush 2.6.0; see [delivery-point-rebinding.md](delivery-point-rebinding.md).

    {"services":3,"push_service_providers":4,"delivery_points":1200,"delivery_point_bindings":1200,
     "subscribers":800,"counts":{"leaked_counter":2},
     "problems":[{"kind":"leaked_counter","subject":"apns:5f2c...","detail":"..."}]}

`counts` is complete; `problems` holds at most 50 examples of each kind. The
kinds: `duplicate_provider` (a service with two providers of one type — the
one case where the provider for a device is ambiguous), `dangling_provider`
(a service's set names a provider that no longer exists), `orphaned_provider`
(a provider record no service refers to), `stale_binding` (a stored binding
pointing at a missing provider), `binding_disagrees` (a binding that differs
from the derived provider), `orphaned_delivery_point` (a subscriber's set names
a device with no record; heals on the next read), `unreferenced_delivery_point`
(a device record its own subscriber's set does not name, which an interrupted
`/subscribe` leaves behind), `leaked_counter` (a `delivery.point.counter:` key,
which nothing has written since subscribing became a redis script),
`index_not_built` (the subscriber index has never been rebuilt over this
database — run [`/rebuildsubscriberindex`](#rebuildsubscriberindex)),
`missing_index_entry` (a subscriber or device the index does not know about)
and `stale_index_entry` (an index entry with nothing behind it, which makes
[`/stats`](#stats) overcount). A summary line is logged at warning level
whenever anything is found.

### `/rebuildserviceset`

No parameters. Builds the index of service names that `/subscriptions` (with no
`services`) and `/psps` need. Only required once, on a database created before
uniqush 2.2.0. Returns `{"code":"UNIQUSH_SUCCESS"}` or an error.

### `/rebuildsubscriberindex`

No parameters. Builds the per-service subscriber index that wildcard `/push`
and [`/stats`](#stats) read, from the subscriber sets, which are the source of
truth. Only required once, on a database that predates the index; a database
created by this release or later is marked as indexed when it is first opened.

Idempotent, and safe to run against a live server: each service's index is
built under a name of its own and renamed over the live one, so a concurrent
push sees the old index or the new one and never a partial one. It takes no
lock. Run [`/checkdb`](#checkdb) afterwards to confirm; a subscription made
during the run can, rarely, be missed, and `/checkdb` names it.

Returns `{"code":"UNIQUSH_SUCCESS"}` or an error. Until it has been run,
wildcard pushes still reach the same subscribers, over a keyspace scan that is
slow on a large database and logs an error on every use, and `/stats` refuses
to answer.

### `/version`

Returns the version string, e.g. `uniqush-push 2.7.0`. The same as
`uniqush-push -version`.

### `/stop`

Waits for in-flight requests, flushes, and exits the process. Prefer this to a
signal so the cache reaches the database.

## Response codes

Every response `code` is one of:

| Code | Meaning |
|---|---|
| `UNIQUSH_SUCCESS` | Done. |
| `UNIQUSH_REMOVE_INVALID_REG` | (push, dropped) The push service said the device is gone; it has been unsubscribed. |
| `UNIQUSH_UPDATE_UNSUBSCRIBE` | (push, dropped) The device was replaced by another registration and unsubscribed. |
| `UNIQUSH_ERROR_GENERIC` | Something failed; see `errorMsg`. Provider credential or configuration errors reported by the push service land here with a message naming what to check. |
| `UNIQUSH_ERROR_EMPTY_NOTIFICATION` | `/push` with no content. |
| `UNIQUSH_ERROR_DATABASE` | Redis error. |
| `UNIQUSH_ERROR_FAILED_RETRY` | (logged, not returned) A retried push was abandoned. |
| `UNIQUSH_ERROR_BUILD_PUSH_SERVICE_PROVIDER` | `/addpsp` or `/rmpsp` parameters were invalid; `errorMsg` says which. |
| `UNIQUSH_ERROR_BUILD_DELIVERY_POINT` | `/subscribe` or `/unsubscribe` parameters were invalid. |
| `UNIQUSH_ERROR_BAD_DELIVERY_POINT` | The push service rejected the device (bad token). |
| `UNIQUSH_ERROR_UPDATE_PUSH_SERVICE_PROVIDER`, `UNIQUSH_ERROR_UPDATE_DELIVERY_POINT` | A database update after a push failed. |
| `UNIQUSH_ERROR_CANNOT_GET_SERVICE`, `UNIQUSH_ERROR_CANNOT_GET_SUBSCRIBER`, `UNIQUSH_ERROR_CANNOT_GET_DELIVERY_POINT_ID` | A required addressing parameter was missing or malformed. |
| `UNIQUSH_ERROR_NO_SUBSCRIBER`, `UNIQUSH_ERROR_NO_DEVICE`, `UNIQUSH_ERROR_NO_DELIVERY_POINT`, `UNIQUSH_ERROR_NO_PUSH_SERVICE_PROVIDER` | Nothing to push to: the subscriber, device or provider does not exist. |
| `UNIQUSH_ERROR_NO_PUSH_SERVICE_TYPE` | `/previewpush` without a `pushservicetype`. |
| `UNIQUSH_ERROR_INDEX_NOT_BUILT` | `/stats` on a database whose subscriber index has not been built. Run [`/rebuildsubscriberindex`](#rebuildsubscriberindex). |

Simple responses (`/addpsp`, `/rmpsp`, `/subscribe`, `/unsubscribe`) wrap the
details with a numeric `status`, `0` for success and `1` for failure:

    {"type":"Subscribe","date":1756900000,"status":0,"details":{"from":"127.0.0.1:52210","service":"myservice","subscriber":"alice","pushServiceProvider":"apns:...","deliveryPoint":"apns:...","code":"UNIQUSH_SUCCESS"}}
