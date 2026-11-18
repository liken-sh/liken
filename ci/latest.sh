#!/usr/bin/env bash
#
# Report the versions that the CI workflows pin, next to the newest
# upstream release of each, and bump one of them.
#
# The pins are:
#   - actionPins in ci/actions.go, one line for each repository of
#     GitHub Actions, with a release and the commit of its tag. ci/
#     writes every workflow from that table. The composite actions
#     under .github/actions are written by hand with the same commits.
#   - prekVersion and prekSHA256 in ci/workflow.go.
#   - trivyVersion and trivySHA256 in ci/trivy.go.
#   - the rev of each hook repository in each .pre-commit-config.yaml.
#
# These pins are upstream releases, so the report leaves the channel
# columns empty. "Newest" is GitHub's latest release, which is never a
# pre-release or a draft. A hook repository compares against its
# newest version tag, and so does an action whose latest release is
# not a version number: the latest release of github/codeql-action is
# a CodeQL bundle, and each version of the action is a separate
# release. fluxcd/flux2 compares against liken/flux/VERSION instead,
# because the publish job pushes the deploy artifacts with the flux
# that the OS ships.
#
# A bump pins the commit and not only the tag, because a tag can be
# moved to other code. It refuses a tag that has no GitHub release or
# whose release is a pre-release. For an annotated tag it pins the
# commit that the tag points to, which is what a checkout of the tag
# gets.
#
# Usage:
#   ci/latest.sh                       report every action, prek, and
#                                      each hook repository
#   ci/latest.sh --bump <repository>   bump one repository of actions,
#                                      such as actions/checkout, in
#                                      ci/actions.go and in each
#                                      composite action
#   ci/latest.sh --bump prek           bump prekVersion and prekSHA256
#                                      (needs gh to verify the
#                                      attestation)
#   ci/latest.sh --bump trivy          bump trivyVersion and
#                                      trivySHA256 (needs docker to
#                                      run cosign)
#   ci/latest.sh --bump hook:<repo>    bump one hook repository, such as
#                                      pre-commit/pre-commit-hooks, in
#                                      every .pre-commit-config.yaml

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source-path=SCRIPTDIR source=../toolchains/pin.sh
source "$here/../toolchains/pin.sh"
root="$(cd "$here/.." && pwd)"

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

# action_pins prints "repository release commit" for each line of
# actionPins in ci/actions.go.
action_pins() {
    sed -n 's/^\t{"\([^"]*\)", "\([^"]*\)", "\([0-9a-f]*\)"},$/\1 \2 \3/p' "$here/actions.go"
}

composite_actions() {
    find "$root/.github/actions" -name 'action.y*ml' | sort
}

newest_release() {
    github_api "repos/$1/releases/latest" --max-time 20 | jq -r .tag_name
}

newest_version_tag() {
    newest_tag "https://github.com/$1" '^v?[0-9]+(\.[0-9]+)+$'
}

flux_release() { cat "$root/liken/flux/VERSION"; }

# latest_release <repository> prints the release that actionPins should
# pin for that repository, or nothing when none is found.
latest_release() {
    local release
    if [[ "$1" == fluxcd/flux2 ]]; then
        flux_release
        return
    fi
    release="$(newest_release "$1")" || release=""
    [[ "$release" =~ ^v?[0-9] ]] || release="$(newest_version_tag "$1")" || release=""
    echo "$release"
}

pinned_prek() { sed -n 's/^const prekVersion = "\(.*\)"$/\1/p' "$here/workflow.go"; }
pinned_trivy() { sed -n 's/^const trivyVersion = "\(.*\)"$/\1/p' "$here/trivy.go"; }

# hooks prints "repository rev file" for each hook repository of each
# .pre-commit-config.yaml. It lists the files with git, which skips
# ignored cache directories that contain upstream copies of the file.
hooks() {
    git -C "$root" ls-files --cached --others --exclude-standard |
        grep -E '(^|/)\.pre-commit-config\.yaml$' | sort -u | while read -r file; do
        [[ -f "$root/$file" ]] || continue
        awk -v f="$root/$file" '/- repo: https:\/\/github\.com\// { r = $3; sub(/https:\/\/github\.com\//, "", r); next }
            r != "" && /rev:/ { print r, $2, f; r = "" }' "$root/$file"
    done
}

report() {
    local repo release commit latest prek prek_latest trivy trivy_latest revs
    while read -r repo release commit; do
        latest="$(latest_release "$repo")"
        if [[ "$repo" == fluxcd/flux2 ]]; then
            row "$repo" "$release" "${latest:-?}" - - - "liken/flux/VERSION; ci/actions.go"
        else
            row "$repo" "$release" "${latest:-?}" - - - "the newest release; ci/actions.go"
        fi
    done < <(action_pins)

    prek="$(pinned_prek)"
    prek_latest="$(newest_release j178/prek)" || prek_latest=""
    prek_latest="${prek_latest#v}"
    row prek "$prek" "${prek_latest:-?}" - - - "prekVersion and prekSHA256 in ci/workflow.go"

    trivy="$(pinned_trivy)"
    trivy_latest="$(newest_release aquasecurity/trivy)" || trivy_latest=""
    trivy_latest="${trivy_latest#v}"
    row trivy "$trivy" "${trivy_latest:-?}" - - - "trivyVersion and trivySHA256 in ci/trivy.go"

    hooks >"$work/hooks"
    while read -r repo; do
        revs="$(awk -v r="$repo" '$1 == r { print $2 }' "$work/hooks" | sort -uV | paste -sd,)"
        latest="$(newest_version_tag "$repo")" || latest=""
        row "  hook:$repo" "$revs" "${latest:-?}" - - - "the newest version tag"
    done < <(awk '{ print $1 }' "$work/hooks" | sort -u)
}

if [[ "${1:-}" != "--bump" ]]; then
    report
    exit 0
fi

target="${2:-}"
case "$target" in
"") fail "--bump needs a name: a repository of actions, prek, trivy, or hook:<repo>" ;;
prek)
    old="$(pinned_prek)"
    new="$(newest_release j178/prek)" || new=""
    new="${new#v}"
    [[ "$new" != null ]] || new=""
    bump_needed prek "$old" "$new" || exit 0
    # The digest comes from the release's .sha256 file. The bump also
    # checks that the archive matches it and has a GitHub attestation
    # from prek's release workflow, so a changed release asset fails
    # here and not in CI.
    asset="prek-x86_64-unknown-linux-gnu.tar.gz"
    base="https://github.com/j178/prek/releases/download/v$new"
    fetch_file "$base/$asset" "$work/$asset"
    digest="$(fetch_text "$base/$asset.sha256" --max-time 20 | awk '{ print $1 }')" || digest=""
    [[ "$digest" =~ ^[0-9a-f]{64}$ ]] || fail "$base/$asset.sha256 holds no SHA-256 digest"
    actual="$(sha256sum "$work/$asset" | awk '{ print $1 }')"
    [[ "$actual" == "$digest" ]] || fail "$asset is $actual, and its .sha256 file says $digest"
    gh attestation verify "$work/$asset" --repo j178/prek \
        --signer-workflow j178/prek/.github/workflows/release.yml >/dev/null ||
        fail "$asset carries no attestation of prek's release workflow"
    set_line "$here/workflow.go" '^const prekVersion = ".*"$' "const prekVersion = \"$new\""
    set_line "$here/workflow.go" '^const prekSHA256 = ".*"$' "const prekSHA256 = \"$digest\""
    bumped prek "$old" "$new, $asset $digest"
    ;;
trivy)
    old="$(pinned_trivy)"
    new="$(newest_release aquasecurity/trivy)" || new=""
    new="${new#v}"
    [[ "$new" != null ]] || new=""
    bump_needed trivy "$old" "$new" || exit 0
    # Trivy signs each release asset keyless with Sigstore and
    # publishes the bundle beside it as <asset>.sigstore.json. cosign
    # accepts the archive only when the signer is the reusable-release
    # workflow of aquasecurity/trivy at the release's tag. HOME points
    # at a writable directory because cosign caches the Sigstore trust
    # root there, and the container runs as the calling user.
    require_tools docker
    asset="trivy_${new}_Linux-64bit.tar.gz"
    base="https://github.com/aquasecurity/trivy/releases/download/v$new"
    identity="https://github.com/aquasecurity/trivy/.github/workflows/reusable-release.yaml@refs/tags/v$new"
    fetch_file "$base/$asset" "$work/$asset"
    fetch_file "$base/$asset.sigstore.json" "$work/$asset.sigstore.json"
    docker run --rm --user "$(id -u):$(id -g)" -e HOME=/tmp \
        -v "$work:/work:ro" "$(image_of cosign)" verify-blob \
        --bundle "/work/$asset.sigstore.json" \
        --certificate-identity "$identity" \
        --certificate-oidc-issuer https://token.actions.githubusercontent.com \
        "/work/$asset" >&2 || fail "$asset does not verify as signed by $identity"
    digest="$(sha256sum "$work/$asset" | awk '{ print $1 }')"
    set_line "$here/trivy.go" '^const trivyVersion = ".*"$' "const trivyVersion = \"$new\""
    set_line "$here/trivy.go" '^const trivySHA256 = ".*"$' "const trivySHA256 = \"$digest\""
    bumped trivy "$old" "$new, $asset $digest"
    echo "the digest came from the archive, whose sigstore bundle verified as Trivy's release workflow"
    ;;
hook:*)
    repo="${target#hook:}"
    new="$(newest_version_tag "$repo")" || new=""
    [[ -n "$new" ]] || fail "github.com/$repo has no version tag"
    hooks | awk -v r="$repo" '$1 == r { print $3 }' | sort -u | while read -r file; do
        sed -i "\\|- repo: https://github.com/$repo\$|{n;s|rev: .*|rev: $new|}" "$file"
        echo "${file#"$root"/}: $repo $new"
    done
    ;;
*)
    repo="$(cut -d/ -f1-2 <<<"$target")"
    old="$(action_pins | awk -v r="$repo" '$1 == r { print $2 }')"
    [[ -n "$old" ]] || fail "actionPins in ci/actions.go has no line for $repo"
    new="$(latest_release "$repo")"
    [[ "$new" != null ]] || new=""
    bump_needed "$repo" "$old" "$new" || exit 0
    prerelease="$(github_api "repos/$repo/releases/tags/$new" --max-time 20 | jq -r .prerelease)" ||
        fail "github.com/$repo publishes no release for the tag $new"
    [[ "$prerelease" == false ]] || fail "$repo $new is a pre-release"
    commit="$(tag_commit "https://github.com/$repo" "$new")"
    set_line "$here/actions.go" "^[[:space:]]+[{]\"${repo//./\\.}\", " "$(printf '\t{"%s", "%s", "%s"},' "$repo" "$new" "$commit")"
    while read -r file; do
        sed -i -E "s|(uses: ${repo//./\\.})(/[^@ ]*)?@[^ ]*( # [^ ]*)?$|\1\2@$commit # $new|" "$file"
    done < <(composite_actions)
    bumped "$repo" "$old" "$new, the commit $commit"
    echo "the commit came from the tag $new, which GitHub publishes as a release that is not a pre-release"
    ;;
esac
next_step "make workflows at the top of the repository"
