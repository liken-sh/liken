# This Makefile names the checks a change must pass, and delegates each
# one to the domain that owns it. `make test` runs every check CI runs,
# in the same commands, so a change that passes here passes there. The
# Go module and the Rust workspace are both in this directory, so their
# checks are here; the docs are their own domain with their own
# Makefile.
#
# The coverage floors are the one number each gate enforces: the Go
# floor is in .testcoverage.yml, and the three Rust floors are below.
# CI reads the same files, so a floor moves in one place.

.PHONY: test
test: test-go test-rust test-docs

# keycodes.go is generated from the kernel header named here, and it is
# committed, so a build needs the header only when the table is
# regenerated against a newer kernel.
INPUT_EVENT_CODES ?= /usr/include/linux/input-event-codes.h

.PHONY: codes
codes:
	go run keycodegen.go $(INPUT_EVENT_CODES) keycodes.go
	gofmt -w keycodes.go

# The test patterns that a pattern:// URI plays are committed in
# patterns/, one file for each pattern and frame, and the player image
# copies them, so neither the image build nor CI runs an encoder. The
# names and the frames match patterns.go. `make patterns` writes a file
# that is missing, and `make -B -j2 patterns` writes them all again
# after an edit here. Each encode holds up to 400 MB, so -j2 is a safe
# speedup and a high -j is not.
#
# Each file is ten minutes of one still frame at one frame a second.
# mpv reads the position and the duration from the timestamps, so the
# scrubber advances as it does for a film. A chapter every minute gives
# the scrubber its marks, and the title gives the header a name.
#
# The recipe encodes one minute and copies it ten times over, because
# every minute is the same. The copy costs no encode, and each copy
# starts on a keyframe, so a seek decodes at most one minute of frames.
# The encode runs on one thread with no lookahead, because x264's
# defaults held 4 GB of memory for one 3840x2160 encode, and one thread
# holds under 400 MB.
PATTERN_NAMES := white bars
PATTERN_FRAMES := 1280x720 1920x1080 3840x2160 2560x1080 3840x1600 1920x804
PATTERN_FILES := $(foreach name,$(PATTERN_NAMES),$(foreach frame,$(PATTERN_FRAMES),patterns/$(name)-$(frame).mkv)) \
	patterns/speakers-1280x720.mkv

PATTERN_SOURCE_white := color=c=white:r=1
PATTERN_TITLE_white := White
PATTERN_SOURCE_bars := smptehdbars=r=1
PATTERN_TITLE_bars := Colour bars

.PHONY: patterns
patterns: $(PATTERN_FILES)

patterns/%.mkv: name = $(word 1,$(subst -, ,$*))
patterns/%.mkv: frame = $(word 2,$(subst -, ,$*))
patterns/%.mkv:
	work=$$(mktemp -d) && trap 'rm -rf "$$work"' EXIT && \
	{ echo ';FFMETADATA1'; for minute in 0 1 2 3 4 5 6 7 8 9; do \
	  printf '[CHAPTER]\nTIMEBASE=1/1\nSTART=%d\nEND=%d\ntitle=Minute %d\n' \
	    $$((minute * 60)) $$((minute * 60 + 60)) $$((minute + 1)); \
	done; } >"$$work/chapters" && \
	ffmpeg -nostdin -loglevel error -y \
	  -f lavfi -i '$(PATTERN_SOURCE_$(name)):s=$(frame)' -t 60 \
	  -c:v libx264 -preset veryfast -tune stillimage -g 60 -bf 0 \
	  -threads 1 -rc-lookahead 0 -pix_fmt yuv420p \
	  -color_range tv -colorspace bt709 -color_primaries bt709 -color_trc bt709 \
	  "$$work/minute.mkv" && \
	ffmpeg -nostdin -loglevel error -y \
	  -stream_loop 9 -i "$$work/minute.mkv" -i "$$work/chapters" \
	  -map 0:v -map_chapters 1 -c copy \
	  -metadata title='$(PATTERN_TITLE_$(name)), $(subst x,×,$(frame))' \
	  $@

# The speaker walk plays pink noise from one speaker of a 7.1 layout at a
# time, 5 seconds each, around the room from the front left and then the
# subwoofer, and names each speaker on the screen and in a chapter. Pink
# noise is what a receiver's own speaker setup plays, and the subwoofer's
# noise stops at 120 Hz. The picture is 1280x720, because the file is
# for the ears. The audio is AAC at 80 kb/s with the cutoff forced to
# 20 kHz: with the default, the encoder spreads the rate over all eight
# channels, although seven are silent at a time, and cuts every band
# above 8 kHz, so a tweeter would play nothing. 80 kb/s keeps the file
# under the 500 KB that the large-file check allows. Each speaker is a
# channel, the ffmpeg name of its channel, and its name on the screen.
SPEAKERS := FL:Front_left FC:Centre FR:Front_right SR:Side_right \
	BR:Back_right BL:Back_left SL:Side_left LFE:Subwoofer
SPEAKER_FONT := ../brand/fonts/SourceSans3-Regular.otf

patterns/speakers-1280x720.mkv: $(SPEAKER_FONT)
	work=$$(mktemp -d) && trap 'rm -rf "$$work"' EXIT && \
	audio="anoisesrc=color=pink:amplitude=0.25:sample_rate=48000:duration=40:seed=1,asplit=8" && \
	video="color=c=black:s=1280x720:r=1:d=40" && \
	echo ';FFMETADATA1' >"$$work/chapters" && \
	index=0 && \
	for speaker in $(SPEAKERS); do \
	  channel=$${speaker%%:*}; label=$$(echo "$${speaker#*:}" | tr _ ' '); \
	  start=$$((index * 5)); end=$$((start + 5)); \
	  audio="$$audio[n$$index]"; \
	  shape=; [ "$$channel" = LFE ] && shape="lowpass=f=120,lowpass=f=120,"; \
	  gates="$$gates;[n$$index]$${shape}volume=0:enable='not(between(t,$$start,$$end))'[$$channel]"; \
	  video="$$video,drawtext=fontfile=$(SPEAKER_FONT):text='$$label':fontsize=96:fontcolor=white:x=(w-text_w)/2:y=(h-text_h)/2-40:enable='between(t,$$start,$$end)'"; \
	  video="$$video,drawtext=fontfile=$(SPEAKER_FONT):text='$$channel · $$((index + 1)) of 8':fontsize=40:fontcolor=0x9a9a9a:x=(w-text_w)/2:y=(h/2)+50:enable='between(t,$$start,$$end)'"; \
	  printf '[CHAPTER]\nTIMEBASE=1/1\nSTART=%d\nEND=%d\ntitle=%s\n' "$$start" "$$end" "$$label" >>"$$work/chapters"; \
	  index=$$((index + 1)); \
	done && \
	ffmpeg -nostdin -loglevel error -y \
	  -filter_complex "$$audio$$gates;[FL][FR][FC][LFE][BL][BR][SL][SR]join=inputs=8:channel_layout=7.1:map=0.0-FL|1.0-FR|2.0-FC|3.0-LFE|4.0-BL|5.0-BR|6.0-SL|7.0-SR[audio];$$video[video]" \
	  -i "$$work/chapters" -map '[video]' -map '[audio]' -map_chapters 0 -t 40 \
	  -c:v libx264 -preset veryfast -tune stillimage -g 5 -bf 0 \
	  -threads 1 -rc-lookahead 0 -pix_fmt yuv420p \
	  -color_range tv -colorspace bt709 -color_primaries bt709 -color_trc bt709 \
	  -c:a aac -b:a 80k -cutoff 20000 \
	  -metadata title='Speaker walk, 7.1' \
	  $@

# One run of the tests gives both the race check and the coverage
# profile, because -race with -covermode=atomic does both.
#
# go-test-coverage is a pinned tool dependency (the `tool` directive
# in go.mod), so the gate needs nothing installed beyond the Go
# toolchain.

# A package with no test file writes no rows to the profile, so the
# gate never counts it: its number is not low, it is missing. This
# lists such packages, and test-go fails on the first one.
UNTESTED_PACKAGES := go list -f '{{if not (or .TestGoFiles .XTestGoFiles)}}{{.ImportPath}}{{end}}' ./...

# The pod build is the program the sidecar containers, the player shim,
# and the api run, built with the tag pod (leader_pod.go says why). The
# api's watches link client-go's reflector, its dynamic client, and rest,
# with the packages they need. The check vets the build and fails when
# it links the typed clientset, the informer factories, or leader
# election, which would more than double the program that every
# playback pod and every Remote's pod runs.
POD_BUILD_CLIENT_GO := go list -tags pod -deps . | grep -E '^k8s.io/client-go/(kubernetes|informers|dynamic/dynamicinformer|tools/leaderelection)(/|$$)'

.PHONY: test-go
test-go:
	test -z "$$(gofmt -l .)" || { gofmt -l .; exit 1; }
	go mod tidy -diff
	test -z "$$($(UNTESTED_PACKAGES))" || { echo 'packages with no test file:'; $(UNTESTED_PACKAGES); exit 1; }
	go vet ./...
	go vet -tags pod .
	test -z "$$($(POD_BUILD_CLIENT_GO))" || { echo 'the pod build links more of client-go than its watches need:'; $(POD_BUILD_CLIENT_GO); exit 1; }
	go test -race -covermode=atomic -coverprofile=coverage.out ./...
	go tool go-test-coverage --config=.testcoverage.yml

# The Rust half is a cargo workspace with two members: the idle screen,
# which draws with the media-screen library at the top of the
# repository, and the display a playback pod draws over the film.
#
# The Rust checks: the format, the lints, and the tests under a coverage
# gate. cargo-llvm-cov runs the tests itself and measures every line they
# reach, and `cargo llvm-cov report` applies each floor to that one
# measurement. The unit tests cover the parsers, the rules, the
# measurements, and the timeline; the integration test runs the binary
# under cage on the headless backend, so the frame loop and the graphics
# setup count too. That test needs cage, wlr-randr, and a Vulkan device,
# and Mesa's lavapipe is enough of one.
#
# The two crates share a lock file and a target tree. Each crate keeps a
# floor of its own, because they earn different numbers: the idle screen
# carries a window, and the display's own window half needs a compositor
# no unit test has. media-screen's own gate runs in its own directory.

# The floor on line coverage, as a percentage, one per crate. CI enforces the
# same numbers through this file. Raise one when the tests earn it; never
# lower it.
IDLE_COVERAGE_FLOOR := 95
DISPLAY_COVERAGE_FLOOR := 90

.PHONY: test-rust
test-rust:
	cargo fmt --check
	cargo clippy --workspace --all-targets -- -D warnings
# The image builds with `measure` off, so the lints run over that build
# too. The pass is the library and the binary alone, because the test
# targets reach the flags and the measurements the feature carries.
	cargo clippy -p idle-screen --no-default-features --lib --bins -- -D warnings
# One instrumented build of the workspace runs the tests of both
# crates, and one report per crate applies that crate's floor. The
# build uses the features of the whole workspace, the same set the
# first clippy pass lints, so each dependency compiles once. A separate
# run for each crate resolves a different feature set for each one,
# and so compiles the iced crates twice.
#
# A report with `-p` counts the lines of that crate's files alone.
# Neither crate links the other, so a line of the idle screen runs
# only under the idle screen's tests, and a line of the display only
# under the display's. So each crate's number comes from its own tests
# alone.
#
# The run starts from no objects and no profiles, so no test binary of
# an earlier run counts. The Cobertura files at the workspace root are
# inputs to `make coverage-report`, which draws the page the site
# publishes.
	cargo llvm-cov clean --workspace
	cargo llvm-cov --workspace --all-targets --no-report
	cargo llvm-cov report -p idle-screen \
		--fail-under-lines $(IDLE_COVERAGE_FLOOR)
	cargo llvm-cov report -p idle-screen \
		--cobertura --output-path coverage-idle-screen.xml
	cargo llvm-cov report -p media-display \
		--fail-under-lines $(DISPLAY_COVERAGE_FLOOR)
	cargo llvm-cov report -p media-display \
		--cobertura --output-path coverage-display.xml

.PHONY: test-docs
test-docs:
# The reference pages are generated and committed, so a checkout
# carries them. make decides that a page is current by comparing file
# times, and a fresh checkout gives each file the time that git wrote
# it. So in CI, make cannot be trusted to rebuild a page, and a change
# to a generator in brand/ can leave a stale page that the site
# publishes. The check runs the generate target with -B, which
# regenerates every page whatever the times say. It fails when the
# result differs from what git has staged, or adds an untracked file,
# as the skills check below does.
	$(MAKE) -C docs -B generate
	git diff --quiet -- docs/content/docs/reference \
		&& test -z "$$(git ls-files --others --exclude-standard -- docs/content/docs/reference)" \
		|| { git status --short -- docs/content/docs/reference; exit 1; }
	$(MAKE) -C docs test
# skills/ is generated from the guides and committed, so a checkout
# carries it. The check regenerates it and fails when the result differs
# from what git has staged, or adds an untracked file, which means a
# guide changed and nobody ran `make -C docs skills`. The comparison is
# with the index, not with the last commit, so a commit that stages a
# guide and its regenerated skill together passes.
	$(MAKE) -C docs skills
	git diff --quiet -- skills && test -z "$$(git ls-files --others --exclude-standard -- skills)" \
		|| { git status --short -- skills; exit 1; }
	$(MAKE) -C docs build

# The report is the coverage data as one page the site publishes at
# /coverage.html. It reads what the gates already wrote: the Go
# profile from test-go, and one Cobertura file per crate from
# test-rust. Run `make test` first, or the inputs are stale or
# missing.
#
# `test` does not depend on this, because a gate and a report are
# separate acts: the gate says pass or fail, and the report says
# where the lines are.
#
# The tool is the brand component's, pinned as a tool dependency of
# the docs module the way Hugo and crdref are, so it runs from docs/
# and needs nothing installed. -root names the tree the inputs
# describe, which is this directory.
COVERAGE_INPUTS := coverage.out coverage-idle-screen.xml coverage-display.xml

.PHONY: coverage-report
coverage-report:
	cd docs && go tool coverage -title media-operator -root .. \
		-label Go -label "Rust (idle-screen)" \
		-label "Rust (media-display)" \
		-out ../coverage.html \
		$(addprefix ../,$(COVERAGE_INPUTS))
