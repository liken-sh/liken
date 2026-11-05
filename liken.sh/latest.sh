#!/usr/bin/env bash
#
# Report what the liken.sh domain pins, and what each upstream offers
# now.
#
# The domain pins the Terraform providers, in terraform.tf.
#
# The providers come from the Terraform registry, and each upstream
# builds its provider from source, so their rows have no channel. A
# constraint such as `~> 3.0` takes every release within the major
# version, so its row compares only the major version of the newest
# release. An exact version compares the whole version.
#
# Usage:
#   liken.sh/latest.sh                    report every provider
#   liken.sh/latest.sh --bump <provider>  move one provider, such as
#                                         provider:linode, in terraform.tf

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source-path=SCRIPTDIR source=../toolchains/pin.sh
source "$here/../toolchains/pin.sh"

# providers prints "name source constraint" for each provider in the
# required_providers block.
providers() {
    awk '/required_providers/ { in_block = 1; next }
        in_block && /^  }/ { exit }
        in_block && /^ *[a-z]+ = \{/ { name = $1 }
        in_block && /^ *source *=/ { gsub(/"/, "", $3); source = $3 }
        in_block && /^ *version *=/ { sub(/.*= *"/, ""); sub(/".*/, ""); print name, source, $0 }' \
        "$here/terraform.tf"
}

newest_provider() {
    fetch_text "https://registry.terraform.io/v1/providers/$1/versions" --max-time 20 |
        jq -r '.versions[].version' | grep -E '^[0-9]+\.[0-9]+\.[0-9]+$' | sort -V | tail -1
}

# latest_constraint <constraint> <newest>: the newest release, in the
# form of the pinned constraint.
latest_constraint() {
    if [[ "$1" == "~> "* ]]; then
        echo "~> ${2%%.*}.0"
    else
        echo "$2"
    fi
}

if [[ "${1:-}" != "--bump" ]]; then
    providers | while read -r name source constraint; do
        newest="$(newest_provider "$source")" || newest=""
        latest="?"
        [[ -z "$newest" ]] || latest="$(latest_constraint "$constraint" "$newest")"
        row "provider:$name" "$constraint" "$latest" - - - "$source; the newest release is ${newest:-?}"
    done
    exit 0
fi

target="${2:-}"
target="${target#provider:}"
read -r _ source constraint < <(providers | awk -v n="$target" '$1 == n') ||
    fail "terraform.tf has no provider named ${target:-?}"
newest="$(newest_provider "$source")" || newest=""
[[ -n "$newest" ]] || fail "the Terraform registry did not answer for $source"
new="$(latest_constraint "$constraint" "$newest")"
bump_needed "$target" "$constraint" "$new" || exit 0
sed -i "\\|source  = \"$source\"|{n;s|version = \".*\"|version = \"$new\"|}" "$here/terraform.tf"
bumped "$target" "$constraint" "$new"
next_step "terraform init -upgrade and terraform plan in liken.sh, and read the provider's changelog"
