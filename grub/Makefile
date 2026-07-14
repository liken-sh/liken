# The grub domain: the bootloader liken ships for BIOS machines,
# vendored prebuilt from Ubuntu's archive (fetch.sh documents why
# BIOS machines need a bootloader when UEFI machines don't, and why
# grub-mkimage rides along with the modules it links).
#
# The one artifact built here is the core image: grub-mkimage links
# the pinned modules into a single compressed program with early.cfg
# embedded, ready to be written into a biosBoot partition. Everything
# about it is chosen at build time, once, for every machine:
#
#   - The modules are the closure of what early.cfg and the
#     installer-rendered grub.cfg actually run: disk access
#     (biosdisk), the partition table and filesystem the boot path
#     reads (part_gpt, fat), finding volumes by label (search,
#     search_label), the full script engine and menu (normal),
#     loading the kernel (linux), the environment block (loadenv),
#     and the small comforts of a debuggable config (test, echo,
#     serial). grub-mkimage resolves their dependencies through the
#     vendored tree's moddep.lst.
#
#   - The prefix (-p) is where GRUB looks for its config once
#     early.cfg has pointed $root at the boot home: /grub on that
#     volume, the directory the installer populates.
#
# The biosBoot partition is 1Mi and the core image must leave room
# for its own first-sector bookkeeping, so the recipe fails loudly if
# the image somehow outgrows the partition rather than letting an
# install truncate it.
VERSION := $(strip $(file <VERSION))
DIST := dist/$(VERSION)

MODULES := biosdisk part_gpt fat search search_label normal linux \
	loadenv test echo serial

all: $(DIST)/grub-core.img

$(DIST)/grub-mkimage $(DIST)/grub-boot.img &: VERSION fetch.sh
	./fetch.sh

# The embedded copy of early.cfg is stripped to its bare commands
# first: the early parser that runs it is quote-blind, and prose
# comments with apostrophes swallow the commands after them (the
# file's own tail comment tells the story).
$(DIST)/grub-core.img: $(DIST)/grub-mkimage $(DIST)/grub-boot.img early.cfg
	grep -v '^#' early.cfg | grep -v '^$$' > $(DIST)/embedded.cfg
	$(DIST)/grub-mkimage --format i386-pc --directory $(DIST)/i386-pc \
		--config $(DIST)/embedded.cfg --prefix /grub --output $@ $(MODULES)
	@size=$$(stat -c%s $@); \
	if [ $$size -gt 1000000 ]; then \
		echo "grub-core.img is $$size bytes and won't fit a 1Mi biosBoot partition"; \
		rm $@; exit 1; \
	fi
	@stat -c '%s bytes  grub-core.img' $@

.PHONY: all clean

clean:
	rm -rf dist
