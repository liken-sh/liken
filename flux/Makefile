# The flux domain: the GitOps feature's ground and its engine seed.
#
# manifests/ is the feature's liken-owned ground (the namespace, the
# minting Role, the installer grant), seeded by init while the
# Cluster document declares the feature. fetch.sh renders the engine
# seed, gotk-components.yaml, which the cluster operator embeds and
# plants once (cluster-operator/flux.go); the git repository owns the
# engine after that. plans/14-gitops-from-first-boot.md tells the
# whole story.
override VERSION := $(strip $(file <VERSION))

dist/$(VERSION)/gotk-components.yaml: VERSION fetch.sh
	./fetch.sh

.PHONY: all clean

all: dist/$(VERSION)/gotk-components.yaml

clean:
	rm -rf dist
