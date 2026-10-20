#!/bin/sh
set -e

# Up to 2.10.0 the config was not a conffile, so dpkg has no record of what was
# installed and asks about it on the first upgrade even if it is untouched --
# and fails without a terminal. Remove it when it is the stock one from 2.8.0
# to 2.10.0, so dpkg installs the new one without asking. An edited config is
# left alone: dpkg asks once, and keeps it by default.
conf=/etc/uniqush/uniqush-push.conf
if [ "$1" = upgrade ] && dpkg --compare-versions "$2" lt 2.11.0 && [ -f "$conf" ]; then
	case "$(md5sum < "$conf")" in
	"1e0ef82143c7883a64c4ef90657cac2f  -" | "523b066b7cc731013595f5fa8e922001  -")
		rm -f "$conf"
		;;
	esac
fi
