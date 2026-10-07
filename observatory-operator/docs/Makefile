# The docs domain builds the manual at liken.sh/observatory/. The
# reference page of each of the 20 resources is generated, never
# edited: crdref, the generator the brand component publishes, renders
# each one from its CRD in deploy/, so a schema edit is a manual edit.
# GENERATED lists those pages, and each rule names the generator's
# sources as inputs, so a change to the generator regenerates them.
HUGO := go tool hugo
CRDREF := go tool crdref

# crdref comes from brand/ through the replace directive in go.mod, so
# an edit to its sources never changes go.mod. Each generated page
# names those sources as inputs, and go.mod for the generator's own
# dependencies, so an edit to either one regenerates the page.
BRAND := ../../brand
CRDREF_SOURCES := go.mod $(BRAND)/go.mod \
	$(filter-out %_test.go,$(wildcard $(BRAND)/crdref/*.go $(BRAND)/linkcheck/*.go))

REFERENCE := content/docs/reference

# The weights put the resource pages after the hand-written reference
# pages, in the order of the tree: the site, the telescope and its
# reservation, the optics, then each device kind.
GENERATED := $(REFERENCE)/observatories.md \
	$(REFERENCE)/telescopes.md \
	$(REFERENCE)/reservations.md \
	$(REFERENCE)/opticaltubes.md \
	$(REFERENCE)/opticaltrains.md \
	$(REFERENCE)/mounts.md \
	$(REFERENCE)/cameras.md \
	$(REFERENCE)/filterwheels.md \
	$(REFERENCE)/focusers.md \
	$(REFERENCE)/rotators.md \
	$(REFERENCE)/dustcaps.md \
	$(REFERENCE)/flatpanels.md \
	$(REFERENCE)/guiders.md \
	$(REFERENCE)/domes.md \
	$(REFERENCE)/weatherstations.md \
	$(REFERENCE)/skyqualitymeters.md \
	$(REFERENCE)/switches.md \
	$(REFERENCE)/gpses.md \
	$(REFERENCE)/polaraligners.md \
	$(REFERENCE)/receivers.md

SKILLS := go tool skills
BASE := https://liken.sh/observatory/

# The generate target builds every generated reference page and
# nothing else. The test-docs target in ../Makefile runs it with -B,
# so that every page is regenerated, and fails when one differs from
# what git has staged.
.PHONY: generate
generate: $(GENERATED)

.PHONY: build
build: $(GENERATED)
	$(HUGO) --destination dist/site

# The site's own test: every internal link resolves before the build.
# manual_test.go names the links that no content file answers for.
.PHONY: test
test: $(GENERATED)
	go test ./...

$(REFERENCE)/observatories.md: ../deploy/observatories-crd.yaml $(CRDREF_SOURCES)
	$(CRDREF) -title Observatory -weight 100 $< $@

$(REFERENCE)/telescopes.md: ../deploy/telescopes-crd.yaml $(CRDREF_SOURCES)
	$(CRDREF) -title Telescope -weight 105 $< $@

$(REFERENCE)/reservations.md: ../deploy/reservations-crd.yaml $(CRDREF_SOURCES)
	$(CRDREF) -title Reservation -weight 110 $< $@

$(REFERENCE)/opticaltubes.md: ../deploy/opticaltubes-crd.yaml $(CRDREF_SOURCES)
	$(CRDREF) -title OpticalTube -weight 115 $< $@

$(REFERENCE)/opticaltrains.md: ../deploy/opticaltrains-crd.yaml $(CRDREF_SOURCES)
	$(CRDREF) -title OpticalTrain -weight 120 $< $@

$(REFERENCE)/mounts.md: ../deploy/mounts-crd.yaml $(CRDREF_SOURCES)
	$(CRDREF) -title Mount -weight 125 $< $@

$(REFERENCE)/cameras.md: ../deploy/cameras-crd.yaml $(CRDREF_SOURCES)
	$(CRDREF) -title Camera -weight 130 $< $@

$(REFERENCE)/filterwheels.md: ../deploy/filterwheels-crd.yaml $(CRDREF_SOURCES)
	$(CRDREF) -title FilterWheel -weight 135 $< $@

$(REFERENCE)/focusers.md: ../deploy/focusers-crd.yaml $(CRDREF_SOURCES)
	$(CRDREF) -title Focuser -weight 140 $< $@

$(REFERENCE)/rotators.md: ../deploy/rotators-crd.yaml $(CRDREF_SOURCES)
	$(CRDREF) -title Rotator -weight 145 $< $@

$(REFERENCE)/dustcaps.md: ../deploy/dustcaps-crd.yaml $(CRDREF_SOURCES)
	$(CRDREF) -title DustCap -weight 150 $< $@

$(REFERENCE)/flatpanels.md: ../deploy/flatpanels-crd.yaml $(CRDREF_SOURCES)
	$(CRDREF) -title FlatPanel -weight 155 $< $@

$(REFERENCE)/guiders.md: ../deploy/guiders-crd.yaml $(CRDREF_SOURCES)
	$(CRDREF) -title Guider -weight 160 $< $@

$(REFERENCE)/domes.md: ../deploy/domes-crd.yaml $(CRDREF_SOURCES)
	$(CRDREF) -title Dome -weight 165 $< $@

$(REFERENCE)/weatherstations.md: ../deploy/weatherstations-crd.yaml $(CRDREF_SOURCES)
	$(CRDREF) -title WeatherStation -weight 170 $< $@

$(REFERENCE)/skyqualitymeters.md: ../deploy/skyqualitymeters-crd.yaml $(CRDREF_SOURCES)
	$(CRDREF) -title SkyQualityMeter -weight 175 $< $@

$(REFERENCE)/switches.md: ../deploy/switches-crd.yaml $(CRDREF_SOURCES)
	$(CRDREF) -title Switch -weight 180 $< $@

$(REFERENCE)/gpses.md: ../deploy/gpses-crd.yaml $(CRDREF_SOURCES)
	$(CRDREF) -title GPS -weight 185 $< $@

$(REFERENCE)/polaraligners.md: ../deploy/polaraligners-crd.yaml $(CRDREF_SOURCES)
	$(CRDREF) -title PolarAligner -weight 190 $< $@

$(REFERENCE)/receivers.md: ../deploy/receivers-crd.yaml $(CRDREF_SOURCES)
	$(CRDREF) -title Receiver -weight 195 $< $@

# The skills target emits every guide as an Agent Skill into ../skills,
# one SKILL.md per guide, with the generator the brand component
# publishes (its README says what the output holds). The output is
# committed, and the test-docs target in ../Makefile fails when it is
# stale.
.PHONY: skills
skills:
	$(SKILLS) -base $(BASE) content/docs/guides ../skills
