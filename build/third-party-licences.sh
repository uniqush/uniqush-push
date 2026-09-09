#!/bin/bash
# Regenerates THIRD-PARTY-LICENSES from the modules the binary actually links.
#
# Run it after changing dependencies. The file it writes is checked in, because
# the licences have to be in the release artifacts whether or not a release
# machine has a module cache to read them from.
#
# The list comes from `go list -deps` on the main package rather than from
# go.mod, so it covers what is linked into the binary and nothing else: a module
# required for tests or tooling does not ship, and does not belong here.
set -euo pipefail

cd "$(dirname "$0")/.."
output="THIRD-PARTY-LICENSES"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

# licenceFileFor prints the path of a module's licence, or nothing. Most modules
# keep it at the root; goconf keeps its COPYRIGHT one level down, so look there
# too rather than silently omitting a licence that exists.
licenceFileFor() {
	local dir="$1" candidate
	for candidate in "$dir"/LICENSE "$dir"/LICENSE.txt "$dir"/LICENSE.md \
		"$dir"/COPYING "$dir"/COPYRIGHT "$dir"/*/COPYRIGHT "$dir"/*/LICENSE; do
		if [ -f "$candidate" ]; then
			echo "$candidate"
			return
		fi
	done
}

# identify prints an SPDX identifier for a licence file.
identify() {
	local file="$1"
	# Order matters twice over. BSD-3 is BSD-2 plus a non-endorsement clause, so
	# that clause is tested first. And the Apache test looks for the licence's
	# own title line, not the word Apache, so a file whose preamble mentions
	# Apache -- conf/LICENSE says its terms are not this project's Apache ones --
	# is not mistaken for one.
	if grep -qi "Apache License, Version 2.0\|Apache License$" "$file"; then echo "Apache-2.0"
	elif grep -qi "Permission is hereby granted, free of charge" "$file"; then echo "MIT"
	elif grep -qi "Neither the name" "$file"; then echo "BSD-3-Clause"
	elif grep -qi "Redistribution and use" "$file"; then echo "BSD-2-Clause"
	else echo "UNKNOWN"; fi
}

while read -r module version; do
	[ -z "$module" ] && continue
	[ "$module" = "github.com/uniqush/uniqush-push" ] && continue

	dir=$(go list -m -f '{{.Dir}}' "$module")
	if [ -z "$dir" ]; then
		echo "no module directory for $module; run go mod download" >&2
		exit 1
	fi

	licence=$(licenceFileFor "$dir")
	if [ -z "$licence" ]; then
		echo "no licence file found for $module in $dir" >&2
		exit 1
	fi

	spdx=$(identify "$licence")
	if [ "$spdx" = "UNKNOWN" ]; then
		echo "could not identify the licence of $module ($licence)" >&2
		exit 1
	fi

	# Group by the exact text, so the five modules sharing the Go Authors'
	# licence reproduce it once rather than five times.
	hash=$(sha256sum "$licence" | cut -d' ' -f1)
	mkdir -p "$work/$hash"
	cp -f "$licence" "$work/$hash/text"
	echo "$spdx" >"$work/$hash/spdx"
	echo "    $module $version" >>"$work/$hash/modules"
done < <(go list -deps -f '{{if .Module}}{{.Module.Path}} {{.Module.Version}}{{end}}' . | sort -u)

# Code in this repository under a licence other than uniqush-push's own carries
# its own LICENSE beside it, and is just as linked into the binary as a module
# is. Anything vendored this way in future is picked up by being placed the same
# way, rather than by remembering to edit a list.
while read -r licence; do
	[ "$licence" = "./LICENSE" ] && continue

	spdx=$(identify "$licence")
	if [ "$spdx" = "UNKNOWN" ]; then
		echo "could not identify the licence in $licence" >&2
		exit 1
	fi

	hash=$(sha256sum "$licence" | cut -d' ' -f1)
	mkdir -p "$work/$hash"
	cp -f "$licence" "$work/$hash/text"
	echo "$spdx" >"$work/$hash/spdx"
	echo "    $(dirname "${licence#./}")/ (in this repository)" >>"$work/$hash/modules"
done < <(find . -path ./.git -prune -o \( -name LICENSE -o -name COPYRIGHT \) -print | sort)

{
	cat <<'HEADER'
Third-party licences
====================

uniqush-push itself is licensed under the Apache License 2.0; see LICENSE.

The released binary statically links the Go modules listed below, along with any
code in this repository carrying a licence of its own, so all of it is inside
the binary. The BSD and MIT licences require their copyright notice and
disclaimer to accompany a binary distribution, which is what this file is for.
It ships in the release archive and installs to
/usr/share/doc/uniqush-push/ in the .deb and .rpm.

Regenerate with build/third-party-licences.sh after changing dependencies or
vendoring code.

HEADER

	for dir in $(ls -d "$work"/*/ | sort); do
		echo "================================================================================"
		echo "$(cat "$dir/spdx") -- covering:"
		echo
		sort "$dir/modules"
		echo
		echo "--------------------------------------------------------------------------------"
		echo
		cat "$dir/text"
		echo
	done
} >"$output"

echo "wrote $output"
