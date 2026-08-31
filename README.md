- [Homepage](http://uniqush.org)
- [Download](http://uniqush.org/downloads.html)
- [Blog/News](http://blog.uniqush.org)
- [@uniqush](http://twitter.com/uniqush)

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
> - **APNs — repaired, but not yet verified against Apple's servers.** As of the
>   unreleased version it uses the HTTP/2 API by default and sends the
>   `apns-push-type` header that iOS 13+ requires. Earlier releases defaulted to
>   the binary protocol, which Apple switched off on 31 March 2021, and could
>   not deliver at all.
>
>   These changes are covered by unit tests against a mocked APNs, but **nobody
>   has yet run them against real Apple credentials and a real device**, because
>   the current maintainer does not have an Apple developer account — and Apple
>   sells no way to get one for free. If you have one, a report either way would
>   be genuinely useful; see
>   [docs/apns-verification-plan.md](docs/apns-verification-plan.md), which lists
>   exactly which cases have no coverage.
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
> Building requires **Go 1.25 or newer**.

## Supported Platforms ##

- [FCM](https://firebase.google.com/docs/cloud-messaging/) from Google for the Android platform (`gcm` is an alias)
- [APNS](https://developer.apple.com/documentation/usernotifications/sending-notification-requests-to-apns) from Apple for the iOS platform
- [ADM](https://developer.amazon.com/sdk/adm.html) from Amazon for Kindle tablets
- [UnifiedPush](https://unifiedpush.org/) / [Web Push](https://datatracker.ietf.org/wg/webpush/documents/), for de-Googled Android, Linux desktops and browsers

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

## FAQ ##

- Q: Is this a general push notification platform for all types of devices? How does this differ
  from services such as [Urban Airship](http://urbanairship.com)?
- A: [Urban Airship](http://urbanairship.com) is a great service, and there are
  other similar services available, like [OpenPush](http://openpush.im/),
[Notificare](https://notifica.re/), etc. All of them are wonderful services.
However, [Uniqush](http://uniqush.org) is different from them.
[Uniqush](http://uniqush.org) is not a service. Instead,
**[Uniqush](http://uniqush.org) is a system, which runs on your own
server**. In fact, if you wish, you can use Uniqush to set up a service similar to [Urban Airship](http://urbanairship.com).

- Q: OK. Then is it a library? Like
  [java-apns](https://github.com/notnoop/java-apns)?
- A: Well.. Not actually. I mean, it is a program, like Apache HTTP Server. You download it, you run it. It does require a [Redis](http://redis.io/) server, but, other than that, you don't need to worry about which language to use, package dependencies, etc.

- Q: But wait, how can I use it anyway? I mean, if my program wants to send
  a push notification, I need to tell Uniqush about this action. How can I
  communicate with Uniqush? There must be some library so that I can use it
  in my program to talk with Uniqush, right?
- A: We are trying to make it easier. `uniqush-push` provides RESTful APIs. In
  other words, you talk with `uniqush-push` through HTTP protocol. As long as
there's an HTTP client library for your language, you can use it and talk with
`uniqush-push`. For details about our RESTful APIs, see [our API
documentation](http://uniqush.org/documentation/usage.html).

- Q: Then that's cool. But I noticed that you are using [Go](http://golang.org) programming language. Do I need to install [Go](http://golang.org) compiler and other stuff to run `uniqush-push`?
- A: No. There are no installation dependencies. All you need to do is to download the
  binary file from the [download page](http://uniqush.org/downloads.html) and
install it. But you do need to set up a [Redis](http://redis.io) server running
somewhere, preferably with persistence, so that `uniqush-push` can store the
user data in [Redis](http://redis.io). For more details, see the
[installation guide](http://uniqush.org/documentation/install.html)

- Q: This is nice. I want to give it a try. But you are keep talking about `uniqush-push`, and I'm talking about *Uniqush*, are they the same thing?
- A: Thank you for your support! *Uniqush* is intended to be the name of a
  system which provides a full stack solution for communication between mobile
devices and the app's server. `uniqush-push` is one piece of the system.
However, right now, `uniqush-push` is the only piece and others are under
active development. If you want to know more details about the *Uniqush*
system's plan, you can read the [blog
post](http://blog.uniqush.org/uniqush-after-go1.html). If you want to find out
about the latest progress with *Uniqush*, please check out [our
 blog](http://blog.uniqush.org/). And, if you are really impatient, there's
 always our [our GitHub account](http://github.com/uniqush) which could have
 brand-new stuff that hasn't been released yet.

## Setting Up Redis ##

[Redis persistence](http://redis.io/topics/persistence) describes the details
of how Redis saves data on shutdown, as well as how one might back up that
data. Make sure that the Redis server you use has persistence enabled - your
redis.conf should have contents similar to the section `**PERSISTENCE**` of
redis.conf in the example config files linked in http://redis.io/topics/config

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
- [Documentation](http://uniqush.org/documentation/index.html)
- [The Uniqush blog](http://blog.uniqush.org) announces the latest news about Uniqush.
- [Redis persistence](http://redis.io/topics/persistence)
