# The docs domain builds the manual for bluetooth.liken.sh. The three
# CRD reference pages are generated, never edited: crdref, the
# generator the brand repository publishes, renders each page from
# its schema in deploy/crds.yaml and a hand-written preamble that
# opens it. The schema descriptions become the field tables, so a
# schema edit is a manual edit. The three CRDs travel in one manifest
# file, so each rule selects its kind. go.mod pins the generator the
# way it pins Hugo, as a tool directive, and each rule lists go.mod
# as an input, so a generator bump regenerates every page.
HUGO := go tool hugo
CRDREF := go tool crdref

REFERENCE := content/docs/reference

GENERATED := \
	$(REFERENCE)/adapters.md \
	$(REFERENCE)/pairings.md \
	$(REFERENCE)/pairingrequests.md

.PHONY: build
build: $(GENERATED)
	$(HUGO) --destination dist/site

# The site's own tests: every internal link resolves before the build
# (manual_test.go passes this site's exceptions).
.PHONY: test
test:
	go test ./...

$(REFERENCE)/adapters.md: ../deploy/crds.yaml crdref/adapter-preamble.md go.mod
	$(CRDREF) -kind Adapter -weight 10 $< $@ crdref/adapter-preamble.md

$(REFERENCE)/pairings.md: ../deploy/crds.yaml crdref/pairing-preamble.md go.mod
	$(CRDREF) -kind Pairing -weight 15 $< $@ crdref/pairing-preamble.md

$(REFERENCE)/pairingrequests.md: ../deploy/crds.yaml crdref/pairingrequest-preamble.md go.mod
	$(CRDREF) -kind PairingRequest -weight 20 $< $@ crdref/pairingrequest-preamble.md
