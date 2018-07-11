#!/bin/bash
# Checks(vet) this project for golang errors.
# Prints errors and returns a non-zero exit code on failure.
find . -type f -name '*.go' -not -path "./vendor/*" | \
	xargs go tool vet -printfuncs Debugf,Infof,Configf,Warnf,Errorf,Alertf,Fatalf -printf -all
