#!/bin/sh
# The entrypoint of the indi-phd2 image. It copies the profile into
# $HOME, removes PHD2's instance lock, and starts PHD2 in place of this
# shell, so that PHD2 receives the container's signals.
#
# PHD2 rewrites its config while it runs, and it can move the current
# profile to a new, empty one. So every start reads the whole profile
# again from /etc/phd2/PHDGuidingV2, a file that the pod mounts, and
# keeps none of what the previous run wrote. Without a mounted profile,
# PHD2 starts with the config in $HOME, or with none.
#
# PHD2 writes its process ID to the lock $HOME/phd2.<instance>. In a
# container PHD2 is process 1 every time, so a lock that a crash left
# names a process that is running: PHD2 itself. PHD2 then reports that
# another instance runs, and quits. The lock stays in an emptyDir when
# the container restarts, so the script removes it before each start.
set -eu

profile=/etc/phd2/PHDGuidingV2
if [ -f "$profile" ]; then
	cp "$profile" "$HOME/.PHDGuidingV2"
fi
rm -f "$HOME"/phd2.[0-9]*
exec /usr/bin/phd2.bin "$@"
