# ADM simulator

Use `NewServer()` and pass its `Client()` to `srv.NewADMPushService`. Requests to
`https://api.amazon.com` are routed to a local HTTP server; other hosts are
rejected, so no Amazon credentials or Kindle are needed. Close the client idle
connections and the server after use.

The synthetic OAuth credentials are `test-client` / `test-secret`. The simulator
checks the token request, message headers and JSON payload, and lets tests set a
status, reason and retry header per registration ID. `Counts()` checks token
reuse and message submission. This is not a complete ADM implementation: it does
not simulate device delivery, registration changes, expiry or rate limits.

The protocol is based on Amazon's [token exchange](https://developer.amazon.com/docs/adm/request-access-token.html)
and [send-message documentation](https://developer.amazon.com/docs/adm/send-message.html).
The latter distinguishes `Unregistered` (the app instance is unavailable) from
`InvalidRegistrationId` (the ID does not correspond to the sender). The current
service tests characterize existing classifications; they do not establish that
retaining `Unregistered` registrations is correct or that sender mismatches
should delete subscriptions.
