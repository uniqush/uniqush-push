uniqush-push NEWS

Unreleased
-------------------------------

### APNs: HTTP/2 endpoint is configurable, and non-Apple destinations are opt-in

- Feature: **`/addpsp` accepts `endpoint` and `cacert` for `apns`.** `endpoint`
  is the base URL HTTP/2 pushes go to, `cacert` a PEM bundle to verify it
  against. Together they make it possible to point uniqush at a simulator or a
  relay without disabling certificate verification -- which is what
  `skipverify` did, and what it is now refused for doing against Apple.

  A provider that sets neither keeps sending exactly where it used to: the
  environment is still inferred from the binary protocol's `addr`. Both are
  cleared when omitted from a later `/addpsp`, the same way `bundleid` has
  always behaved.

- Security: **a non-Apple `endpoint` is refused unless uniqush.conf permits
  it.** Set `allow_non_apple_endpoints=true` in the `[apns]` section to enable
  the capability.

  An endpoint decides where every push for a service goes, carrying device
  tokens and the notification payload, and on a certificate provider it is also
  where the APNs client certificate is presented during the handshake. Without
  a gate, anyone who can reach `/addpsp` could redirect a service's entire push
  stream to a host they control. The setting is re-checked when a push is sent
  and not only at registration, because a provider loaded from redis never
  passes through the code that validates it.

  Nothing existing breaks: `endpoint` is new in this release, so no stored
  provider has one.

- Bugfix: **`skipverify` is refused for Apple's own hosts.** It predates the
  HTTP/2 path and was silently ignored there, so an operator who set it years
  ago for the binary-protocol simulator still has it stored. Honouring it now
  would have disabled certificate verification on connections to Apple. The
  check matches on the `push.apple.com` domain and normalises the hostname
  through IDNA first, because `https://api.push.apple.com\u3002` is a URL
  net/http dials as Apple and a byte comparison does not.

- Bugfix: **`Finalize` no longer deadlocks.** It took the client cache's write
  lock and returned still holding it, so anything touching the cache afterwards
  blocked forever. Shutdown mostly hid this; a `Finalize` followed by any
  further push did not.

- **API change for embedders:** `http_api.HTTPPushRequestProcessor.GetClient`
  now returns `(HTTPClient, func(), error)`. The second value releases the
  borrow and must be called exactly once, and never on the error path where it
  is nil. Borrowing is what lets a client superseded mid-push stay alive until
  its last request drains instead of being closed underneath it.
  `TryGetClient` is removed: it looked providers up by name after the cache
  moved to a composite key, so it had been returning nil for every caller.
### APNs: token (.p8) authentication, and a way to test any of this

- Feature: **APNs providers can authenticate with a signing key instead of a
  certificate.** `/addpsp` now accepts `authkey` (the path to the `.p8` from the
  developer portal), `keyid` and `teamid` as an alternative to `cert` and `key`.
  uniqush signs an ES256 JWT and sends it as `authorization: bearer <jwt>`.

  A `.p8` does not expire and covers every app in the team, unlike a certificate
  which expires annually and is per-app.

  The token is refreshed every 35 minutes. That number is bounded on both sides
  by Apple and is not a tuning choice: a token older than an hour is rejected
  with `ExpiredProviderToken`, and minting more than one per 20 minutes for the
  same key is rejected with `TooManyProviderTokenUpdates`. The upper bound is
  the tighter of the two, at `lifetime - floor`, so that recovery from a refused
  token is always possible; see the ADR. The cache is keyed on
  a fingerprint of the signing key's public half rather than on the provider or
  on the path the key was read from: the mint limit is per key, and the same
  `.p8` reached by two paths, a symlink or a copy is one key to Apple. The key
  is also required to be P-256 at `/addpsp`, since ES256 accepts nothing else
  and a P-384 key would otherwise register cleanly and fail on the first push.

  Tokens are signed **deterministically** and `iat` is quantised into 35-minute
  buckets, so every process -- and every restart, and every additional instance
  -- computes a byte-identical token for the same bucket. Apple sees one token
  per bucket however many uniqush processes there are. Nothing is shared between
  them and no credential is stored anywhere new.

  Because Apple measures its 20-minute floor from when it *observes* a token
  rather than from the token's `iat`, a bucket whose first push lands late can
  still be refused at the following boundary. uniqush recovers by presenting the
  previous bucket's token -- the one Apple actually saw, still valid, and
  recomputable by any instance precisely because signing is deterministic -- and
  remembers the refusal so the rest of the window skips the failed attempt.

  That needed `srv/apns/es256`, a small RFC 6979 implementation over
  `filippo.io/nistec`, because `crypto/ecdsa` cannot sign deterministically and
  as of Go 1.26 ignores its `Reader` argument entirely. It is checked against
  the RFC's published P-256 test vectors rather than against itself. The
  decision and the rejected alternatives are recorded in
  `docs/adr/0001-deterministic-apns-provider-tokens.md`.

- Bugfix: **APNs failures are classified instead of all being called bad
  notifications.** Every non-permanent reason used to become a
  `BadNotification` and the push was dropped -- so a 503 from Apple, or a wrong
  signing key, was reported as though the payload were malformed.

  Transient conditions (`TooManyRequests`, `InternalServerError`,
  `ServiceUnavailable`, `Shutdown`) are now retried with a backoff, and
  credential or configuration failures (`InvalidProviderToken`,
  `ExpiredProviderToken`, `BadCertificate`, `Forbidden`, `BadTopic` and friends)
  are reported against the provider with a message naming what to check. FCM has
  done this since its rewrite; APNs had no retry handling at all.

  `TooManyProviderTokenUpdates` moves the other way in this change. The previous
  release reported it against the provider, which was the best available reading
  when nothing could be done about it. Now that the previous bucket's token can
  be recomputed and presented, it is a transient condition with a known recovery,
  so it is retried -- after Apple's floor, which is what sets the delay -- rather
  than failing the push.

  **An existing certificate-based service cannot be switched to `.p8` in
  place.** A provider's name hashes its fixed data; `cert` and `key` are part of
  that and a signing key deliberately is not, so the two auth modes produce
  different provider names and `/addpsp` rejects the second as a conflicting
  provider. `/rmpsp` followed by `/addpsp` is *not* a workaround -- a delivery
  point whose provider has gone is deleted on the next read, so it silently
  unsubscribes every device. Token auth is therefore for new services, until
  delivery points stop being bound to a provider's credential hash. Existing
  certificate providers are unaffected and keep updating in place as before.

- Feature: **`/addpsp` accepts `endpoint` and `cacert` for APNs.** The HTTP/2
  destination was previously chosen by string-matching the binary protocol's
  `addr` for "sandbox", so it could only ever be one of Apple's two hosts, and
  `skipverify` was ignored on the HTTP/2 path entirely. Between them, the
  repaired HTTP/2 code could not be pointed at a test server at all.

  Both settings are optional and neither changes an existing provider: without
  an endpoint the environment is still inferred from `addr`. `skipverify` is now
  refused for Apple's own hosts, since it disables the only check that the host
  answering for `api.push.apple.com` is Apple.

- Testing: **`go test ./srv/apns/` now drives the real HTTP/2 transport against
  a simulator** (`srv/apns/apnstest`) that enforces Apple's documented contract
  rather than accepting whatever arrives -- the path shape, the required
  headers, priority 5 for background pushes, the payload ceiling, provider token
  validity, and duplicate headers. `go test -tags apns_live
  ./srv/apns/http_api/` checks reachability and error parsing against Apple's
  real sandbox, which answers unauthenticated requests.

  Delivery to a device is still unverified and needs a paid Apple Developer
  Program membership. See `docs/apns-verification-plan.md`.

### FCM: migrated to HTTP v1

- Bugfix: **`fcm` now uses FCM's HTTP v1 API.** It previously posted to
  `https://fcm.googleapis.com/fcm/send` with an `Authorization: key=` server
  key, which Google decommissioned on **20 June 2024**. That endpoint now
  answers with an HTML 404, so every Android push has been failing since.

  Three things changed, and the first two need action from operators:

  - **Auth.** A static server key is replaced by an OAuth2 token minted from a
    Firebase service account. `/addpsp` now takes `projectid` and
    `credentialsfile` (a path to the service-account JSON) instead of `apikey`.
    The file is read at push time and the access token is refreshed
    automatically.
  - **`data` values must all be strings.** The legacy API accepted arbitrary
    JSON and uniqush passed it through, so a `uniqush.payload.fcm` containing
    numbers, booleans or nested objects used to work and now does not. uniqush
    rejects it locally with a message naming the offending field, rather than
    letting FCM answer with an opaque 400.
  - **No more multicast.** The legacy API accepted up to 1000 registration ids
    per request; v1 takes exactly one. A push to N devices is now N requests
    over a shared HTTP/2 connection. This is Google's own recommended
    replacement and needs no configuration, but it does change the shape of the
    traffic uniqush generates.

  Unchanged: `/subscribe` still takes `regid`, so **no device has to
  re-subscribe**.

- Bugfix: **Dead registrations are detected more carefully than before.** Only
  `UNREGISTERED` and `SENDER_ID_MISMATCH` remove a subscription. v1 collapses a
  lot of what the legacy API reported separately into `INVALID_ARGUMENT` --
  including an oversized payload or a non-string data value -- so treating that
  as a dead device would delete working subscriptions because of a bad payload.
  `QUOTA_EXCEEDED`, `UNAVAILABLE` and `INTERNAL` are retried, honouring
  `Retry-After`. `THIRD_PARTY_AUTH_ERROR` is reported against the provider,
  since it means the APNs certificate or web push key uploaded to the Firebase
  project is wrong rather than anything about the device.

- Maintenance: **`gcm` is now an alias for `fcm`, not a separate backend.** The
  two have been identical since 2018, when uniqush repointed gcm at the FCM
  endpoint. The name is kept because a delivery point's database key is
  `<pushservicetype>:<hash>`: retiring it would strand every stored gcm
  subscription, unpushable and not even removable through `/unsubscribe`.

  A gcm provider keeps `projectid` in its fixed data and an fcm provider does
  not, exactly as before, so that existing providers of either kind can be
  updated in place. `/addpsp` rejects an update whose fixed data changed, so
  this detail is what makes upgrading possible without re-subscribing devices.

  The one-call migration, for either name:

      curl http://localhost:9898/addpsp \
        -d service=myservice \
        -d pushservicetype=fcm \
        -d projectid=my-firebase-project \
        -d credentialsfile=/etc/uniqush/service-account.json

- Maintenance: The implementation is hand-rolled against `net/http` and
  `golang.org/x/oauth2`, adding one direct dependency. Using Google's
  firebase-admin-go SDK would have pulled in roughly 55 indirect ones -- grpc,
  OpenTelemetry, Firestore, Cloud Storage, monitoring -- for a daemon that makes
  a single API call.

### New backend: UnifiedPush / Web Push

- New provider: **`webpush`, also registered as `unifiedpush`.** Implements
  RFC 8030 delivery, RFC 8291 `aes128gcm` payload encryption and RFC 8292 VAPID
  authentication, which is what [UnifiedPush](https://unifiedpush.org/) uses
  between an application server and a push server. The same backend drives
  browser Web Push.

  This is the only uniqush backend with no vendor account, no certificate and no
  API key: the user chooses their own push provider. It reaches de-Googled
  Android devices, Linux desktops and browsers.

  Registration takes `vapidpublickey`, `vapidprivatekey` and `subscriber` on
  `/addpsp`; a subscription takes `endpoint`, `p256dh` and `auth` on
  `/subscribe`, all three of which a UnifiedPush connector library produces on
  the device. `uniqush.payload.webpush` sends a raw body verbatim. See the
  README for worked examples.

  The two names behave identically, but a `pushservicetype` is part of a
  subscription's identity, so pick one per service and stay with it.
- New feature: `uniqush-push -generate-vapid-keys` prints a fresh VAPID key
  pair. Keys are minted by the binary rather than over the REST API, which has
  no authentication.
- New feature: optional `[webpush]` / `[unifiedpush]` config sections with
  `allow_private_addresses` and `allowed_hosts`.

  **Security note.** For every other backend the destination host is a constant
  compiled into uniqush. For Web Push it is supplied by whoever called
  `/subscribe`, which makes an unguarded implementation a server-side request
  forgery primitive. uniqush therefore refuses by default to POST to addresses
  that are not globally routable — loopback, RFC 1918, carrier-grade NAT,
  link-local (including `169.254.169.254`), documentation and reserved ranges,
  and the IPv6 equivalents. Redirects are never followed, and the check runs
  before every push rather than only at subscribe time, so DNS rebinding does
  not defeat it. Self-hosted push servers on a private network are supported via
  the config options above.

### APNs

Together these are the difference between iOS notifications arriving and
silently not arriving; anyone running uniqush for APNs should treat this as a
required upgrade.

**These changes have not been verified against Apple's servers.** They are
covered by unit tests against a mocked APNs and follow Apple's current published
documentation, but no one has yet run them with real credentials against a real
device. Reports from anyone who can are very welcome.

- Bugfix: **HTTP/2 is now the default APNs transport.** Previously uniqush used
  Apple's binary protocol unless a push passed `uniqush.http2=1`. Apple shut the
  binary protocol down on 31 March 2021, so the default path could not deliver
  anything. Passing `uniqush.http2=0` still selects it and now logs a
  deprecation warning; that fallback will be removed in a future release.
- Bugfix: **Send the `apns-push-type` header.** Apple has required this on
  watchOS since watchOS 6 and recommends it everywhere. Its absence is worst for
  background pushes: on iOS 13 and later APNs accepts the request, returns 200,
  and then discards the notification, so the failure never appears in logs.
- Bugfix: **Derive `apns-priority` from the push type.** It was hardcoded to 10.
  Apple's documentation for background pushes says "Always use priority 5. Using
  priority 10 is an error", which APNs enforces with a 400 `BadPriority`, so
  background pushes were rejected outright.
- Bugfix: **Unsubscribe on the full set of permanent token failures.** Only
  `BadDeviceToken` and a bare 410 were handled. `Unregistered`, `ExpiredToken`
  and `DeviceTokenNotForTopic` now also remove the subscription, so dead tokens
  no longer accumulate. Provider and payload errors such as `PayloadTooLarge`,
  `BadTopic` and `BadCertificate` deliberately do *not* unsubscribe, since those
  indicate a problem on our side rather than a dead device.
- New feature: `uniqush.apns_push_type` can be set on `/push` to choose the push
  type. Valid values are `alert` (the default), `background`, `complication`,
  `controls`, `fileprovider`, `liveactivity`, `location`, `mdm`, `pushtotalk`,
  `voip` and `widgets`. An unrecognised value is rejected by uniqush rather than
  being sent on for APNs to answer with an opaque 400.
  The older `uniqush.apns_voip=1` continues to work and implies `voip`.
- New feature: Send an `apns-id` header, unique per notification. APNs generates
  one when it is omitted, but only returns it in a response uniqush does not
  keep, which made "did this specific push arrive" unanswerable.
- Maintenance: Update `golang.org/x/net` from a March 2020 revision to v0.57.0.
  It backs the APNs HTTP/2 client, so it predated every HTTP/2 hardening fix
  since, including CVE-2023-44487 (rapid reset) and CVE-2023-45288
  (CONTINUATION flood). `govulncheck` is now part of CI.
- Maintenance: **Building now requires Go 1.25 or newer** (was 1.14). A
  non-vulnerable `golang.org/x/net` is not reachable from Go 1.24.
- Maintenance: Replace Travis CI with GitHub Actions, and migrate
  `.golangci.yml` to the v2 config format.

Known limitation: a 410 response carries a `timestamp` recording when APNs last
saw the token as invalid, and Apple's guidance is to keep the subscription if
the device re-registered the same token after that point. Acting on it needs a
reliable per-delivery-point registration time, which uniqush does not yet track
consistently, so the token is currently dropped unconditionally.

25 Nov 2019, uniqush-push 2.7.0
-------------------------------

- Bugfix: Change from the deprecated `redis.FlushDb` alias to `redis.FlushDB` of go-redis (FlushDb is removed in the latest releases).
  This may require updating the version of go-redis that `uniqush-push` is built with
- Bugfix: Properly handle values of `sandbox` other than `sandbox=true` when creating push service providers. (#249)
  (This bug is not triggered when there is no `sandbox` query param)
- Bugfix: Fix possible incorrect subscription when sending API response for /push containing multiple subscriptions (pushes were sent correctly)
- Maintenance: Start using go modules
- Maintenance: Add documentation to source code

21 Jul 2018, uniqush-push 2.6.1
-------------------------------

- Maintenance: Fix various code style warnings from code linters (e.g. gometalinter). Refactor and document code.
- Bugfix: Fix the rare "No device" errors seen when retrying a push. (PR #222)
- Bugfix: Fix the regular expression used as a sanity check of subscriptions and services. (PR #222)
  The intended accepted characters for use in services and subscriptions were `a-z, A-Z, 0-9, -, _, @ or .`

  Forbid using the backtick in service and subscription names (this was accidentally permitted by the invalid regex).

  Continue allowing a few other invalid characters for now.
  Those may be deprecated in future releases.

18 Jul 2018, uniqush-push 2.6.0
-------------------------------

- Maintenance: Update GCM push URL to the equivalent https://fcm.googleapis.com/fcm/ endpoint (#210)
  Applications using GCM are unaffected by this change.
  (The old URL stop working in April 2019)
- Maintenance: Upgrade go-redis from v5 to v6.
- Get rid of excessive database locking when fetching subscriptions for a user.
- Make the APNS pool size configurable at runtime
- Stop overriding Gomaxprocs (removes a call to `runtime.GOMAXPROCS(runtime.NumCPU() + 1)`).
  This allows users to override this setting.
  This is no longer needed because the latest releases of Go have reasonable defaults for GOMAXPROCS.
- New feature: Add an optional `slave_host` and `slave_port` field to the uniqush db config.
  This may help with scaling if the redis master (or sharded redis masters) have high load,
  by performing read operations against the redis slave instead.

Changes to APIs:

- New feature: Prevent creating two **different** push service providers of the same service name and push service type in /addpsp. (#197)
  Updating mutable fields of existing PSP will continue to work.
- New feature: Add optional fields to subscriptions that clients can use to track information about an app with a subscription
  (`app_version`, `locale`, `subscribe_date`, `devid`, `old_devid` (device id)).

  These can be set in calls to `/subscribe`, and will be returned (if they exist) in calls to `/subscriptions`

  Note that the `subscribe_date` provided by the client must be a unix timestamp in seconds.

  - Uniqush-push currently does not use these for anything, but they are returned when fetching subscriptions.
  - `devid` can be used by clients to remove duplicate subscriptions (e.g. different regid/device token but the same device for GCM/APNS)
    if the same device id is seen in calls to /subscribe.
    (E.g. this can used in combination with subscribe_date to check which subscription was newer)
  - `old_devid` is only useful if you plan to change the way that device ids are generated in a newer release,
    and want to manually remove duplicate subscriptions if they arise (e.g. for APNS).
- If /subscriptions is called with `include_delivery_point_ids=1`, this
  will return unique string identifiers for each delivery point (as `delivery_point_id`) to use with `/push`
- Make the APNS worker pool size (for the binary API) configurable at runtime. (see example in conf/uniqush-push.conf)

  This controls the number of encrypted TCP connections to APNS (per active APNS Push Service Provider)
  that can run at a given time.

  This defaults to 13 and has a maximum of 50. The default should be reasonable for most use cases.
- `/push` now accepts an optional parameter `delivery_point_id` with a comma separated list of
  delivery point ids to push to, e.g.  `delivery_point_id="apns:abcdef0123456789"`
  to push to the single subscription with that delivery point id.

  Knowing the delivery point id allows clients to implement custom logic to invoke `uniqush-push`'s APIs.

  - For example, a client may wish to push different payloads (or not push at all)
    to endpoints running `app_version` 1.2.3 of your app or older.

    (or base the payload on the locale of the device, etc)

  This parameter only needs to be used if you want to push to some delivery points (for a subscriber) but not others.

01 Apr 2018, uniqush-push 2.5.0
-------------------------------

- Support "title", "title-loc-key", and "title-loc-args"
- Support larger APNS payloads.
  Support 5120 byte payloads for APNS voip pushes
  (Where the Cert is a VOIP cert and `uniqush.apns_voip=1` is part of
  the query params in the call to `/push`)
- Support more granular loglevel levels in uniqush config files:
  alert, error, warn/warning, standard/verbose/info, and debug.

07 Oct 2018, uniqush-push 2.4.0
-------------------------------

- New feature: Initial support for GCM/FCM "notification" pushes (Documented in PR #185).
  `uniqush.notification.gcm` and `uniqush.notification.fcm` can be used
  as fields for `/push`, with the JSON blob to send GCM/FCM for the
  optional "notification" message.
  "notification" messages will let GCM/FCM display the notification for you.
- Maintenance: Change from https://android.googleapis.com/gcm/send to
  https://gcm-http.googleapis.com/gcm/send (equivalent endpoints)
- Maintenance: Bump go version used to compile releases
- Maintenance: go 1.9+ is recommended for compiling and testing
- Bug fix: Improve logging subscriber name in failed API requests.

18 Jul 2017, uniqush-push 2.3.0
-------------------------------

+ New feature: Add /previewpush endpoint to preview the payload that would be
  generated and sent to push services. (Issue #140)
  This helps with debugging.
+ Maintenance: Update APNS binary provider API(default) from version 1 to version 2.
+ Maintenance: Upgrade to redis.v5 (Issue #143)
+ New provider: Add FCM support. (Issue #148)
  The parameters that would be provided to /addpsp, /subscribe, and /push are
  the same as they would be for GCM. (Replace "gcm" with "fcm" when following instructions)
+ New feature: Add support APNS HTTP2 API (Issue #157, PR #173)
  This gives more accurate results on whether a push succeeded,
  and should not impact Uniqush's performance.
  To set this up, call /addpsp (to create a new provider or modify an
  existing provider) with the same params you would use to create a new
  APNS endpoint for binary providers (including cert and key),
  in addition to providing `bundleid`.
  Currently, to make testing easy, each call to `/push` must be provided with
  the query param value `uniqush.http2=1`.
  Otherwise, uniqush continues to use the APNS binary provider API.
+ Maintenance: Use unescaped payloads for GCM and FCM.
  This allows larger payloads, avoiding escaping characters such as `<` and `>`

Fixes #134

go 1.8.3+ and an up to date version of golang.org/x/net/http2
are suggested (For the APNS HTTP2 API).

02 Nov 2016, uniqush-push 2.2.0
-------------------------------

- Add API endpoints for querying subscriptions (/subscriptions), available services (/services), and a migration API for building the services set (/rebuildserviceset)
- Allow for providing custom JSON payloads to ADM, APNS, and GCM
- Add feedback to indicate whether a delivery point was modified on push (thanks Clemens Fischer)
- Better connection pooling for the GCM implementation reduces memory footprint by about 90% for if(we)
- Migrate Redis implementation to redis.v3
- Automatically remove invalid PSPs if they are detected

09 Mar 2016, uniqush-push 2.1.0
-------------------------------

This release contains bugfixes, new APIs and improvements.

ChangeLog:
- _improvement_ Add new APIs for listing the subscriptions of a subscriber and for listing the services exist.
- _bugfix_ Fix concurrency issues in ADM, APNS.  Change the APNS implementation from a buggy connection pool to a reliable worker pool.
- _bugfix_ Fix a bug which would lead to an infinite loop in rare circumstances.
- _improvement_ Remove Go's default HTML escaping of JSON payloads, for APNS.  The APNS servers now render payloads with characters such as '"' properly.
- _improvement_ Add more details to error messages.
- _improvement_ Add enough buffer space for potential 100-byte APNS device tokens.

08 Mar 2016, uniqush-push 2.0.0
-------------------------------

This release contains a change to the response format, as well as bug fixes and improvements.

ChangeLog:
- _improvement_ Changed the response format of most APIs from logs to JSON.
  This allows clients to reliably parse results and errors from the API response.
  **This will break clients that parse the old format**
- _improvement_ Allow 2048 byte APNS payloads
- _bugfix_ Fix various memory leaks.
- _bugfix_ Fix bugs in closing connections.
- Remove support for C2DM, which was shut down by Google on October 2015.

Older releases
--------------

The release notes for older releases can be found at https://github.com/uniqush/uniqush-push/releases
