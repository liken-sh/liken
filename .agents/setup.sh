#!/bin/sh
# Point the organization root's AGENTS.md at this repository's copy, so
# the harness reads the same organization rules from every repository
# below it. Run once after cloning, and again when the organization
# directory moves.

set -eu

agents_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
org_dir=$(dirname -- "$agents_dir")
agents_name=$(basename -- "$agents_dir")

ln -sfn "$agents_name/AGENTS.md" "$org_dir/AGENTS.md"
