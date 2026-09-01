#!/bin/bash
# Checks(vet) this project for golang errors.
# Prints errors and returns a non-zero exit code on failure.
set -o pipefail

go vet -printfuncs Debugf,Infof,Configf,Warnf,Errorf,Alertf,Fatalf -printf -all ./... || exit 1

# CI runs golangci-lint, which enables govet's shadow analyzer (see
# .golangci.yml). `go vet` does not run shadow by default and offers no flag to
# turn it on, so the check above is weaker than the one the pull request will
# face, and a shadowed variable gets discovered in CI rather than in the
# terminal it was written in.
#
# The analyzer ships as a separate binary. Use it when it is installed, and say
# so when it is not -- a check that quietly does less than it appears to is
# worse than one that is honestly absent.
SHADOW=$(command -v shadow)
if [ -n "$SHADOW" ]; then
	go vet -vettool="$SHADOW" ./... || exit 1
else
	echo "note: shadow analyzer not installed, so this skipped a check CI will run." >&2
	echo "      go install golang.org/x/tools/go/analysis/passes/shadow/cmd/shadow@latest" >&2
fi
