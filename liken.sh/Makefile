# The files that Terraform reads from this directory but that are not
# source.
#
#   make flatcar   download the pinned Flatcar image into flatcar/
#
# The redirect host boots the Flatcar Container Linux image that
# redirects.tf uploads to Linode. The image is about 550 MB, so it
# stays out of history: this target downloads it into flatcar/, which
# .gitignore excludes. Only an apply that creates the Linode image
# reads the file. A plan needs no download, because flatcar.mk gives
# Terraform the version and the MD5 digest.
include flatcar.mk

FLATCAR_IMAGE := flatcar/$(FLATCAR_VERSION)/flatcar_production_akamai_image.bin.gz
FLATCAR_URL := https://stable.release.flatcar-linux.net/amd64-usr/$(FLATCAR_VERSION)/flatcar_production_akamai_image.bin.gz

# Linode refuses an uploaded image that is larger than 6144 MiB
# uncompressed, and it refuses it only after the upload. The disk's
# size is in the image's first MiB: the GPT header at byte 512 gives
# the address of the backup header, which is the disk's last 512-byte
# sector, at byte 32 of the header. So the check reads that one field
# and does not decompress the whole image.
LINODE_IMAGE_LIMIT := 6442450944

.PHONY: flatcar
flatcar: $(FLATCAR_IMAGE)

$(FLATCAR_IMAGE):
	mkdir -p $(@D)
	curl --fail --location --max-time 1800 --output $@.part $(FLATCAR_URL)
	echo "$(FLATCAR_SHA512)  $@.part" | sha512sum --check --strict
	last=$$(gzip -dc $@.part | head -c 1048576 | od -An -tu8 -j544 -N8 | tr -d ' '); \
	size=$$(( (last + 1) * 512 )); \
	echo "$@: $$size bytes uncompressed, and Linode accepts at most $(LINODE_IMAGE_LIMIT)"; \
	test $$size -le $(LINODE_IMAGE_LIMIT)
	mv $@.part $@
