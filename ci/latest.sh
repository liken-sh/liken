#!/usr/bin/env bash
#
# Report what the CI workflows pin, and what each upstream has
# released now.
#
# ci/ writes every workflow from its templates, so the templates and
# the composite action in .github/actions/build-setup hold every
# GitHub Action that CI runs. The generator holds the prek version
# that every prek job installs and the Trivy version that every publish
# job installs, and each .pre-commit-config.yaml holds the revision of
# each hook repository. The flux action is not here, because its
# version follows liken/flux/VERSION.
#
# Each pin is built from source by its upstream, so no row has a
# channel. A major tag such as `@v7` takes every release within the
# major version, so a major-tag row compares only the major version of
# the newest release. An exact tag such as `@v2.87.1` compares the
# whole version. The newest release is GitHub's latest release, which
# is never a pre-release or a draft. A hook repository that publishes
# no releases compares against its newest version tag, and so does an
# action whose latest release is not a version of the action:
# github/codeql-action publishes the CodeQL bundle as its releases, and
# tags each version of the action.
#
# Usage:
#   ci/latest.sh                       report every action, prek, and
#                                      each hook repository
#   ci/latest.sh --bump <action>       move one action, such as
#                                      actions/checkout, in every template
#   ci/latest.sh --bump prek           move the prek version
#   ci/latest.sh --bump trivy          move the Trivy version
#   ci/latest.sh --bump hook:<repo>    move one hook repository, such as
#                                      pre-commit/pre-commit-hooks, in
#                                      every .pre-commit-config.yaml

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root="$(cd "$here/.." && pwd)"

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

row() { printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' "$@"; }

# The files that name an action.
workflow_sources() {
    ls "$here"/templates/*
    ls "$root"/.github/actions/*/action.yaml
}

# uses prints "action ref" for each action in the sources, once.
uses() {
    workflow_sources | xargs sed -n 's/.*uses: *\([^ .][^ @]*\)@\([^ {]*\).*/\1 \2/p' |
        grep -v '^fluxcd/flux2/action ' | sort -u
}

# The GitHub API allows 60 anonymous requests an hour, and the report
# sends about 20, so it sends a token when one is at hand.
github_auth=()
token="${GITHUB_TOKEN:-$(gh auth token 2>/dev/null || true)}"
[[ -n "$token" ]] && github_auth=(-H "Authorization: Bearer $token")

newest_release() {
    curl -fsS --retry 3 --max-time 20 "${github_auth[@]}" \
        "https://api.github.com/repos/$1/releases/latest" | jq -r .tag_name
}

newest_tag() {
    git ls-remote --tags --refs "https://github.com/$1" |
        sed 's|.*refs/tags/||' | grep -E '^v?[0-9]+(\.[0-9]+)+$' | sort -V | tail -1
}

# latest_ref <action> <pinned ref>: the ref that the newest release
# gives, in the form of the pinned ref.
latest_ref() {
    local repo release
    repo="$(cut -d/ -f1-2 <<<"$1")"
    release="$(newest_release "$repo")" || release=""
    [[ "$release" =~ ^v?[0-9] ]] || release="$(newest_tag "$repo")" || release=""
    [[ -n "$release" ]] || return 0
    if [[ "$2" =~ ^v[0-9]+$ ]]; then
        sed -E 's/^(v[0-9]+).*/\1/' <<<"$release"
    else
        echo "$release"
    fi
}

pinned_prek() { sed -n 's/^const prekVersion = "\(.*\)"$/\1/p' "$here/workflow.go"; }
pinned_trivy() { sed -n 's/^const trivyVersion = "\(.*\)"$/\1/p' "$here/workflow.go"; }

# hooks prints "repository rev file" for each hook repository of each
# .pre-commit-config.yaml. git's list leaves out the caches, which
# hold upstream copies of the files.
hooks() {
    git -C "$root" ls-files --cached --others --exclude-standard |
        grep -E '(^|/)\.pre-commit-config\.yaml$' | sort -u | while read -r file; do
        [[ -f "$root/$file" ]] || continue
        awk -v f="$root/$file" '/- repo: https:\/\/github\.com\// { r = $3; sub(/https:\/\/github\.com\//, "", r); next }
            r != "" && /rev:/ { print r, $2, f; r = "" }' "$root/$file"
    done
}

report() {
    local action refs latest prek prek_latest trivy trivy_latest repo revs
    uses >"$work/uses"
    while read -r action; do
        refs="$(awk -v a="$action" '$1 == a { print $2 }' "$work/uses" | paste -sd,)"
        latest="$(latest_ref "$action" "${refs%%,*}")"
        if [[ "${refs%%,*}" =~ ^v[0-9]+$ ]]; then
            row "$action" "$refs" "${latest:-?}" - - - "a major tag; the newest major version"
        else
            row "$action" "$refs" "${latest:-?}" - - - "the newest release"
        fi
    done < <(awk '{ print $1 }' "$work/uses" | sort -u)

    prek="$(pinned_prek)"
    prek_latest="$(newest_release j178/prek)" || prek_latest=""
    prek_latest="${prek_latest#v}"
    row prek "$prek" "${prek_latest:-?}" - - - "prekVersion in ci/workflow.go"

    trivy="$(pinned_trivy)"
    trivy_latest="$(newest_release aquasecurity/trivy)" || trivy_latest=""
    trivy_latest="${trivy_latest#v}"
    row trivy "$trivy" "${trivy_latest:-?}" - - - "trivyVersion in ci/workflow.go"

    hooks >"$work/hooks"
    while read -r repo; do
        revs="$(awk -v r="$repo" '$1 == r { print $2 }' "$work/hooks" | sort -uV | paste -sd,)"
        latest="$(newest_tag "$repo")" || latest=""
        row "  hook:$repo" "$revs" "${latest:-?}" - - - "the newest version tag"
    done < <(awk '{ print $1 }' "$work/hooks" | sort -u)
}

if [[ "${1:-}" != "--bump" ]]; then
    report
    exit 0
fi

target="${2:-}"
case "$target" in
"")
    echo "latest.sh: --bump needs a name: an action, prek, trivy, or hook:<repo>" >&2
    exit 1
    ;;
prek)
    old="$(pinned_prek)"
    new="$(newest_release j178/prek)"
    new="${new#v}"
    [[ -n "$new" && "$new" != null ]] || {
        echo "latest.sh: GitHub did not answer for j178/prek" >&2
        exit 1
    }
    sed -i "s/^const prekVersion = \".*\"$/const prekVersion = \"$new\"/" "$here/workflow.go"
    echo "prek: $old -> $new"
    ;;
trivy)
    old="$(pinned_trivy)"
    new="$(newest_release aquasecurity/trivy)"
    new="${new#v}"
    [[ -n "$new" && "$new" != null ]] || {
        echo "latest.sh: GitHub did not answer for aquasecurity/trivy" >&2
        exit 1
    }
    sed -i "s/^const trivyVersion = \".*\"$/const trivyVersion = \"$new\"/" "$here/workflow.go"
    echo "trivy: $old -> $new"
    ;;
hook:*)
    repo="${target#hook:}"
    new="$(newest_tag "$repo")"
    [[ -n "$new" ]] || {
        echo "latest.sh: github.com/$repo has no version tag" >&2
        exit 1
    }
    hooks | awk -v r="$repo" '$1 == r { print $3 }' | sort -u | while read -r file; do
        sed -i "\\|- repo: https://github.com/$repo\$|{n;s|rev: .*|rev: $new|}" "$file"
        echo "${file#"$root"/}: $repo $new"
    done
    ;;
*)
    old="$(uses | awk -v a="$target" '$1 == a { print $2; exit }')"
    [[ -n "$old" ]] || {
        echo "latest.sh: no template uses $target" >&2
        exit 1
    }
    new="$(latest_ref "$target" "$old")"
    [[ -n "$new" && "$new" != null ]] || {
        echo "latest.sh: GitHub did not answer for $target" >&2
        exit 1
    }
    workflow_sources | xargs sed -i "s|uses: $target@[^ {]*|uses: $target@$new|"
    echo "$target: $old -> $new"
    ;;
esac
echo "next: make workflows at the top of the repository"
