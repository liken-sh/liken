# `make test` runs every check that CI runs, with the same commands, so
# a change that passes here passes in CI. The Go module and the Rust
# workspace are in this directory, so their checks are here. The docs
# have their own Makefile, which `test-docs` calls.
#
# Each coverage gate has one floor, stored in one place that CI also
# reads. The Go floor is in .testcoverage.yml, and the floors of the
# two Rust crates are below.

# A recipe that fails or is killed can leave a partial target that is
# newer than its inputs. make deletes it, so the next run builds it
# again.
.DELETE_ON_ERROR:

.PHONY: test
test: test-go test-rust test-docs

# keycodes.go is generated from this kernel header and is committed, so
# you need the header only to regenerate the table against a newer
# kernel.
INPUT_EVENT_CODES ?= /usr/include/linux/input-event-codes.h

.PHONY: codes
codes:
	go run keycodegen.go $(INPUT_EVENT_CODES) keycodes.go
	gofmt -w keycodes.go

# The test patterns that a pattern:// URI plays are committed in
# patterns/, one file for each pattern and frame size, and the player
# image copies them, so neither the image build nor CI runs an encoder.
# The names and frame sizes must match patterns.go. `make patterns` writes
# the files that are missing, and `make -B -j2 patterns` writes all of
# them again after an edit here. Each encode uses a lot of memory, so
# keep -j low.
#
# Each file is ten minutes of one still frame at one frame per second.
# mpv reads the position and the duration from the timestamps, so the
# scrubber advances as it does for a film. A chapter every minute gives
# the scrubber its marks, and the title is the name that the header
# shows.
#
# Every minute is the same, so the recipe encodes one minute and
# repeats it ten times with a stream copy. The copy needs no encode,
# and each repeat starts on a keyframe, so a seek decodes at most one
# minute of frames. The encode uses one thread and no lookahead,
# because x264's defaults use several gigabytes of memory for one
# 3840x2160 encode.
PATTERN_NAMES := white bars
PATTERN_FRAMES := 1280x720 1920x1080 3840x2160 2560x1080 3840x1600 1920x804
PATTERN_FILES := $(foreach name,$(PATTERN_NAMES),$(foreach frame,$(PATTERN_FRAMES),patterns/$(name)-$(frame).mkv)) \
	patterns/speakers-1280x720.mkv

PATTERN_SOURCE_white := color=c=white:r=1
PATTERN_TITLE_white := White
PATTERN_SOURCE_bars := smptehdbars=r=1
PATTERN_TITLE_bars := Color bars

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

# The speaker walk plays pink noise from one speaker of a 7.1 layout at
# a time, 5 seconds each, in order around the room from the front left
# and ending with the subwoofer. The screen and a chapter show the name
# of the current speaker. Pink noise is what a receiver's own speaker
# setup plays. The subwoofer's noise is filtered to below 120 Hz. The
# picture is 1280x720 because the file is meant for listening.
#
# The audio is AAC at 80 kb/s with the cutoff set to 20 kHz. With the
# default cutoff, the encoder divides the bit rate among all eight
# channels even though seven are silent at any moment, and it cuts
# every band above 8 kHz, so a tweeter would play nothing. 80 kb/s
# keeps the file under the 500 KB that the large-file check allows.
# Each SPEAKERS entry is the ffmpeg channel name, a colon, and the name
# shown on the screen.
SPEAKERS := FL:Front_left FC:Center FR:Front_right SR:Side_right \
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

# One run of the tests does both the race check and the coverage
# profile, because -race works with -covermode=atomic.
#
# go-test-coverage is a tool dependency pinned by the `tool` directive
# in go.mod, so the gate needs nothing installed beyond the Go
# toolchain.

# A package with no test file writes no rows to the coverage profile,
# so the gate ignores it and it cannot lower the total. test-go fails
# when this command lists any package.
UNTESTED_PACKAGES := go list -f '{{if not (or .TestGoFiles .XTestGoFiles)}}{{.ImportPath}}{{end}}' ./...

# The sidecar containers, the player shim, and the api run the pod
# build, the program built with the build tag pod. leader_pod.go gives
# the reason for the tag. The api's watches need client-go's reflector,
# dynamic client, and rest packages. This check fails when the pod build
# also links the typed clientset, the informer factories, or leader
# election, because they make the binary much larger in every playback
# pod and every Remote's pod.
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

# The Rust half is a cargo workspace with two members: the idle screen
# and the display that a playback pod draws over the film. Both link the
# media-screen library at the top of the repository.
#
# The Rust checks are the format, the lints, and the tests under a
# coverage gate. The idle screen's integration test runs its binary
# under cage on the headless backend, so it also covers the frame loop
# and the graphics setup. That test needs cage, wlr-randr, and a Vulkan
# device, and Mesa's lavapipe is enough. The display has no test that
# runs it under a compositor, so its window code is not covered and its
# floor is lower. media-screen's gate runs in its own directory.

# The floor on line coverage, as a percentage, for each crate. CI
# enforces the same numbers through this file. Raise a floor when
# coverage rises, and do not lower it.
IDLE_COVERAGE_FLOOR := 95
DISPLAY_COVERAGE_FLOOR := 90

.PHONY: test-rust
test-rust:
	cargo fmt --check
	cargo clippy --workspace --all-targets -- -D warnings
# The image builds with the `measure` feature off, so these lines lint
# and test that build too. Without them, a test that names a flag of
# the feature compiles only in the default build, and the shipped build
# goes untested. The headless test requires the feature, so cargo skips
# it here.
	cargo clippy -p idle-screen --no-default-features --all-targets -- -D warnings
	cargo test -p idle-screen --no-default-features
# One instrumented build runs the tests of both crates, and a report
# for each crate applies its floor. The build uses the whole
# workspace's features, the same set that the first clippy run lints,
# so each dependency compiles once. A separate run for each crate would
# resolve a different feature set and compile the iced crates twice. A
# report with `-p` counts only that crate's files, and neither crate
# links the other, so each crate's coverage comes from its own tests.
#
# `clean` runs first, so no object or profile of an earlier run counts.
# The Cobertura files are the inputs of `make coverage-report`.
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
# The reference pages are generated and committed. A fresh checkout
# gives every file the time at which git wrote it, so in CI make cannot
# tell from file times whether a page is stale, and a change to a
# generator in brand/ would leave a stale page that the site publishes.
# So this check regenerates every page with -B, and fails when the
# result differs from what git has staged or adds an untracked file.
	$(MAKE) -C docs -B generate
	git diff --quiet -- docs/content/docs/reference \
		&& test -z "$$(git ls-files --others --exclude-standard -- docs/content/docs/reference)" \
		|| { git status --short -- docs/content/docs/reference; exit 1; }
	$(MAKE) -C docs test
# skills/ is generated from the guides and committed. This check
# regenerates it and fails when the result differs from what git has
# staged or adds an untracked file, which means that a guide changed
# and nobody ran `make -C docs skills`. The comparison is with the
# index, not with the last commit, so a commit that stages a guide and
# its regenerated skill together passes.
	$(MAKE) -C docs skills
	git diff --quiet -- skills && test -z "$$(git ls-files --others --exclude-standard -- skills)" \
		|| { git status --short -- skills; exit 1; }
	$(MAKE) -C docs build

# The report puts the coverage data on one page, which the site
# publishes at /coverage.html. It reads what the gates already wrote:
# the Go profile from test-go, and one Cobertura file for each crate
# from test-rust. Run `make test` first, or the inputs are stale or
# missing.
#
# `test` does not depend on this target, because the gate decides pass
# or fail and the report shows which lines are uncovered.
#
# The tool belongs to the brand component and is pinned as a tool
# dependency of the docs module, so it runs from docs/ and needs
# nothing installed. -root is this directory, the source tree that the
# inputs describe.
COVERAGE_INPUTS := coverage.out coverage-idle-screen.xml coverage-display.xml

.PHONY: coverage-report
coverage-report:
	cd docs && go tool coverage -title media-operator -root .. \
		-label Go -label "Rust (idle-screen)" \
		-label "Rust (media-display)" \
		-out ../coverage.html \
		$(addprefix ../,$(COVERAGE_INPUTS))
