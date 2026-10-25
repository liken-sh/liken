#!/usr/bin/env bash
#
# Print one table of every pin in the repository: what each pin names
# now, what its upstream offers, and whether the pin follows the rule
# in root plan 83.
#
# Each component that pins something keeps a latest.sh beside the pin,
# because which upstream to ask and how to read its answer is that
# component's own knowledge. This script finds every latest.sh in the
# tree, runs them, and decides the state of each row in one place, so
# that no two components can disagree on what "behind" means.
#
# A latest.sh prints tab-separated rows of seven columns: name, pinned,
# latest, channel, newest-channel, hold, and note. A sub-pin keeps the
# indented name that its latest.sh gives it, and its row follows the
# row of the pin it belongs to.
#
# The scripts run at the same time, because each one waits on a
# different upstream, and one slow server must not hold up the others
# in turn. Each script gets five minutes. A script that fails or runs
# out of time prints one `unknown` row with its directory, and the
# report goes on, because one broken upstream must not hide the state
# of every other pin.
#
# Usage:
#   ./versions.sh     run from anywhere; `make versions` runs it

set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

# Caches, build output, and vendored packages can hold a copy of a
# latest.sh that is not a pin of this repository.
mapfile -t scripts < <(cd "$root" && find . -name latest.sh \
    -not -path '*/.cache/*' -not -path '*/dist/*' \
    -not -path '*/node_modules/*' -not -path '*/cache/*' | sort)

for i in "${!scripts[@]}"; do
    (
        dir="${scripts[$i]#./}"
        dir="${dir%/latest.sh}"
        if timeout 300 "$root/$dir/latest.sh" >"$work/$i.rows" 2>"$work/$i.err"; then
            awk -F'\t' -v OFS='\t' -v dir="$dir" '{ print dir, $0 }' "$work/$i.rows"
        else
            reason="$(tail -1 "$work/$i.err")"
            printf '%s\t%s\t?\t?\t?\t?\t-\tlatest.sh failed: %s\n' \
                "$dir" "${dir##*/}" "${reason:-no message}"
        fi >"$work/$i.out"
    ) &
done
wait

# The state comes from the first column that matches, in this order. A
# hold is the decision of a person, so it names the state even when
# the upstream did not answer. An unknown row cannot say more. A pin
# on an old channel is wrong whatever its version is in that channel.
#
# The name column joins the directory and the row's name. A row named
# for its own directory, such as k3s in liken/k3s, shows the directory
# alone. A sub-pin keeps its indent and no directory, because the row
# above it names the directory. A row with fewer than seven columns is
# unknown, because the script broke the contract.
for i in "${!scripts[@]}"; do
    cat "$work/$i.out"
done | awk -F'\t' -v OFS='\t' '
    BEGIN { print "COMPONENT", "PINNED", "LATEST", "CHANNEL", "NEWEST", "HOLD", "STATE", "NOTE" }
    {
        dir = $1; name = $2
        n = split(dir, parts, "/")
        if (name ~ /^ /) component = name
        else if (name == parts[n]) component = dir
        else component = dir "/" name
        if (NF != 8) {
            print component, $3, "?", "?", "?", "-", "unknown", "latest.sh printed " NF - 1 " columns, not 7"
            next
        }
        if ($7 != "-") state = "held"
        else if ($4 == "?" || $6 == "?") state = "unknown"
        else if ($5 != $6) state = "wrong channel"
        else if ($3 != $4) state = "behind"
        else state = "current"
        print component, $3, $4, $5, $6, $7, state, $8
    }' | column -t -s "$(printf '\t')"
