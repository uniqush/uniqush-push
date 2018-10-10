uniqush-push NEWS

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
  the query param value `uniqush.http=1`.
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
