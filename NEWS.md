uniqush-push NEWS

03 Sep 2026, uniqush-push 2.8.0
-------------------------------

The longer version of everything below, for operators, is in
[docs/upgrading.md](docs/upgrading.md). No device needs to re-subscribe.

APNs:

Anyone running uniqush for APNs should treat this as a required upgrade: 2.7.0
could not deliver an iOS notification at all. The changes are verified against
a conformance simulator and Apple's sandbox, but not yet against a real device;
see docs/apns-verification-plan.md.

- Bugfix: Use the HTTP/2 API by default. Apple shut the binary protocol down on 31 March 2021.
  `uniqush.http2=0` still selects it and logs a deprecation warning; it will be removed in a future release.
- Bugfix: Send the `apns-push-type` header, and derive `apns-priority` from it instead of hardcoding 10.
  Background pushes were previously either silently discarded (iOS 13+) or rejected with `BadPriority`.
- Bugfix: Classify APNs failures instead of treating every one as a `BadNotification` and dropping the push.
  Transient reasons are retried; credential and configuration reasons are reported against the provider.
- Bugfix: Also unsubscribe on `Unregistered`, `ExpiredToken` and `DeviceTokenNotForTopic`, not only `BadDeviceToken` and 410.
- Bugfix: Refuse `skipverify` for Apple's own hosts. It was silently ignored on the HTTP/2 path, so honouring it
  now would have disabled certificate verification against Apple.
- Bugfix: `Finalize` no longer deadlocks on the HTTP/2 client cache.
- New feature: Token (`.p8`) authentication. `/addpsp` accepts `authkey`, `keyid` and `teamid` as an
  alternative to `cert` and `key`. Tokens are signed deterministically, so any number of uniqush instances can
  share one key with nothing shared between them. (docs/adr/0001-deterministic-apns-provider-tokens.md)
- New feature: `uniqush.apns_push_type` on `/push` selects the push type (`alert`, `background`, `voip`, ...).
  `uniqush.apns_voip=1` still works and implies `voip`.
- New feature: Send a unique `apns-id` header per notification.
- New feature: `/addpsp` accepts `endpoint` and `cacert` for `apns`, so a simulator or relay can be used
  without disabling certificate verification.
- Security: A non-Apple `endpoint` is refused unless `allow_non_apple_endpoints=true` is set in `[apns]`.

FCM:

- Bugfix: Migrate to FCM's HTTP v1 API. Google decommissioned the legacy endpoint on 20 June 2024, so every
  Android push has been failing since. **Action required:** `/addpsp` now takes `projectid` and `credentialsfile`
  (a Firebase service-account JSON) instead of `apikey`, and all `data` values must be strings.
- Bugfix: Only `UNREGISTERED` and `SENDER_ID_MISMATCH` unsubscribe a device. v1 reports bad payloads as
  `INVALID_ARGUMENT`, so treating that as a dead device would have deleted working subscriptions.
- Maintenance: `gcm` is now an alias for `fcm`. Existing gcm providers and subscriptions keep working.

UnifiedPush / Web Push:

- New provider: `webpush`, also registered as `unifiedpush`. RFC 8030 delivery, RFC 8291 encryption and
  RFC 8292 VAPID, which is what UnifiedPush and browser Web Push use. See the README for setup.
- New feature: `uniqush-push -generate-vapid-keys` prints a VAPID key pair.
- Security: Pushes to non-globally-routable addresses are refused by default, since the destination comes
  from `/subscribe`. Relax per service with `allow_private_addresses` and `allowed_hosts` in the config.

Retries:

- Change: A push service's requested delay (`Retry-After`, or Apple's provider-token floor) now seeds the
  retry schedule for every backend. Previously the first retry was always 5 seconds and the push was
  abandoned past a minute regardless.
- Security: A requested delay is capped at 30 minutes, so a remote server cannot pin memory with a huge `Retry-After`.

Database:

- Bugfix: A read no longer deletes delivery points whose provider is missing. `/rmpsp` used to silently
  unsubscribe every device in the service on the next push, unrecoverably.
- New feature: `/addpsp` accepts `replace=true` to replace a provider whose credentials changed -- e.g. moving
  APNs from a certificate to a `.p8` -- without losing subscriptions. A delivery point's provider is now
  derived from its service and push service type rather than read from the stored binding; the binding is
  still written, so a rollback needs no repair. (docs/delivery-point-rebinding.md)
- New feature: `/checkdb` reports database inconsistencies. Read-only and lock-free, so it is safe to run
  against production. Run it before upgrading a database created before 2.6.0.

Maintenance:

- Building requires Go 1.25 or newer (was 1.14).
- Update `golang.org/x/net` from a 2020 revision to v0.57.0 (CVE-2023-44487, CVE-2023-45288). `govulncheck` runs in CI.
- Replace Travis CI with GitHub Actions; migrate `.golangci.yml` to the v2 format.
- `go test ./srv/apns/` drives the real HTTP/2 transport against a simulator that enforces Apple's documented
  contract; `go test -tags apns_live ./srv/apns/http_api/` probes Apple's real sandbox.

Changes to APIs (embedders only):

- `http_api.HTTPPushRequestProcessor.GetClient` now returns `(HTTPClient, func(), error)`.
  Call the second value exactly once to release the client; it is nil on the error path.
- `TryGetClient` is removed. It had been returning nil for every caller since the cache moved to a composite key.

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
