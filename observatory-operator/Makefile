# `make test` runs every check that CI runs, with the same commands, so
# a change that passes here passes there. The coverage floor is in
# .testcoverage.yml, which CI reads too.

# A recipe that fails, or is killed, leaves its target partly written
# and newer than its inputs. make deletes that target, so the next
# build makes it again.
.DELETE_ON_ERROR:

.PHONY: test
test: test-go test-docs

# A package with no test file writes no rows to the coverage profile,
# so the gate never measures it and cannot fail it. test-go-lint lists
# such packages and fails when there is one.
UNTESTED_PACKAGES := go list -f '{{if not (or .TestGoFiles .XTestGoFiles)}}{{.ImportPath}}{{end}}' ./...

# One run of the tests does the race check and writes the coverage
# profile. go-test-coverage is a tool dependency in go.mod, so the gate
# needs nothing installed beyond the Go toolchain.
.PHONY: test-go
test-go: test-go-lint
	go test -race -covermode=atomic -coverprofile=coverage.out ./...
	go tool go-test-coverage --config=.testcoverage.yml

.PHONY: test-go-lint
test-go-lint:
	test -z "$$(gofmt -l .)" || { gofmt -l .; exit 1; }
	go mod tidy -diff
	test -z "$$($(UNTESTED_PACKAGES))" || { echo 'packages with no test file:'; $(UNTESTED_PACKAGES); exit 1; }
	go vet ./...

# Each test of the operator's package starts the operator against the
# fake API server, and -race makes each one slow. So CI splits the
# tests into SHARDS jobs, which package.toml sets. Shard SHARD runs
# every SHARDS-th name of the sorted list of test names in the module.
# A name that two packages share goes to one shard, which runs it in
# both packages, so each test runs once. Each shard writes a coverage
# profile of every package, with zero counts for the code that only
# the other shards' tests reach.
SHARD ?= 1
SHARDS ?= 1

.PHONY: test-go-shard
test-go-shard:
	names=$$(go test -race -covermode=atomic -list . ./... | grep -E '^(Test|Example|Fuzz)' | sort -u | \
		awk 'NR % $(SHARDS) == $(SHARD) % $(SHARDS)' | paste -sd '|') && \
		test -n "$$names" && \
		go test -race -covermode=atomic -coverprofile=coverage.out -run "^($$names)$$" ./...

# CI's gate job downloads each shard's profile into
# coverage-shards/<artifact>/coverage.out. This target sums the count
# of each block over the shards into coverage.out and checks the sum
# against the floor. CI's lint job runs test-go-lint beside the shards.
.PHONY: test-go-gate
test-go-gate:
	awk 'FNR == 1 { next } { key = $$1 " " $$2; if (!(key in count)) order[++n] = key; count[key] += $$3 } \
		END { print "mode: atomic"; for (i = 1; i <= n; i++) print order[i], count[order[i]] }' \
		coverage-shards/*/coverage.out >coverage.out
	go tool go-test-coverage --config=.testcoverage.yml

# `liken plugins sync` installs the plugin of a release, so to test a
# change to cli/, build the plugin from the checkout with this target.
# kubectl finds the plugin as kubectl-liken-observatory on PATH, so
# BINDIR must be a directory on PATH. The binary reports the version
# dev unless VERSION sets another, so it warns that its version differs
# from the operator's, and --force silences the warning.
BINDIR ?= $(HOME)/.local/bin
VERSION ?= dev

.PHONY: install-plugin
install-plugin:
	mkdir -p $(BINDIR)
	CGO_ENABLED=0 go build -trimpath -ldflags "-X main.version=$(VERSION)" \
		-o $(BINDIR)/kubectl-liken-observatory ./cli

# drivers/generated.go maps each driver to the image that holds it, from
# indi/images/ and the tag in indi/package.toml, and holds the tag of
# the weston image from weston/package.toml. A test fails when the file
# is stale. This target writes it again.
.PHONY: drivers
drivers:
	go test ./drivers -run TestTheMapIsCurrent -update

# deploy/firmware/qhy.yaml holds one Firmware for each bare QHY camera
# chip that a rule of firmware/85-qhyccd.rules loads, on the indi-qhy
# tag of drivers/generated.go. A test fails when the file is stale.
# This target writes it again. Run it after make drivers.
.PHONY: firmware
firmware:
	go test ./firmware -run TestTheQHYFirmwareIsCurrent -update

# firmware/85-qhyccd.rules is a copy of the rules file in the indi-qhy
# image at the tag that indi/package.toml pins. After a bump of the
# INDI images, this builds the image, copies the file again, and writes
# deploy/firmware/qhy.yaml from it. It runs Docker, so it runs on a
# workstation, never in CI.
.PHONY: firmware-rules
firmware-rules:
	cd .. && docker buildx bake indi-qhy --load
	id=$$(docker create ghcr.io/liken-sh/indi-qhy:$(INDI_TAG) /none) \
		&& docker cp $$id:/usr/lib/udev/rules.d/85-qhyccd.rules firmware/85-qhyccd.rules; \
		status=$$?; docker rm $$id >/dev/null; exit $$status
	$(MAKE) firmware

# The schema comes from schema/ and from the standard properties of
# each INDI interface in indi/standard/: the CRD in deploy/ of each kind
# with procedures, and the typed fields in observatory/generated_*.go. A
# test fails when one of them is stale. This target writes them again.
.PHONY: schema
schema:
	go test ./schema -run TestTheSchemaIsCurrent -update

# schema/disagreements.txt lists each place where the sources of the
# standard properties disagree, with the standard properties page of
# indilib/docs among them. The generator never reads the page, because
# its text is not ours to publish. This fetches the page from GitHub at
# its newest commit, or at COMMIT, and writes the report again, so it
# runs on a workstation, never in CI.
.PHONY: crosscheck
crosscheck:
	go run ./schema/crosscheck -root . $(if $(COMMIT),-commit $(COMMIT))

# The captures in indi/standard/ and the transcripts in indi/inditest/testdata/
# are what a real indiserver sent on the libindi that indi/package.toml
# pins. After a bump of the INDI images, this records both again: it
# builds the bare drivers of indi/standard/bare/ and captures each one,
# and runs each simulator of the indi-simulators image through its
# script. A test fails when a capture names another INDI version than
# the pin. It runs Docker, so it runs on a workstation, never in CI.
#
# Both images build through bake, from the trust and indi targets of
# the same commit. The bare drivers are never published, so their
# target is in a bake file of its own beside them, which ci/ does not
# write.
INDI_VERSION := $(shell sed -n 's/^version = "\(.*\)"/\1/p' ../indi/package.toml)
INDI_TAG := $(INDI_VERSION)-$(shell sed -n 's/^revision = \(.*\)/\1/p' ../indi/package.toml)

.PHONY: captures
captures: standard transcripts

.PHONY: standard
standard:
	cd .. && INDI_SNAPSHOT=$(INDI_VERSION) docker buildx bake \
		-f docker-bake.hcl -f observatory-operator/indi/standard/bare/docker-bake.hcl indi-bare --load
	go run ./indi/capture/record -image indi-bare:$(INDI_VERSION) standard indi/standard

.PHONY: transcripts
transcripts:
	cd .. && docker buildx bake indi-simulators --load
	go run ./indi/capture/record -image ghcr.io/liken-sh/indi-simulators:$(INDI_TAG) simulators indi/inditest/testdata

.PHONY: test-docs
test-docs:
# The reference pages are generated and committed. make compares file
# times to decide whether a page is current, and a fresh checkout gives
# each file the time that git wrote it, so in CI make can skip a page
# that a change to a generator in brand/ made stale. -B regenerates
# every page whatever the times say. The check fails when the result
# differs from what git has staged, or adds an untracked file.
	$(MAKE) -C docs -B generate
	git diff --quiet -- docs/content/docs/reference \
		&& test -z "$$(git ls-files --others --exclude-standard -- docs/content/docs/reference)" \
		|| { git status --short -- docs/content/docs/reference; exit 1; }
	$(MAKE) -C docs test
# skills/ is generated from the guides and committed. The check
# regenerates it and fails the same way, which means that a guide
# changed and nobody ran `make -C docs skills`. It compares with the
# index, not with the last commit, so a commit that stages a guide and
# its regenerated skill together passes.
	$(MAKE) -C docs skills
	git diff --quiet -- skills && test -z "$$(git ls-files --others --exclude-standard -- skills)" \
		|| { git status --short -- skills; exit 1; }
	$(MAKE) -C docs build

# The coverage report draws the gate's profile as one HTML page for the
# site to publish. `test` does not depend on it, so a report that fails
# to render never fails a build. coverage is a tool of the docs module,
# so the command runs from docs/.
.PHONY: coverage-report
coverage-report: coverage.out
	cd docs && go tool coverage \
		-title observatory-operator \
		-root .. \
		-out ../coverage.html \
		-label Go ../coverage.out

# The report reads the profile test-go writes, so a tree with no
# profile runs the tests first.
coverage.out:
	$(MAKE) test-go
