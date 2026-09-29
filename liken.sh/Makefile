# The files that Terraform reads from this directory but that are not
# source.
#
#   make flatcar   download the pinned Flatcar image into flatcar/
#
# The redirect host boots the Flatcar Container Linux image that
# redirects.tf uploads to Linode. The image is about 600 MB, so it
# stays out of history: this target downloads it into flatcar/, which
# .gitignore excludes, and Terraform reads it from there.
#
# The version and the SHA-512 below are the pin. The digest comes from
# the DIGESTS file that Flatcar publishes beside the image and signs
# with its image signing key, so a download that does not match fails
# here, before Terraform uploads it. README.md gives the steps to move
# the pin and to check the signature on the new digest.
FLATCAR_VERSION := 4757.2.0
FLATCAR_SHA512 := c1cfaab50dd7872345cd4ab790722961f9be7e64cfc3dea9f84bc5d38f6a13973917bea8b493da3f408c784f782e9307f3869f4c9eab28888c4544133e19d81a

FLATCAR_IMAGE := flatcar/$(FLATCAR_VERSION)/flatcar_production_akamai_image.bin.gz
FLATCAR_URL := https://stable.release.flatcar-linux.net/amd64-usr/$(FLATCAR_VERSION)/flatcar_production_akamai_image.bin.gz

# flatcar/version tells Terraform which downloaded image to upload.
# The target writes it after the digest check, so the file never names
# an image that failed the check.
.PHONY: flatcar
flatcar: $(FLATCAR_IMAGE)
	echo $(FLATCAR_VERSION) > flatcar/version

$(FLATCAR_IMAGE):
	mkdir -p $(@D)
	curl --fail --location --output $@.part $(FLATCAR_URL)
	echo "$(FLATCAR_SHA512)  $@.part" | sha512sum --check --strict
	mv $@.part $@
