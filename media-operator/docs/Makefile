# The docs domain builds the manual for media.liken.sh. Five of the
# reference pages are generated, never edited: crdref, the generator
# the liken repository publishes, renders each page from its CRD
# schema in deploy/, a hand-written preamble that opens the page, and
# a hand-written postamble that carries its bus sections. The schema
# descriptions become the field tables, so a schema edit is a manual
# edit. go.mod pins the generator the way it pins Hugo, as a tool
# directive, and each rule lists go.mod as an input, so a generator
# bump regenerates every page.
HUGO := go tool hugo
CRDREF := go tool crdref

REFERENCE := content/docs/reference

GENERATED := \
	$(REFERENCE)/players.md \
	$(REFERENCE)/plays.md \
	$(REFERENCE)/remotes.md \
	$(REFERENCE)/keymaps.md \
	$(REFERENCE)/mediapreferences.md

.PHONY: build
build: $(GENERATED)
	$(HUGO) --destination dist/site

$(REFERENCE)/players.md: ../deploy/players-crd.yaml crdref/players-preamble.md crdref/players-postamble.md go.mod
	$(CRDREF) -title Players -weight 10 -postamble crdref/players-postamble.md $< $@ crdref/players-preamble.md

$(REFERENCE)/plays.md: ../deploy/plays-crd.yaml crdref/plays-preamble.md crdref/plays-postamble.md go.mod
	$(CRDREF) -title Plays -weight 20 -postamble crdref/plays-postamble.md $< $@ crdref/plays-preamble.md

$(REFERENCE)/remotes.md: ../deploy/remotes-crd.yaml crdref/remotes-preamble.md crdref/remotes-postamble.md go.mod
	$(CRDREF) -title Remotes -weight 30 -postamble crdref/remotes-postamble.md $< $@ crdref/remotes-preamble.md

$(REFERENCE)/keymaps.md: ../deploy/keymaps-crd.yaml crdref/keymaps-preamble.md crdref/keymaps-postamble.md go.mod
	$(CRDREF) -title Keymaps -weight 40 -postamble crdref/keymaps-postamble.md $< $@ crdref/keymaps-preamble.md

$(REFERENCE)/mediapreferences.md: ../deploy/mediapreferences-crd.yaml crdref/mediapreferences-preamble.md crdref/mediapreferences-postamble.md go.mod
	$(CRDREF) -title MediaPreferences -weight 50 -postamble crdref/mediapreferences-postamble.md $< $@ crdref/mediapreferences-preamble.md
