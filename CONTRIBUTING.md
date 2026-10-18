# Contributing to uniqush-push

Bug reports, questions and suggestions go in the
[issue tracker](https://github.com/uniqush/uniqush-push/issues). Security
problems do not: see [SECURITY.md](SECURITY.md).

## Building and testing

You need Go 1.26 or newer. The tests that use redis expect it on
`localhost:6379` and skip when it isn't there; CI runs them.

These are the checks CI runs on a pull request:

```
go build ./...
./govet.sh
go test -race ./...
go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run --new-from-merge-base=master ./...
go run golang.org/x/vuln/cmd/govulncheck@latest ./...
```

golangci-lint only gates on new findings, because there is a backlog of old
ones.

## Pull requests

Open them against `master`, one change per pull request.

- **Test what you change.** A test should check behavior, not restate the
  implementation.
- **Justify every change.** The commit message says why the change is needed,
  and a comment says why the code is the way it is, not what it does.
- **Keep the diff small.** Unrelated cleanups go in their own pull request.
- **Update the docs it affects.** A user-visible change gets a line under
  `Unreleased` in [NEWS.md](NEWS.md), in the style of the entries already
  there. A change to an endpoint updates [docs/api.md](docs/api.md).
- **Think twice about new dependencies.** uniqush talks to APNs, FCM and the
  rest over `net/http` rather than through vendor SDKs, to keep the
  dependency tree small. A new module needs a reason.

## AI-assisted contributions

AI contributions are welcome. As with any contribution, they should be well
tested and every change justified.

Pull requests are also checked by
[no-ai-marks](https://github.com/mishan/no-ai-marks), which fails on the
attribution AI tools add by default: `Co-authored-by` trailers naming a tool
or model, "Generated with" footers, links to AI sessions, the robot emoji,
branch names such as `claude/fix-push`, and hidden or look-alike Unicode
characters. It reads commit messages, the pull request title and description,
the branch name, and the lines your change adds.

It doesn't judge how a change was written, only what it carries, so an
AI-assisted change passes once those lines are left out. `Co-authored-by` for
a person is fine. To check before you push:

```
pipx run no-ai-marks range origin/master
```

If the pull request description is what failed, editing it re-runs the check.

## License

uniqush-push is under the [Apache 2.0 License](LICENSE), and contributions are
accepted under the same license.
