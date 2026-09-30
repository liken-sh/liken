# The targets that cover more than one component. Each component's own
# Makefile builds and tests that component; this file only runs them
# together.
#
#   make workflows  write .github/workflows/ and docker-bake.hcl from
#                   every package.toml
#   make images     build every image into the local Docker daemon
#   make docs       build every manual into dist/docs/, for a preview
#   make preview    build them, then serve dist/docs/ at localhost
#   make site       build the site that liken.sh serves, into dist/docs/

# The generator reads each component's package.toml and writes the
# root workflow, one workflow for each component, and the bake file
# that builds every image. CI fails when the committed files differ
# from what it writes.
.PHONY: workflows
workflows:
	cd ci && go run . generate -root ..

# Every image of the repository, through the bake file, into the local
# Docker daemon, each tagged the way the bake file names it. The Docker
# daemon loads an image of one platform only, so the build makes each
# image for this machine's platform, including the CLI images that CI
# makes for two. Each smoke check under a component's smoke/ then runs
# on the loaded image.
PLATFORM ?= linux/$(shell docker version --format '{{.Server.Arch}}')

.PHONY: images
images:
	docker buildx bake --set '*.platform=$(PLATFORM)' --load

# The project's manuals, in the layout of the one site: liken's manual
# at the root, and each other component's manual under the prefix its
# package.toml gives it, such as /display/.
PORT ?= 8080
SITE_URL ?= http://localhost:$(PORT)
DOCS := $(CURDIR)/dist/docs

# Each entry is a component's directory and its prefix in the site.
SITES = $(shell cd ci && go run . sites -root ..)

# Each manual builds twice. The component's own `make -C docs build`
# generates the reference pages, runs its own checks, and writes the
# site for its address on liken.sh. Then Hugo builds the manual again
# under SITE_URL, so every page links to the others at the address the
# preview serves. A link that names https://liken.sh/ in full leaves
# the preview.
.PHONY: docs
docs:
	rm -rf $(DOCS)
	$(MAKE) -C liken/docs build
	cd liken/docs && go tool hugo build --quiet \
		--baseURL $(SITE_URL)/ --destination $(DOCS)
	@set -e; for site in $(SITES); do \
		dir=$${site%%:*}; prefix=$${site##*:}; \
		echo "building $$dir into /$$prefix/"; \
		$(MAKE) -C $$dir/docs build; \
		(cd $$dir/docs && go tool hugo build --quiet \
			--baseURL $(SITE_URL)/$$prefix/ --destination $(DOCS)/$$prefix); \
	done

# The server is brand's preview program: a file server that answers
# the way GitHub Pages does, with text/markdown for each page's
# Markdown twin and the site's 404.html for a missing page.
.PHONY: preview
preview: docs
	cd brand && go run ./preview -dir $(DOCS) -addr localhost:$(PORT)

# The site that liken.sh serves. Each manual's own build already names
# its address on liken.sh, so the trees copy into place as they are.
# liken's build writes release.txt, the commit that CI checks the
# served site against.
#
# Each manual also serves its component's test coverage report at
# coverage.html, and a component with a report and no manual serves it
# at coverage/<name>.html. A report needs the profile of each of its
# coverage jobs. A job that ran in this CI run left its profile on
# disk. For a job that did not run, `ci reports` takes the profile
# that the site serves now, from the run where the job last ran. It
# publishes every profile under coverage/<component>/ for the next
# deploy, and it lists the reports whose profiles are all at hand. A
# report with a profile missing stays out of the site, because it
# would show a lower coverage with no reason. ci/coverage.go gives the
# reason that a served profile is still correct.
#
# Each manual is a target of its own, so `make -j site` builds the
# manuals together, and CI runs it that way. Two manuals never write to
# the same path: each build writes its generated pages and its
# dist/site/ inside its own component, and reads the brand theme
# without writing to it. The copies go to separate directories under
# dist/docs/, and liken's tree at the root holds no directory that a
# prefix names. The coverage reports come after every manual, because
# `ci reports` writes into the finished tree. The recipe passes SITES
# to the second make, so `ci sites` runs once.
.PHONY: site
site:
	rm -rf $(DOCS)
	$(MAKE) SITES='$(SITES)' site-manual-liken \
		$(foreach site,$(SITES),site-manual-$(firstword $(subst :, ,$(site))))
	@set -e; reports="$$(cd ci && go run . reports -root .. \
		-site $(SITE_URL) -dest $(DOCS))"; \
	for report in $$reports; do \
		dir=$${report%%:*}; out=$(DOCS)/$${report#*:}; \
		echo "rendering $$dir's coverage report into $${report#*:}"; \
		$(MAKE) -C $$dir coverage-report; \
		mkdir -p $$(dirname $$out); \
		cp $$dir/coverage.html $$out; \
	done

# liken's manual is the root of the site.
site-manual-liken:
	$(MAKE) -C liken/docs build
	mkdir -p $(DOCS)
	cp -R liken/docs/dist/site/. $(DOCS)/

# Every other manual copies into the prefix that SITES gives its
# directory. These targets are not phony, because make does not apply
# a pattern rule to a phony target, and no file has their names.
site-manual-%: prefix = $(lastword $(subst :, ,$(filter $*:%,$(SITES))))
site-manual-%:
	@echo "building $* into /$(prefix)/"
	$(MAKE) -C $*/docs build
	mkdir -p $(DOCS)/$(prefix)
	cp -R $*/docs/dist/site/. $(DOCS)/$(prefix)/
