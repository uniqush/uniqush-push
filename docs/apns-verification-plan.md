# Verifying the APNs backend

APNs is the backend uniqush can verify least. The HTTP/2 repairs in
[#278](https://github.com/uniqush/uniqush-push/pull/278) were covered only by
unit tests against a mocked APNs, and nobody had run them against Apple's
servers.

That gap cannot be closed completely, because **Apple charges for the ability to
close it**. Push Notifications is not among the capabilities available to a free
Apple ID: enabling it needs an App ID with the APS entitlement, which needs a
paid Apple Developer Program membership ($99/year). That is true for both
authentication methods — a `.p12` certificate and a `.p8` token key are both
minted from a paid account. There is no free tier and no legitimate way around
it.

So the approach is to get as close as possible without an account, and to make
it cheap for someone who has one to cover the rest. Most of that now exists.

---

## What you can run today

### The conformance suite — `go test ./srv/apns/`

`srv/apns/apnstest` is an APNs simulator, and `srv/apns/conformance_test.go`
drives the real push service against it over a real HTTP/2 socket. This is the
first coverage the HTTP/2 path has had that exercises the transport rather than
mocking it out.

The simulator enforces Apple's documented contract instead of accepting whatever
arrives — the `/3/device/<hex>` path shape, the required headers, priority 5 for
background pushes, the 4096-byte ceiling, canonical `apns-id` UUIDs, and
duplicate headers. That last one is worth calling out: `http.Header.Set`
canonicalises `apns-topic` to `Apns-Topic`, and using it alongside a lowercase
literal puts the field on the wire twice, which Apple answers with 400
`DuplicateHeaders`. It is an easy change to make while tidying and invisible
without a server that counts.

Each of these was confirmed to fail the suite when reintroduced:

| Reintroduced bug | Caught by |
|---|---|
| `apns-push-type` header dropped | `TestConformanceAlertPush` |
| `apns-priority` hardcoded to 10 | `TestConformanceBackgroundPushUsesPriority5` |
| header shared instead of cloned per token | `TestConformanceEachTokenGetsItsOwnAPNSID` |
| `Header.Set` alongside the lowercase literal | `TestConformanceHeadersAreSentOnce` |
| endpoint hardcoded back to Apple | `TestConformanceEndpointIsHonoured` |

What it cannot tell you is whether Apple agrees with any of it. The simulator
matches our reading of the documentation, which is exactly the thing in doubt.

### The live probe — `go test -tags apns_live ./srv/apns/http_api/`

Apple's development host answers unauthenticated requests. `-D -` prints the
status line and headers, which `-s` alone does not:

```console
$ curl -sS -D - --http2 -X POST \
    -H 'apns-topic: com.example.uniqush.probe' \
    -H 'apns-push-type: alert' \
    https://api.development.push.apple.com/3/device/aaaa -d '{"aps":{}}'
HTTP/2 403
apns-id: 9CC486EA-EA83-9351-40DF-557A308816D8

{"reason":"MissingProviderToken"}
```

That is the host the live tests target, `common.HostDevelopment`.
`api.sandbox.push.apple.com` is the older name for the same environment and
behaves identically, but the command above is the one that matches what is
under test.

So these tests run against genuine Apple infrastructure with no account at all.
They cover TLS to Apple, ALPN negotiating h2, both host constants, the
`/3/device` path shape, and uniqush's own `APNSErrorResponse` parsing a real
Apple error body rather than a fixture. They also assert that uniqush does *not*
treat `MissingProviderToken` — the reason Apple returns for an unauthenticated
push — as a dead token, since unsubscribing on it would delete every
subscription in a service because a credential was wrong.

What they cannot reach is `Forbidden`, which requires authenticating and then
being refused for the topic. That needs a paid account, so the guarantee that
uniqush does not unsubscribe on it lives in the simulator instead, in
`TestConformanceForbiddenDoesNotUnsubscribe`. Asserting it here would mean
indexing the failure map with whatever reason happened to come back, which
passes whether or not the guarantee holds.

They are build-tagged, so they stay out of `go test ./...` and out of CI.

### Pointing uniqush at either

`/addpsp` takes two settings that make the above possible:

```
-d endpoint=https://localhost:8443   # APNs HTTP/2 base URL
-d cacert=/path/to/ca.pem            # verify it against this CA
```

Both live in `VolatileData`, so a service can move between environments without
every device re-subscribing, and both are optional — without them the
environment is inferred from `addr` exactly as before, so no existing provider
changes where it sends. `skipverify` is refused for Apple's own hosts: it is an
easy setting to leave behind after testing, and it disables the only check that
the host answering for `api.push.apple.com` is Apple.

Prefer `cacert` over `skipverify` when testing. It still verifies the chain and
the hostname, so the code path production uses is the code path under test.

---

## What is left

### Token (`.p8`) authentication

uniqush only supports certificate authentication: `createTLSConfig` loads a
`cert`/`key` pair and `/addpsp` requires both. Apple's token-based auth — an
ES256-signed JWT built from a `.p8` key, a key id and a team id — is not
implemented.

Certificates expire annually and are per-app; a `.p8` key does not expire and
covers every app in the team, which is why it is the default in current
documentation and in every actively maintained APNs library. Anyone volunteering
to test uniqush is likely to have a `.p8` and to find being asked for a `.p12`
odd, so this widens the pool for the item below.

- Accept `authkey` (path to the `.p8`), `keyid` and `teamid` in `/addpsp`, as an
  alternative to `cert`/`key`.
- Mint a JWT with `alg: ES256`, `kid`, `iss` and `iat`, sent as
  `authorization: bearer <jwt>`.
- Cache and refresh it. Apple rejects a token older than one hour with
  `ExpiredProviderToken`, and rejects minting more than one per 20 minutes per
  key with `TooManyProviderTokenUpdates`, so the refresh window has to sit
  between those bounds — roughly every 40-50 minutes.
- Keep the credential out of `FixedData`, for the same reason `endpoint` is not
  there: a provider's name hashes its fixed data, so a rotatable credential
  stored there could never be rotated without re-subscribing every device.
  `srv/fcm/push_service.go` has the same reasoning written out for
  `credentialsfile`.
- No new dependency needed: `github.com/golang-jwt/jwt/v5` is already indirect.

The simulator should grow matching support — validating the JWT signature,
returning `ExpiredProviderToken` past the hour, and
`TooManyProviderTokenUpdates` on over-eager refresh — so the refresh timing can
be tested without waiting an hour against Apple.

### The part that needs an Apple account

None of the above proves a notification arrives on an iPhone. That needs a paid
membership, and the realistic route is asking someone who has one.

What would make that ask likely to succeed is a kit: a script taking a `.p8` (or
`.p12`), a team id, a bundle id and a device token, which runs `/addpsp`,
`/subscribe` and `/push` against a local uniqush and prints a filled-in report
to paste into an issue. Ten minutes of a stranger's time, rather than an
open-ended favour.

Worth asking for specifically, since these have no coverage:

- an alert push to a foregrounded and a backgrounded app
- a **background** push (`apns-push-type: background`, priority 5) — the case
  that used to return 200 and then silently drop, and the main thing #278 fixed
- a VoIP push, via both `uniqush.apns_push_type=voip` and the older
  `uniqush.apns_voip=1`
- a push to a token from the other environment, expected to come back
  `BadDeviceToken` and unsubscribe
- a push to an uninstalled app, expected to come back 410 `Unregistered`

Note that `srv/apns/apns-test/apns-test.sh` and the `uniqush/apns-simulator` it
drives are binary-protocol only, and so exercise a path Apple switched off on
2021-03-31. The conformance suite replaces them for HTTP/2; the script is worth
retiring or rewriting against `endpoint`.

## Status

| | Needs an Apple account | Proves | State |
|---|---|---|---|
| Configurable endpoint and CA | no | nothing alone; unblocks the rest | done |
| Local simulator | no | protocol conformance, error mapping, the whole stack | done |
| Live sandbox probe | no | reachability, TLS/ALPN/h2, real error parsing | done |
| `.p8` auth | no to build, yes to test | nothing alone; widens the pool below | outstanding |
| Real device | **yes** | delivery | outstanding |
