# Web Push / UnifiedPush demo

A small web app for exercising uniqush-push's `webpush` / `unifiedpush` backend
end to end.

This backend is the only one you can test without a vendor account: no Apple
developer programme, no Firebase project, no certificate. A browser is enough.

The app sits between the browser and uniqush and does three things: registers a
push service provider at startup, turns a push registration into a uniqush
subscription, and sends a test notification. It proxies rather than letting the
page call uniqush directly, because uniqush sends no CORS headers and its REST
API has **no authentication** — it should never be reachable from a browser.

> This is a testing tool. Don't deploy it.

## Run it

You need Go 1.25+ and a redis server.

```sh
# 1. redis
redis-server --daemonize yes

# 2. uniqush-push, from the repository root
go build -o uniqush-push .
./uniqush-push -config examples/webpush-demo/uniqush-push.conf

# 3. the demo, in another terminal
cd examples/webpush-demo
go run .
```

Then open <http://localhost:8080>, click **Enable notifications in this
browser**, and click **Send test push**.

On first run the demo generates a VAPID key pair and caches it in
`vapid-keys.json`. Keep that file: the public key is baked into every
subscription a browser creates, so deleting it silently invalidates them all.
(`uniqush-push -generate-vapid-keys` prints a pair if you'd rather manage them
yourself.)

## What just happened

```
browser  --subscribe-->  Mozilla/Google push service
   |                          ^
   |  endpoint, p256dh, auth  |  encrypted POST (RFC 8291)
   v                          |
 demo app  --/subscribe-->  uniqush-push  --------+
           --/push------->
```

The browser's Push API registers with its vendor's push service — Mozilla
autopush for Firefox, FCM for Chrome — and hands back three values: an endpoint
URL, a `p256dh` public key and an `auth` secret. The demo passes those to
uniqush's `/subscribe`. On `/push`, uniqush encrypts the payload to those keys
and POSTs it to the endpoint.

**That is the same protocol UnifiedPush uses.** A UnifiedPush distributor such
as ntfy produces the same three values; only the host differs. Which is why
uniqush registers this backend under both names.

## Testing with real UnifiedPush

The browser path exercises the identical server code, but if you want an actual
UnifiedPush distributor in the loop you need an Android device:

1. Install a distributor — [ntfy](https://ntfy.sh/) is the easiest — and an app
   that uses the UnifiedPush connector library.
2. Get the endpoint, `p256dh` and `auth` that the connector produced.
3. Paste them into the **Or paste a UnifiedPush endpoint** section of the demo.
4. Send a test push.

Note that ntfy's own UnifiedPush endpoints do not require VAPID, but uniqush
sends it regardless, which is harmless — RFC 8292 auth is ignored by servers
that don't want it.

## If it doesn't work

**"Could not register with uniqush"** on startup — uniqush isn't running, or
redis isn't. Check `curl http://localhost:9898/version`.

**The enroll button does nothing** — service workers need `localhost` or HTTPS.
`127.0.0.1` counts as a secure context; a LAN IP does not. Check the browser
console.

**Push reports success but nothing appears** — the notification was delivered
and the service worker ran, but the OS didn't show it. Check the page's Log
panel: `Service worker received a push` means it arrived. macOS Focus modes and
Windows Focus Assist both suppress notifications silently. Also confirm the site
is allowed to notify in the browser's site settings.

**`endpoint address 127.0.0.1 is not globally routable`** — working as
intended. uniqush refuses to POST to addresses that aren't globally routable,
because for this backend the destination comes from whoever called `/subscribe`
rather than from a vendor, which would otherwise make it an SSRF primitive. The
bundled config sets `allow_private_addresses=true` under `[unifiedpush]` so a
locally hosted push server works; remove it for anything resembling production,
and pair it with `allowed_hosts`.

**`Could not resolve endpoint host`** — the same check, failing at DNS.

To see what uniqush stored, click **Show subscriptions**, or:

```sh
curl 'http://localhost:9898/subscriptions?subscriber=demo-user&services=webpushdemo'
```

## Flags

| Flag | Default | |
|---|---|---|
| `-listen` | `localhost:8080` | address for this app |
| `-uniqush` | `http://localhost:9898` | uniqush REST API |
| `-service` | `webpushdemo` | uniqush service name |
| `-pushservicetype` | `unifiedpush` | or `webpush`; they behave identically |
| `-subscriber` | `demo-user` | uniqush subscriber name |
| `-keys` | `vapid-keys.json` | where the VAPID pair is cached |
| `-contact` | `demo@example.org` | VAPID contact: bare email or `https://` URL, **not** a `mailto:` URI |
