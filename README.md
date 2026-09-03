- [Homepage](https://uniqush.org)
- [Download](https://uniqush.org/downloads.html)
- [Blog/News](https://uniqush.org/blog)

## Introduction ##

*Uniqush* (\ˈyü-nə-ku̇sh\ "uni" pronounced as in "unified", and "qush" pronounced as
in "cushion") is a _free_ and _open source_ software system which provides
a unified push service for server side notification to apps on mobile devices.
The `uniqush-push` API abstracts the APIs of the various push services used
to send push notifications to those devices. By running `uniqush-push` on the
server side, you can send push notifications to any supported mobile platform.

[![CI](https://github.com/uniqush/uniqush-push/actions/workflows/ci.yml/badge.svg?branch=master)](https://github.com/uniqush/uniqush-push/actions/workflows/ci.yml)

> ### ⚠️ Status: read before deploying
>
> This project was dormant from 2020 to 2026, and in that time some of the
> upstream APIs it depends on were shut down.
>
> - **APNs — repaired, and verified as far as Apple allows without an account.**
>   As of the unreleased version it uses the HTTP/2 API by default and sends the
>   `apns-push-type` header that iOS 13+ requires. Earlier releases defaulted to
>   the binary protocol, which Apple switched off on 31 March 2021, and could
>   not deliver at all.
>
>   `go test ./srv/apns/` now drives the real HTTP/2 transport against a
>   simulator that enforces Apple's documented contract, and
>   `go test -tags apns_live ./srv/apns/http_api/` checks reachability and error
>   parsing against Apple's real sandbox, which answers unauthenticated
>   requests.
>
>   Token (`.p8`) authentication is now supported alongside certificates:
>   `/addpsp` takes `authkey`, `keyid` and `teamid`. To move an existing
>   certificate-based service across, add `replace=true`; subscriptions survive
>   it. Do **not** use `/rmpsp` for this — see
>   [docs/delivery-point-rebinding.md](docs/delivery-point-rebinding.md).
>
>   What remains is delivery to a device, and that needs a paid Apple Developer
>   Program membership — Apple sells no free route to one. **If you have an
>   account, a report either way would be genuinely useful**; see
>   [docs/apns-verification-plan.md](docs/apns-verification-plan.md) for exactly
>   which cases still have no coverage.
> - **FCM — migrated to HTTP v1, and verified against Google.** The legacy
>   endpoint it used was decommissioned on 20 June 2024. `/addpsp` now takes
>   `projectid` and `credentialsfile` instead of `apikey`; devices do not need
>   to re-subscribe. `gcm` is now an alias for the same backend.
>
>   Confirmed working end to end on 1 September 2026, against a real Firebase
>   project: pushes were accepted by FCM and delivered to a browser. Unlike APNs
>   this needs no paid account, so you can repeat it yourself —
>   [examples/fcm-demo](examples/fcm-demo) is the ten-minute version, and
>   `go test -tags fcm_live ./srv/fcm/` exercises the send path against Google
>   directly. Delivery to an Android device has not been checked yet; the
>   browser path covers the whole server side but not the `android` block.
> - **ADM** is believed to still work, but has not been re-verified.
> - **UnifiedPush / Web Push — new, and the one backend with no vendor
>   dependency.** See below.
>
> Building requires **Go 1.25 or newer**. If you are upgrading from 2.7.0,
> [docs/upgrading.md](docs/upgrading.md) walks through what changes for each
> backend and what needs action.

## Supported Platforms ##

- [FCM](https://firebase.google.com/docs/cloud-messaging/) from Google for the Android platform (`gcm` is an alias)
- [APNS](https://developer.apple.com/documentation/usernotifications/sending-notification-requests-to-apns) from Apple for the iOS platform
- [ADM](https://developer.amazon.com/sdk/adm.html) from Amazon for Kindle tablets
- [UnifiedPush](https://unifiedpush.org/) / [Web Push](https://datatracker.ietf.org/wg/webpush/documents/), for de-Googled Android, Linux desktops and browsers

## Building and running ##

`uniqush-push` is a single binary. Building it needs Go 1.25 or newer; running
it needs a [Redis](https://redis.io) server.

```
git clone https://github.com/uniqush/uniqush-push.git
cd uniqush-push
go build          # produces ./uniqush-push
```

or, without a checkout, `go install github.com/uniqush/uniqush-push@master`.

Copy [conf/uniqush-push.conf](conf/uniqush-push.conf) to
`/etc/uniqush/uniqush-push.conf`, the default location, or point at it with
`-config`. The settings you are most likely to touch:

- `[WebFrontend] addr` — where the REST API listens. It defaults to
  `localhost:9898`, and there is a reason to leave it there: **the API has no
  authentication.** Anything that can reach `/addpsp` can decide where a
  service's pushes go. Bind to localhost, or put it behind a reverse proxy that
  authenticates.
- `[Database] host`, `port` and `password` — the Redis server (`port=0` means
  the default, 6379). `slave_host`/`slave_port` route reads to a replica.
- `logfile`, and a `loglevel` per section: `alert`, `error`, `warn`, `info`
  (also spelled `standard` or `verbose`) or `debug`.

Make sure Redis has [persistence](https://redis.io/docs/latest/operate/oss_and_stack/management/persistence/)
enabled. Every subscription lives there, and by default Redis only snapshots
periodically.

Then:

```
uniqush-push                      # or: uniqush-push -config /path/to/uniqush-push.conf
curl http://localhost:9898/version
```

`uniqush-push -version` prints the version and `uniqush-push -generate-vapid-keys`
mints a key pair for a Web Push provider; both exit without starting the
server. `go test ./...` runs the test suite, including the APNs conformance
suite against a local simulator; the live tests that talk to Apple and Google
are behind build tags and described in
[docs/apns-verification-plan.md](docs/apns-verification-plan.md) and
[examples/fcm-demo](examples/fcm-demo).

## UnifiedPush / Web Push ##

[UnifiedPush](https://unifiedpush.org/) is a decentralised push standard: the
user picks the push provider rather than the app developer, and it works on
devices with no Google services at all. Its application-server side is plain
Web Push — [RFC 8030](https://www.rfc-editor.org/rfc/rfc8030) delivery,
[RFC 8291](https://www.rfc-editor.org/rfc/rfc8291) `aes128gcm` payload
encryption and [RFC 8292](https://www.rfc-editor.org/rfc/rfc8292) VAPID
authentication — so the same backend also drives browser Web Push.

It is registered under two names, `webpush` and `unifiedpush`. They behave
identically; pick one per service and stay with it, since the name is part of a
subscription's identity in the database.

Unlike the other backends, this one needs no vendor account and no certificate.

**1. Generate a VAPID key pair.** These identify your server to push providers.
Some providers reject registrations without them.

```
$ uniqush-push -generate-vapid-keys
vapidpublickey=BIknD72EXwC1CC5WamGPDn4YbTV7o6yE_zMNNJO2xNMGyy4sz6egSmwFhH8lxllQqvqInrkqyKwnuy1Q1vmkevk
vapidprivatekey=NOHiudJNUw6IEf0SN0jYTascVt68R0sQJxMWSbVRWM4
```

**2. Create the push service provider.** `subscriber` is the VAPID contact — a
bare email address or an `https://` URL, not a `mailto:` URI.

```
curl http://localhost:9898/addpsp \
  -d service=myservice \
  -d pushservicetype=unifiedpush \
  -d vapidpublickey=BIknD72... \
  -d vapidprivatekey=NOHiudJ... \
  -d subscriber=admin@example.org
```

**3. Subscribe a device.** The app's UnifiedPush connector library produces all
three values; your app just forwards them to your server.

```
curl http://localhost:9898/subscribe \
  -d service=myservice \
  -d subscriber=alice \
  -d pushservicetype=unifiedpush \
  -d endpoint=https://ntfy.sh/up?id=... \
  -d p256dh=BNcRdreALRFXTkOO... \
  -d auth=tBHItJI5svbpez7KI4CCXg
```

**4. Push.** Payload fields are JSON-encoded and delivered encrypted; the app on
the device decrypts them. Pass `uniqush.payload.webpush` instead to send a raw
body verbatim.

```
curl http://localhost:9898/push -d service=myservice -d subscriber=alice -d msg=hello
```

### A note on SSRF ###

For every other backend the destination host is a constant compiled into
uniqush. Here it comes from whoever called `/subscribe`, so uniqush refuses by
default to POST to addresses that are not globally routable — loopback, RFC 1918,
link-local (including the `169.254.169.254` cloud metadata endpoint), and the
IPv6 equivalents. Redirects are never followed, and the check runs before every
push rather than only at subscribe time, so DNS rebinding does not defeat it.

Self-hosted push servers on a private network are a supported UnifiedPush
setup, so this can be relaxed per service in `uniqush-push.conf` with
`allow_private_addresses`, ideally alongside an `allowed_hosts` list.

## APNs ##

`/addpsp` for `apns` takes the usual `cert`, `key` and `bundleid`. Two optional
settings control where HTTP/2 pushes actually go:

- `endpoint` — the base URL to push to, e.g. `https://api.sandbox.push.apple.com`.
  uniqush appends `/3/device/<token>`, so it must have no path, query or
  fragment. Omitted, the environment is inferred from `addr` exactly as it was
  before this setting existed.
- `cacert` — a PEM bundle to verify that endpoint against. Prefer this to
  `skipverify` when testing: the certificate and hostname are still checked, so
  a simulator has to present one you actually issued.

Both are cleared if a later `/addpsp` omits them, the same way `bundleid`
behaves.

### A note on endpoints ###

An `endpoint` decides where every push for a service goes. It carries device
tokens and the notification payload, and for a certificate provider it is also
where the APNs client certificate is presented. So unlike the UnifiedPush case
above — where the destination comes from a subscriber and the defence is an
address policy — uniqush refuses **any** host outside `push.apple.com` unless
you opt in:

```
[apns]
allow_non_apple_endpoints=true
```

The check runs before every push as well as at `/addpsp`, because a provider
loaded from Redis never passes through the code that validates it. An address
policy would be the wrong tool here: the legitimate destinations are a simulator
on localhost or a relay inside your network, which is exactly what such a policy
blocks, while an attacker-controlled public host is exactly what it permits.

`skipverify` is refused outright for Apple's own hosts. It predates the HTTP/2
path and was silently ignored there, so operators who set it years ago for the
binary-protocol simulator still have it stored; honouring it now would have
disabled certificate verification on connections to Apple.

## FAQ ##

- Q: Is this a hosted push service, like OneSignal or Airship?
- A: No. `uniqush-push` is a program that runs on your own server, the way
  Apache does: you build it, run it, and point it at a Redis server. It talks
  to Apple, Google, Amazon and Web Push servers on your behalf. If you wanted
  to, you could build a hosted service on top of it.

- Q: Is it a library, then? Do I need to write Go?
- A: No. It is a daemon with a REST API over plain HTTP, so any language with
  an HTTP client can drive it — `curl` is enough to try it. Go is needed to
  *build* it, since there are no prebuilt binaries for the current version; the
  downloads on uniqush.org stop at 2.6.1, which can no longer deliver to APNs
  or FCM at all.

- Q: Where is the API documented?
- A: [docs/api.md](docs/api.md) is the reference: every endpoint, the
  parameters each backend takes on `/addpsp` and `/subscribe`, what `/push`
  accepts, and the response formats. The older reference on uniqush.org
  predates this release; its FCM and APNs registration examples will not work
  as written.

- Q: I'm upgrading from 2.7.0 or earlier. Will my subscriptions survive?
- A: Yes; no device has to re-subscribe for anything in this release. FCM
  providers need a new `/addpsp` with `projectid` and `credentialsfile` in
  place of `apikey`; APNs providers keep working with their existing
  certificate, and can move to a `.p8` key with `replace=true`. Do not use
  `/rmpsp` to change a provider's credentials: in 2.7.0 that silently deleted
  every subscription in the service. [docs/upgrading.md](docs/upgrading.md)
  has the details.

- Q: My services are registered as `gcm`. Do I have to rename them?
- A: No, and you should not: `gcm` is an alias for `fcm`, and the name is part
  of every stored subscription's key. Keep the name and re-run `/addpsp` with
  the new parameters.

- Q: Is the API authenticated?
- A: No. It binds to `localhost:9898` by default so that only local processes
  can reach it. Keep it there, or put it behind a reverse proxy that
  authenticates, because whoever can call `/addpsp` controls where every push
  for a service is sent. The same reasoning is why VAPID keys are generated by
  a command-line flag rather than an endpoint, and why a non-Apple APNs
  `endpoint` has to be enabled in the config file.

- Q: What is the difference between *Uniqush* and `uniqush-push`?
- A: *Uniqush* was conceived as a suite of components for mobile messaging;
  `uniqush-push` is the piece that was built, and the names are used
  interchangeably.

- Q: Something doesn't work. Where do I ask?
- A: The [issue tracker](https://github.com/uniqush/uniqush-push/issues).
  Reports from anyone who can verify APNs delivery to a real device are
  especially welcome — see the status note at the top.

## Contributing ##

You're encouraged to contribute to the `uniqush-push` project. There are two ways you can contribute.

### Issues ###

If you encounter an issue while using `uniqush-push`, please report it at the project's [issues tracker](https://github.com/uniqush/uniqush-push/issues). Feature suggestions are also welcome.

### Pull request ###

Code contributions to `uniqush-push` can be made using pull requests. To submit a pull request:

1. Fork this project.
2. Make and commit your changes.
3. Submit your changes as a pull request.

## Related Links ##
- [This story](http://uniqush.org/documentation/intro.html) may help you to understand
the basic idea of *Uniqush*.
- [API reference](docs/api.md), [upgrade notes](docs/upgrading.md), and the rest of [docs/](docs/)
- [Documentation on uniqush.org](http://uniqush.org/documentation/index.html) (older; being updated)
- [The Uniqush blog](https://uniqush.org/blog/) announces releases.
- [Redis persistence](https://redis.io/docs/latest/operate/oss_and_stack/management/persistence/)
