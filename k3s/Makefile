# The k3s domain. liken vendors k3s instead of building Kubernetes:
# one pinned version, one static binary, fetched and verified by
# fetch.sh (documented there). The domain also owns the check that
# every manifest liken applies fits the API this k3s serves; see
# `test` below.
# override keeps this pin authoritative even under a release build.
# `make release VERSION=<stamp>` passes VERSION on the command line,
# and command-line variables silently replace plain assignments in
# every sub-make they reach. This domain's version always comes from
# the VERSION file beside this Makefile, never from a caller.
override VERSION := $(strip $(file <VERSION))

dist/$(VERSION)/k3s: VERSION fetch.sh
	./fetch.sh

.PHONY: all test clean

all: dist/$(VERSION)/k3s

clean:
	rm -rf dist

# Every manifest liken applies, checked against the Kubernetes API,
# offline. kubectl-validate carries the API schemas of each release it
# embeds, reads the CRDs among the manifests for liken's own kinds,
# and compiles every CEL rule a CRD declares, the way the API server
# does on apply. It is a tool directive in go.mod beside this file, a
# nested module, so its dependency graph stays out of the root go.sum,
# the same way docs/ pins Hugo.
#
# kubectl-validate asks the cluster the current kubeconfig names for
# its schemas before it falls back to the embedded ones, with no flag
# to turn that off. KUBECONFIG=/dev/null keeps the check offline and
# the same on every machine, whatever context a workstation holds.
#
# The version is the newest release the pinned kubectl-validate
# embeds, one minor behind the k3s VERSION names. A release it does
# not embed is fetched from the GitHub API on every run, which fails
# offline and hits the rate limit under pre-commit. Move this to the
# release VERSION names when a newer kubectl-validate embeds it.
#
# Each domain keeps its manifests in a manifests/ directory, so the
# glob crosses domains. The copies under image/dist are the build's
# output, not source, and are not here.
KUBERNETES_VERSION := 1.35
MANIFESTS := $(wildcard ../*/manifests/*.yaml)
# --local-crds takes one comma-separated list of directories, and
# every manifests/ directory is on it, so a CRD in a new domain is
# read without a change here. The trailing slash $(dir) leaves comes
# off: kubectl-validate reports every file under a directory named
# with one a second time.
comma := ,
empty :=
space := $(empty) $(empty)
CRD_DIRECTORIES := $(subst $(space),$(comma),$(patsubst %/,%,$(sort $(dir $(MANIFESTS)))))

test:
	KUBECONFIG=/dev/null go tool kubectl-validate --version $(KUBERNETES_VERSION) --local-crds $(CRD_DIRECTORIES) $(MANIFESTS)
