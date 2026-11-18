package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// actionPin is one repository of GitHub Actions that a workflow uses:
// a release tag of the repository, and the commit that the tag points
// at.
type actionPin struct {
	Repository, Version, Commit string
}

// actionPins lists each repository of actions once, at the commit of a
// release. The owner of a repository can move a tag such as v7 or
// v7.0.1 to other code, and the publish jobs run these actions with a
// token that can write to the registry and an OIDC token that signs
// images. A commit always refers to the same code. Each reference in a
// workflow has the release as a trailing comment, as in
// `actions/checkout@<commit> # <release>`, which Dependabot reads.
//
// `ci/latest.sh` reads this table with sed, so keep each entry on one
// line in this form. `ci/latest.sh --bump <repository>` rewrites that
// repository's line, and the uses: lines of each composite action under
// .github/actions, with the newest release and the commit of its tag.
// The fluxcd/flux2 line must match liken/flux/VERSION, so the publish
// job pushes each deploy artifact with the flux that the OS ships.
// `liken/flux/latest.sh --bump` updates it along with the flux pin.
var actionPins = []actionPin{
	{"Swatinem/rust-cache", "v2.9.3", "d1527c8f67ef9af357290475d4af4b0fab8f12e0"},
	{"actions/cache", "v6.1.0", "55cc8345863c7cc4c66a329aec7e433d2d1c52a9"},
	{"actions/checkout", "v7.0.1", "3d3c42e5aac5ba805825da76410c181273ba90b1"},
	{"actions/deploy-pages", "v5.0.1", "368f82528645a54fb793d4d04e342629a3f51346"},
	{"actions/download-artifact", "v8.0.2", "9000827ccba6bdab643e8b6fd33ac0654aef8333"},
	{"actions/setup-go", "v7.0.0", "b7ad1dad31e06c5925ef5d2fc7ad053ef454303e"},
	{"actions/upload-artifact", "v7.0.2", "cf430e030ddbb5b0abf93d22962f4752f3646cd9"},
	{"actions/upload-pages-artifact", "v5.0.0", "fc324d3547104276b827a68afc52ff2a11cc49c9"},
	{"docker/bake-action", "v7.5.0", "59cb1203cd42bcefd40a1b6d73b589e48c86040e"},
	{"docker/login-action", "v4.7.0", "81c6f93d4bc991e510b342a00b419b3bb4efc5c6"},
	{"docker/setup-buildx-action", "v4.4.1", "f87e5991a6d7451dcb8d9637bfbc97413f497069"},
	{"fluxcd/flux2", "v2.10.0", "3fa784edd773d56630bb9f62645e08f70c2cff0a"},
	{"github/codeql-action", "v4.39.1", "9b3254085494b55a0f7c4c2b3d2d52cc712ce370"},
	{"j178/prek-action", "v3.1.0", "44924fe9ef4a6f88253c4b6b1bf11c689d3ce53d"},
	{"taiki-e/install-action", "v2.88.1", "68a389c788b2cd04016ce8c80ee23b80d2ab3d68"},
}

const fluxActionRepository = "fluxcd/flux2"

// actionRepository returns the repository of an action: the first two
// parts of a name such as github/codeql-action/init.
func actionRepository(action string) string {
	parts := strings.SplitN(action, "/", 3)
	if len(parts) < 2 {
		return action
	}
	return parts[0] + "/" + parts[1]
}

// pinOf returns the line of actionPins for the repository of an action.
func pinOf(action string) (actionPin, bool) {
	repository := actionRepository(action)
	i := slices.IndexFunc(actionPins, func(p actionPin) bool { return p.Repository == repository })
	if i < 0 {
		return actionPin{}, false
	}
	return actionPins[i], true
}

// pinnedAction returns the text that a workflow template writes after
// uses: for an action: its name, the pinned commit, and the release as
// a comment.
func pinnedAction(action string) (string, error) {
	pin, ok := pinOf(action)
	if !ok {
		return "", fmt.Errorf("%s is not in actionPins in ci/actions.go", action)
	}
	return action + "@" + pin.Commit + " # " + pin.Version, nil
}

func pinnedRepositories() []string {
	var repositories []string
	for _, pin := range actionPins {
		repositories = append(repositories, pin.Repository)
	}
	slices.Sort(repositories)
	return repositories
}

// checkFluxAction returns an error when the flux action's release
// differs from the flux version that the OS ships.
func checkFluxAction(flux string) error {
	pin, _ := pinOf(fluxActionRepository)
	if pin.Version != "v"+flux {
		return fmt.Errorf("%s sets flux v%s, and actionPins in ci/actions.go runs %s %s. Run ci/latest.sh --bump %s",
			fluxVersionFile, flux, fluxActionRepository, pin.Version, fluxActionRepository)
	}
	return nil
}

// usesLine matches a uses: line of a step or a job, and captures its
// reference and what follows it on the line.
var usesLine = regexp.MustCompile(`^\s*(?:-\s+)?uses:\s*(\S+)(.*)$`)

// pinnedReference matches a reference to an action of another
// repository that names a full commit: <action>@<40 hex digits>.
var pinnedReference = regexp.MustCompile(`^([\w.-]+/[\w.-]+(?:/[\w./-]+)?)@([0-9a-f]{40})$`)

// releaseComment matches the trailing comment that names the release,
// such as "  # v1.2.3".
var releaseComment = regexp.MustCompile(`^\s+# (v\d+\.\d+\.\d+)$`)

// pinnedDocker matches a docker:// action that names its image by
// digest.
var pinnedDocker = regexp.MustCompile(`^docker://[^@\s]+@sha256:[0-9a-f]{64}$`)

// checkActions returns a finding for each uses: line under .github
// that runs code that this repository does not pin: an action
// by a tag or a branch, an action at a commit or release that differs
// from actionPins, and a container image by a tag. It reads the
// generated workflows, the hand-written workflows, and the composite
// actions under .github/actions, because each runs with the token of
// the job that calls it. A local reference, which starts with ./, runs
// code from the same commit and passes.
func checkActions(root string) ([]string, error) {
	files, err := sourceFiles(root)
	if err != nil {
		return nil, err
	}
	var findings []string
	for _, file := range files {
		if !strings.HasPrefix(file, ".github/") || (path.Ext(file) != ".yaml" && path.Ext(file) != ".yml") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, file))
		if errors.Is(err, fs.ErrNotExist) {
			// sourceFiles lists files that git tracks, and the working tree may
			// have deleted one.
			continue
		}
		if err != nil {
			return nil, err
		}
		for n, line := range strings.Split(string(data), "\n") {
			if finding := unpinnedAction(line); finding != "" {
				findings = append(findings, fmt.Sprintf("%s:%d %s", file, n+1, finding))
			}
		}
	}
	return findings, nil
}

// unpinnedAction returns what is wrong with the action reference on
// one line, or an empty string when the line runs pinned code or has no
// reference.
func unpinnedAction(line string) string {
	m := usesLine.FindStringSubmatch(line)
	if m == nil || strings.HasPrefix(m[1], "./") {
		return ""
	}
	ref, rest := m[1], m[2]
	if strings.HasPrefix(ref, "docker://") {
		if pinnedDocker.MatchString(ref) {
			return ""
		}
		return fmt.Sprintf("runs %s, which has no image digest", ref)
	}
	p := pinnedReference.FindStringSubmatch(ref)
	if p == nil {
		return fmt.Sprintf("runs %s, which has no commit. Write it as <action>@<commit> # <release>", ref)
	}
	release := releaseComment.FindStringSubmatch(rest)
	if release == nil {
		return fmt.Sprintf("runs %s with no comment that states its release, such as # v1.2.3", ref)
	}
	pin, ok := pinOf(p[1])
	if !ok {
		return fmt.Sprintf("runs %s, whose repository is not in actionPins in ci/actions.go", p[1])
	}
	if p[2] != pin.Commit || release[1] != pin.Version {
		return fmt.Sprintf("runs %s at %s %s, and actionPins in ci/actions.go pins %s %s",
			p[1], release[1], p[2], pin.Version, pin.Commit)
	}
	return ""
}
