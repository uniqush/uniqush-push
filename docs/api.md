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
| [`/nrdp`](#nrdp) | Count a subscriber's devices |
| [`/push`](#push) | Send a notification |
| [`/previewpush`](#previewpush) | Show the payload `/push` would send, without sending it |
| [`/checkdb`](#checkdb) | Report database inconsistencies (read-only) |
| [`/rebuildserviceset`](#rebuildserviceset) | One-time migration for databases created before 2.2.0 |
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
| `sandbox` | `true` to use Apple's development environment. Otherwise production. |
| `addr` | Binary-protocol gateway address; only relevant to the deprecated binary path, but its host is still what `sandbox`/production is inferred from when `endpoint` is unset. |
| `endpoint` | Base URL HTTP/2 pushes go to (e.g. `https://api.sandbox.push.apple.com`), with no path, query or fragment. Omitted, the environment comes from `sandbox`/`addr`. A host outside `push.apple.com` is refused unless `allow_non_apple_endpoints=true` is set in the `[apns]` section of the config. |
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
their contents, but for a Web Push provider it does include the VAPID private
key itself — one more reason not to expose this API. Databases created before uniqush 2.2.0 need
[`/rebuildserviceset`](#rebuildserviceset) once before this returns anything.

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
| `apns` | `devtoken` | The device token, hex-encoded. |
| `fcm`, `gcm` | `regid` | The FCM registration token. An optional `account` is stored alongside it. |
| `adm` | `regid` | The ADM registration ID. |
| `webpush`, `unifiedpush` | `endpoint`, `p256dh`, `auth` | The push subscription: endpoint URL, client public key and auth secret, as produced by the browser or UnifiedPush connector. |

    curl http://localhost:9898/subscribe -d service=myservice -d subscriber=alice \
      -d pushservicetype=apns -d devtoken=0123456789abcdef...

The response's `details` names the `deliveryPoint` (the device's identifier,
`<pushservicetype>:<hash>`) and the `pushServiceProvider` it was bound to.

### `/unsubscribe`

Same parameters as `/subscribe` (the optional fields are ignored). Removes the
device from the subscriber. The device is identified by rebuilding it from
`pushservicetype` and its token, so those are required; a `delivery_point_id`
from `/subscriptions` is not accepted here.

### `/subscriptions`

| Parameter | |
|---|---|
| `subscriber` | Required. |
| `services` | Optional. Comma-separated list of services to look in; default is every service. |
| `include_delivery_point_ids` | Optional. `1` to include each subscription's `delivery_point_id`, for use with `/push` and `/unsubscribe`. |

Returns a JSON array with one object per device, carrying `service`,
`pushservicetype`, the device identifier for its type (`devtoken`, `regid`,
or `endpoint`/`p256dh`/`auth`), and whichever of `devid`, `old_devid`,
`subscribe_date` and `app_version` were set:

    curl 'http://localhost:9898/subscriptions?subscriber=alice&include_delivery_point_ids=1'
    [{"service":"myservice","pushservicetype":"apns","devtoken":"0123...","app_version":"1.2.3","delivery_point_id":"apns:5f2c..."}]

An empty array is returned when the subscriber has nothing, and also on a
database error (which is logged). Databases created before uniqush 2.2.0 need
[`/rebuildserviceset`](#rebuildserviceset) once.

### `/nrdp`

`service` and `subscriber`. Returns the number of devices as a bare integer.

### `/push`

Sends one notification to every device of the named subscribers in a service.
Delivery is asynchronous per backend, but the response waits for the first
attempt at each device, so it reports what actually happened.

Addressing:

| Parameter | |
|---|---|
| `service` | Required. |
| `subscriber` (or `subscribers`) | Required. One or more subscribers, comma-separated. `*` is a wildcard: `alice.*` matches every subscriber with that prefix, and `*` alone every subscriber in the service. Wildcards scan the database and are slow on large services. |
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
| `content-available` | apns | `aps.content-available`, as a number. |
| `msggroup` | fcm, adm | The collapse key: a newer notification with the same group replaces an undelivered older one. |
| `uniqush.apns_push_type` | apns | The `apns-push-type`: `alert` (default), `background`, `complication`, `controls`, `fileprovider`, `liveactivity`, `location`, `mdm`, `pushtotalk`, `voip` or `widgets`. Also sets the priority Apple requires for that type. |
| `uniqush.apns_voip` | apns | `1` is shorthand for `uniqush.apns_push_type=voip`, and allows the 5120-byte VoIP payload limit instead of 4096. |
| `uniqush.http2` | apns | `0` selects Apple's binary protocol, which Apple shut down in 2021. Deprecated; logs a warning and will be removed. |
| `uniqush.payload.apns` | apns | A complete APNs payload as JSON, sent verbatim instead of building one from the parameters above (`ttl` still applies). |
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
     "counts":{"leaked_counter":2},
     "problems":[{"kind":"leaked_counter","subject":"apns:5f2c...","detail":"..."}]}

`counts` is complete; `problems` holds at most 50 examples of each kind. The
kinds: `duplicate_provider` (a service with two providers of one type — the
one case where the provider for a device is ambiguous), `dangling_provider`
(a service's set names a provider that no longer exists), `orphaned_provider`
(a provider record no service refers to), `stale_binding` (a stored binding
pointing at a missing provider), `binding_disagrees` (a binding that differs
from the derived provider), `orphaned_delivery_point` (a subscriber's set names
a device with no record; heals on the next read) and `leaked_counter` (a
reference counter with no device). A summary line is logged at warning level
whenever anything is found.

### `/rebuildserviceset`

No parameters. Builds the index of service names that `/subscriptions` (with no
`services`) and `/psps` need. Only required once, on a database created before
uniqush 2.2.0. Returns `{"code":"UNIQUSH_SUCCESS"}` or an error.

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

Simple responses (`/addpsp`, `/rmpsp`, `/subscribe`, `/unsubscribe`) wrap the
details with a numeric `status`, `0` for success and `1` for failure:

    {"type":"Subscribe","date":1756900000,"status":0,"details":{"from":"127.0.0.1:52210","service":"myservice","subscriber":"alice","pushServiceProvider":"apns:...","deliveryPoint":"apns:...","code":"UNIQUSH_SUCCESS"}}
