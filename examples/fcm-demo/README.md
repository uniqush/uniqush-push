# FCM demo

A small web app for exercising uniqush-push's `fcm` backend end to end, against
Google's real servers.

uniqush's FCM support was rewritten for the HTTP v1 API after Google
decommissioned the legacy `fcm.googleapis.com/fcm/send` endpoint on 20 June
2024. The unit tests drive a mocked FCM, which proves uniqush builds the request
it means to build — but not that Google agrees. This closes that gap.

Everything here is free. FCM has no cost on Firebase's Spark plan, and unlike
APNs there is no paid developer programme to join.

> This is a testing tool. Don't deploy it.

## The awkward part, and the shortcut

Verifying delivery needs a registration token, which normally means building an
Android app. It doesn't have to.

**FCM issues registration tokens to browsers too**, and HTTP v1 sends to them
through the same `token` target, the same request body and the same code in
`srv/fcm`. A browser gets you a real token in about a minute and exercises the
entire server side. An Android device then adds only the platform-specific
`android` block — `collapse_key` and `ttl`.

So: browser first, device second.

## Set up a Firebase project

Six values, all from the [Firebase console](https://console.firebase.google.com/).
Create a project first if you don't have one; the free Spark plan is enough.

**1. A service account** — this is what uniqush authenticates with.

Project settings (the gear) → **Service accounts** → **Generate new private
key**. A JSON file downloads. Save it as `service-account.json` in this
directory; `.gitignore` already covers it.

It grants send access to every device in the project, so treat it as a
credential. Rotating it later is just a re-run of `/addpsp` with the new path.

**2. A web app** — this is what the browser identifies as.

Project settings → **General** → **Your apps** → add a **Web** app (`</>`).
Skip Firebase Hosting. The `firebaseConfig` object it shows you has the
`apiKey`, `messagingSenderId` and `appId` you need. None of these are secrets;
they ship inside every Firebase client.

**3. A web push certificate** — this is what browsers require.

Project settings → **Cloud Messaging** → **Web configuration** → **Web Push
certificates** → **Generate key pair**. Copy the public key.

While you're on that tab, check that **Firebase Cloud Messaging API (V1)** is
enabled. It is by default on projects created since 2024, but a project older
than that may still have only the legacy API turned on — in which case every
push fails with a 403 and no other clue.

Now fill in the config:

```sh
cd examples/fcm-demo
cp fcm-demo.example.json fcm-demo.json
$EDITOR fcm-demo.json
```

## Run it

You need Go 1.25+ and a redis server.

```sh
# 1. redis
redis-server --daemonize yes

# 2. uniqush-push, from the repository root
go build -o uniqush-push .
./uniqush-push -config examples/fcm-demo/uniqush-push.conf

# 3. the demo, in another terminal
cd examples/fcm-demo
go run .
```

Then open <http://localhost:8080>, click **Get a token for this browser**, and
click **Send test push**.

## What just happened

```
browser  --getToken()-->  FCM
   |                       ^
   |  registration token   |  HTTP v1 POST, OAuth2 bearer token
   v                       |
 demo app  --/subscribe--> uniqush-push
           --/push------->
```

The browser registers with FCM and gets back one opaque registration token.
The demo hands it to uniqush's `/subscribe` as `regid` — the same parameter an
Android token uses, which is why the v1 migration needed no device to
re-subscribe. On `/push`, uniqush mints an OAuth2 access token from the service
account and POSTs to
`https://fcm.googleapis.com/v1/projects/<projectId>/messages:send`.

The push is sent as a **data** message rather than a `notification` one. That's
deliberate: a data message is delivered to the app's own handler on every
platform, so what appears on screen is uniqush's payload having survived the
whole path, rather than something the OS rendered on its own.

**Preview the FCM payload** calls `/previewpush`, which shows the exact v1
message body uniqush would send without sending it. Useful on its own — it needs
no subscription, no device and no network round trip to Google.

## Then: a real Android device

Once the browser path works, the server side is verified. A device adds the
Android-specific behaviour: `msggroup` becoming `android.collapse_key`, `ttl`
becoming `android.ttl`, and the actual notification tray.

You need an app registered to the **same** Firebase project — a token from a
different project fails with `SENDER_ID_MISMATCH`, which uniqush treats as
permanent and unsubscribes.

1. Add an Android app in Project settings → **Your apps**, using your app's
   package name, and download `google-services.json` into `app/`.
2. Add the FCM dependency and log the token:

   ```kotlin
   FirebaseMessaging.getInstance().token.addOnCompleteListener { task ->
       if (task.isSuccessful) Log.d("FCM", "token=${task.result}")
   }
   ```

3. Paste it into **Or paste a token from an Android device** on the demo page.
4. Send a test push. A data-only message arrives in
   `FirebaseMessagingService.onMessageReceived`, not the notification tray, so
   log it there.

## The live Go tests

Separately from this demo, `srv/fcm/live_test.go` drives uniqush's FCM code
directly against Google. It's behind a build tag because it needs real
credentials and real quota.

```sh
export UNIQUSH_FCM_PROJECT_ID=my-firebase-project
export UNIQUSH_FCM_CREDENTIALS=$PWD/examples/fcm-demo/service-account.json
go test -tags fcm_live -v ./srv/fcm/ -run TestLive
```

Most of it needs no device at all. Sending to a deliberately fabricated token
still proves the whole auth path works, because an `INVALID_ARGUMENT` can only
come back *after* Google has accepted the bearer token. Add
`UNIQUSH_FCM_REGID` to also test a real delivery.

## If it doesn't work

**`getToken` fails in the browser.** Almost always the `vapidKey`. The Firebase
console has been known to visually truncate the public key when you select it by
hand — use the copy button. Otherwise check that the service worker registered:
DevTools → Application → Service Workers.

**Nothing arrives, but uniqush reports success.** A 200 from FCM means *accepted
for delivery*, not delivered. Check the browser tab isn't focused (foreground
messages go to `onMessage`, and the demo logs those separately), and that the
notification permission is still granted.

**`FCM rejected our credentials (HTTP 403)`.** Either the Cloud Messaging API
(V1) is not enabled on the project, or `credentialsFile` points at the wrong
JSON. A `google-services.json` and an API key are both easy to grab by mistake;
uniqush parses the file at `/addpsp` time specifically to catch that early.

**`non-JSON body` in a push error.** Something that isn't the v1 API answered.
An HTML 404 here means the request reached the legacy endpoint, which no longer
exists.

**`SENDER_ID_MISMATCH`, and the subscription disappears.** The token belongs to
a different Firebase project. uniqush unsubscribes on this deliberately — the
token can never work for this project — so re-subscribe after fixing the
project.
