#!/usr/bin/env bash
# Starts the compositor image once, headless, before it ships.
#
# Usage: weston.sh <image>
#
#   <image>  the full reference of the weston image, for example
#            ghcr.io/liken-sh/weston:2026.10.02-001. The image must be
#            in the local Docker daemon.
#
# The compositor opens its backend, its renderer, its shell and its
# modules by file name, so a library the image does not contain shows
# up at the first start and at no earlier point. `go test` covers none
# of it. The headless backend needs no graphics card, so this runs on
# an ordinary runner, and it covers everything but the card: the
# backend, the EGL vendor, the DRI driver, the shell, and the
# operator's own controller module.
#
# The config states the shell and the module that the operator itself
# writes into `weston.ini` (`weston.go`, `westonConfig`). weston prints
# a "Loading module" line before it opens a file, whether or not the
# file is there, so the check greps the lines that only a loaded
# library prints. `ivi-shell` registers its layout API, and
# `liken-layout` reports the control socket it listens on. The module
# prints that line only after `ivi_layout_get_api` answers, so the
# second line proves both loads.
#
# The module puts its control socket in `/etc/weston`, which is the
# compositor pod's config volume, so the run gives it a writable tmpfs
# where the pod gives it an `emptyDir`.
#
# weston runs until it is stopped, so `timeout` is what ends it, and
# the log is what says whether it got that far.
set -euo pipefail

image=$1

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

printf '[core]\nshell=ivi-shell.so\nmodules=liken-layout.so\nrenderer=gl\nrequire-input=false\n' > "$work/weston.ini"
timeout 30 docker run --rm --tmpfs /run --tmpfs /etc/weston \
  -e XDG_RUNTIME_DIR=/run \
  -v "$work/weston.ini:/weston.ini:ro" \
  "$image" \
  --backend=headless --config=/weston.ini --socket=release \
  > "$work/weston.log" 2>&1 || true
cat "$work/weston.log"
grep -q "Using GL renderer" "$work/weston.log"
grep -q "Registered plugin API 'ivi_layout_api_v1'" "$work/weston.log"
grep -q "liken-layout: the control socket is /etc/weston/layout.sock" "$work/weston.log"
