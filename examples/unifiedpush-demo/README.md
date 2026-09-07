# UnifiedPush demo

Drives one UnifiedPush message all the way through uniqush-push and back out
again, then reports what was on the wire.

The [webpush-demo](../webpush-demo) next door covers the same backend from a
browser. This one needs no browser, no vendor account and — with
`-distributor local` — no network at all, which also makes it the quickest way
to check the backend still works after a change.

## Run it

You need Go 1.25+, a redis server, and uniqush-push running.

```sh
# from the repository root
redis-server --daemonize yes
go build -o uniqush-push .
./uniqush-push -config examples/webpush-demo/uniqush-push.conf
```

Then, in another terminal:

```sh
# against ntfy.sh, the distributor most people run
go run ./examples/unifiedpush-demo

# against a self-hosted ntfy
go run ./examples/unifiedpush-demo -distributor http://127.0.0.1:2586

# with no distributor and no network: the demo hosts the push server itself
go run ./examples/unifiedpush-demo -distributor local
```

A run ends like this:

```
------------------------------------------------------------------------
On the wire
  POST body            2048 bytes (UnifiedPush allows 1-4096)
  Content-Encoding     aes128gcm
  record size (rs)     2048
  salt                 CV4PG4IMo_PLBwQ3TqRNuQ
  server public key    BFoofV7RphBQ-ZL7X4_MGc4W1cXhHP82wVKXGk30px9YJywG...
  ciphertext           1962 bytes, padded to fill the record

Decrypted
  {"msg":"Hello from uniqush-push over UnifiedPush."}
------------------------------------------------------------------------

OK: the payload uniqush sent is the payload the device read.
```

A distributor on localhost — the built-in one, or a self-hosted ntfy — needs
`allow_private_addresses=true`, because uniqush refuses to POST to a
non-routable address for this backend: the destination comes from whoever called
`/subscribe`. The setting lives in the config section named after the push
service type in use, so `[unifiedpush]` by default and `[webpush]` under
`-pushservicetype webpush`. They are two separate registrations and are
configured separately; the shared config in
`examples/webpush-demo/uniqush-push.conf` sets both.

Useful flags: `-message`, `-raw` (send a verbatim body instead of letting
uniqush build JSON), `-pushservicetype webpush` to use the backend's other name,
`-service`, `-subscriber`, `-keep` to leave the subscription in place,
`-timeout`.

### If ntfy.sh answers 507

```
[Push][Info] ... DeliveryPoint=unifiedpush:e7a4fcd... Retry after 1m0s: "push server
returned HTTP 507: {\"code\":50701,\"http\":507,\"error\":\"cannot publish to
UnifiedPush topic without previously active subscriber\"}"
```

ntfy.sh runs with `visitor-subscriber-rate-limiting` on, which charges a
UnifiedPush message to the *subscriber's* rate limits rather than the
publisher's — so it refuses to accept a push to a topic that has never had a
subscriber. Two things have to be true before a push will land:

- **Something must have subscribed to the topic first**, over `/json`, `/sse`,
  `/raw` or the websocket. The demo does this before it pushes; a real setup has
  the distributor app holding the subscription.
- **The topic must be exactly 14 characters**, `up` plus twelve. ntfy decides
  eligibility by name alone — `strings.HasPrefix(id, "up") && len(id) == 14` —
  and a topic of any other length can never register a subscriber for this
  purpose, so every push to it gets the 507 no matter who is listening.

It is a 5xx rather than a 4xx deliberately, so that application servers retry
instead of deleting the subscription. A self-hosted ntfy has the setting off by
default and none of this applies.

## What it proves

UnifiedPush has three parties, and the demo plays two of them:

```
  application server        distributor            application
  (uniqush-push)            (ntfy, NextPush, ...)  (a phone)
        |                          |                     |
        |  POST, aes128gcm body    |    forwards raw     |
        +------------------------->+-------------------->+
        |     RFC 8030 + 8291      |                     | decrypts
        |     + 8292 (VAPID)       |                     | RFC 8291
```

The demo generates subscription keys the way a UnifiedPush connector library
does on the device, gets an endpoint from the distributor, registers both with
uniqush, asks uniqush to push, waits for the message to come back out of the
*distributor*, and decrypts it.

That last leg is the point. uniqush reporting success only means a push server
answered 2xx; it says nothing about whether anything arrived or whether the
device could read it. A run that ends in the original text has exercised the
encryption, the delivery and the decryption together.

The receiving half lives in `crypto.go` and is written against nothing but the
standard library, so it shares no code with the sender — a matched pair of
mistakes cannot cancel out. `crypto_test.go` checks it against the worked
example in [RFC 8291 section 5](https://www.rfc-editor.org/rfc/rfc8291#section-5),
so a successful run means the bytes on the wire are the bytes the RFC describes,
not merely bytes uniqush and the demo agree on:

```sh
go test ./examples/unifiedpush-demo/
```

## Why UnifiedPush and Web Push are the same backend here

UnifiedPush's own
[server spec](https://unifiedpush.org/developers/spec/server/) defines the
application-server side as Web Push: an HTTP POST to an endpoint the client
supplies, with an RFC 8291 `aes128gcm` body. That is exactly what a browser push
subscription wants, which is why uniqush registers one implementation under two
names, `webpush` and `unifiedpush`. Anything different between them is naming,
not protocol.

The spec adds a few constraints beyond RFC 8030 that this backend honours:

| Requirement | Where |
|---|---|
| Endpoint at most 1000 bytes, `http`/`https` only | rejected at `/subscribe` |
| Payload 1–4096 bytes on the wire | uniqush sends a fixed 2048-byte record |
| Treat any 2xx as accepted, not just 201 | `classifyStatus` |
| 404 and 410 mean the endpoint is gone | the subscription is deleted |
| 429 means back off, honouring `Retry-After` | the retry schedule is seeded from it |
| Redirects MUST NOT be followed | the HTTP client refuses them |
| Do not POST to non-routable addresses | `EndpointPolicy`, re-checked per push |

Because the destination URL comes from whoever called `/subscribe`, that last
one is what keeps uniqush from being a server-side request forgery primitive.
`allow_private_addresses` and `allowed_hosts` relax it for self-hosted push
servers.

## Testing against a phone

Everything above stands in for the device. To put a real one in the loop, give
the demo the phone's own subscription with `-endpoint`, `-p256dh` and `-auth`.
No app has to be written: two from F-Droid cover it.

1. Install [ntfy](https://f-droid.org/en/packages/io.heckel.ntfy/), which is a
   UnifiedPush distributor as well as a notification app. UnifiedPush is on by
   default and ntfy.sh is the default server, so there is nothing to configure —
   but do exempt it from battery optimisation, since its UnifiedPush
   subscriptions rely on a foreground service.
2. Install [UP-Example](https://f-droid.org/en/packages/org.unifiedpush.example/)
   (`org.unifiedpush.example`), the UnifiedPush project's own test app. Register,
   and pick ntfy when it asks which distributor to use.
3. The app now shows **Endpoint**, **P256dh** and **Auth**. Its *Test page* link
   carries all three in the URL fragment, which is the easiest way to get them
   onto a machine — share it to yourself and hand it straight over:

```sh
go run ./examples/unifiedpush-demo \
  -test-page 'https://unifiedpush.org/test_wp.html#endpoint=...&p256dh=...&auth=...' \
  -raw -message 'title=uniqush&message=hello from the server'
```

Or pass the three values individually with `-endpoint`, `-p256dh` and `-auth`.
Since the fragment holds the keys that decrypt this subscription's messages,
send that link to yourself rather than posting it anywhere.

The run ends at the push: the private key never left the phone, so the demo
cannot watch the delivery or decrypt anything, and the phone is what tells you
it worked. Three outcomes:

| On the phone | What it means |
|---|---|
| A notification with your text | The whole path works, encryption included |
| "Could not decrypt content." | It arrived, but RFC 8291 decryption failed |
| Nothing | It never arrived; check the distributor app |

UP-Example reads the body as `title=...&message=...` and falls back to showing
the whole body as the message, which is why the example above uses `-raw`.
Without it, uniqush sends its JSON and the notification is that JSON.

A device's subscription is not the demo's to delete, so device mode never
unsubscribes — `-keep` is implied. It prints a `curl` line for pushing to the
phone again without another registration.

Two things to know before blaming uniqush. Re-registering in UP-Example, or
switching distributors, gives the phone a **new** endpoint *and* new keys, so
the old subscription is dead and you need to run this again. And ntfy.sh will
not accept a push to a topic that has never had a subscriber, so the ntfy app
has to be installed and connected first — see the 507 above.
