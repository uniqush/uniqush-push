# APNs test fixtures

`localhost.cert` and `localhost.key` are a certificate and key pair that tests
name when they build a provider through `/addpsp` or
`BuildPushServiceProviderFromMap`.

They only ever have to load. Nothing verifies them: the simulator in
`srv/apns/apnstest` does not check the client certificate, because modelling
Apple's certificate authority adds nothing to what those tests cover, and every
test that reaches a network sets `skipverify=true` or supplies the simulator's
own CA. That is why it does not matter that this certificate expired on
2022-12-19 — a provider needs a loadable pair, and this is one.

New tests should prefer `apnstest.GenerateClientCert`, which writes a freshly
generated pair into a `t.TempDir()`. These files stay because eight existing
tests name them by path.

## What used to be here

`apns-test.sh` drove a local uniqush against `uniqush/apns-simulator` over the
binary provider protocol, which Apple switched off on 2021-03-31. It was
unrunnable on three counts by the time it was removed: the protocol is gone, the
simulator repository is archived and predates Go modules, and the certificate it
passed to `/addpsp` had expired.

What it was checking is now checked in Go, without a second process:

- `srv/apns/conformance_test.go` drives the HTTP/2 push path against
  `srv/apns/apnstest`, and asserts on the headers and payload Apple would see
- `rebinding_acceptance_test.go` walks the same add-provider, subscribe, push
  sequence, through the real backend
- `go test -tags apns_live ./srv/apns/http_api/` probes Apple's real sandbox

One thing the script did is not replaced: it called `/subscriptions`, `/psps`
and `/rebuildserviceset` over HTTP, so it was also a smoke test of the REST
surface. The Go tests build providers and subscriptions through the packages
directly and never serve a request, so those three endpoints have no automated
coverage. Retiring the script does not lose that — it had not been runnable for
years — but it does not supply it either.

`localhost.p8` went with it. It was added for the JWT manager's tests, which
generate their own signing keys through `apnstest.GenerateSigningKey`, and no
test referred to it.
