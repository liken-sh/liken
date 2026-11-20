# This Makefile builds the manual at liken.sh/observatory/. crdref, the
# generator in brand/, writes the reference page of each resource from
# its CRD in deploy/. GENERATED lists those pages. Do not edit them by
# hand.
HUGO := go tool hugo
CRDREF := go tool crdref

# A recipe that fails, or is killed, leaves its target partly written
# and newer than its inputs, so the next make would treat it as current.
# This setting makes make delete such a target.
.DELETE_ON_ERROR:

# crdref comes from brand/ through a replace directive, so an edit to
# its sources does not change go.mod. Each generated page depends on
# those sources and on both go.mod files, so a change to the generator
# regenerates the pages.
BRAND := ../../brand
CRDREF_SOURCES := go.mod $(BRAND)/go.mod \
	$(filter-out %_test.go,$(wildcard $(BRAND)/crdref/*.go $(BRAND)/linkcheck/*.go))

REFERENCE := content/docs/reference

# The weights place the resource pages after the hand-written reference
# pages. Their order follows the resource hierarchy: the observatory,
# the telescope with its reservation and horizon, the optics, then each
# device kind.
GENERATED := $(REFERENCE)/observatories.md \
	$(REFERENCE)/telescopes.md \
	$(REFERENCE)/reservations.md \
	$(REFERENCE)/horizons.md \
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
	$(REFERENCE)/switches.md \
	$(REFERENCE)/gpses.md \
	$(REFERENCE)/polaraligners.md \
	$(REFERENCE)/receivers.md

SKILLS := go tool skills
BASE := https://liken.sh/observatory/

# The generate target builds the generated reference pages and nothing
# else. The test-docs target in ../Makefile runs it with -B to
# regenerate every page, and fails when a page differs from what git has
# staged.
.PHONY: generate
generate: $(GENERATED)

.PHONY: build
build: $(GENERATED)
	$(HUGO) --destination dist/site

# The site's own test checks that every internal link resolves.
# manual_test.go lists the links that no content file provides.
.PHONY: test
test: $(GENERATED)
	go test ./...

$(REFERENCE)/observatories.md: ../deploy/observatories-crd.yaml $(CRDREF_SOURCES)
	$(CRDREF) -title Observatory -weight 100 $< $@

$(REFERENCE)/telescopes.md: ../deploy/telescopes-crd.yaml $(CRDREF_SOURCES)
	$(CRDREF) -title Telescope -weight 105 $< $@

$(REFERENCE)/reservations.md: ../deploy/reservations-crd.yaml $(CRDREF_SOURCES)
	$(CRDREF) -title Reservation -weight 110 $< $@

$(REFERENCE)/horizons.md: ../deploy/horizons-crd.yaml $(CRDREF_SOURCES)
	$(CRDREF) -title Horizon -weight 112 $< $@

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

$(REFERENCE)/switches.md: ../deploy/switches-crd.yaml $(CRDREF_SOURCES)
	$(CRDREF) -title Switch -weight 180 $< $@

$(REFERENCE)/gpses.md: ../deploy/gpses-crd.yaml $(CRDREF_SOURCES)
	$(CRDREF) -title GPS -weight 185 $< $@

$(REFERENCE)/polaraligners.md: ../deploy/polaraligners-crd.yaml $(CRDREF_SOURCES)
	$(CRDREF) -title PolarAligner -weight 190 $< $@

$(REFERENCE)/receivers.md: ../deploy/receivers-crd.yaml $(CRDREF_SOURCES)
	$(CRDREF) -title Receiver -weight 195 $< $@

# The skills target writes each guide as an Agent Skill into ../skills,
# one SKILL.md per guide, using the generator from the brand component
# (brand/skills/main.go describes the output). The output is
# committed, and the test-docs target in ../Makefile fails when it is
# stale.
.PHONY: skills
skills:
	$(SKILLS) -base $(BASE) content/docs/guides ../skills
