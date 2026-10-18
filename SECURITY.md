# Security policy

## Reporting a vulnerability

Report it privately through
[GitHub's vulnerability reporting](https://github.com/uniqush/uniqush-push/security/advisories/new),
not in a public issue. Include the version, how to reproduce it, and what an
attacker gains.

The fix is developed in a private advisory and released before the details
are made public. You'll be credited in the advisory unless you'd rather not be.

## Supported versions

Only the latest release gets security fixes. Releases before 2.8.0 can no
longer deliver to APNs or FCM at all, so upgrade from those regardless.

## Scope

The REST API has no authentication, by design: it is meant to be reached only
by your own application servers, and must sit behind something that
authenticates (see [docs/api.md](docs/api.md)). That an unauthenticated caller
can use the API is therefore not a vulnerability. Getting around a protection
uniqush does have is: reaching a private address through a Web Push endpoint,
say, or reading credentials back out of `/psps` or `/subscriptions`.

A vulnerability in a dependency is worth reporting when uniqush reaches the
vulnerable code. CI runs `govulncheck`, so most of those are caught already.
