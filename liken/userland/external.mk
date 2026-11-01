# The changes that `liken` makes to buildroot's package recipes. buildroot
# reads this file after every package recipe, so a hook added here runs
# in the recipe's own steps.

# wpa_supplicant builds the features that its .config names. buildroot's
# recipe writes that file from upstream's defconfig and a list of edits,
# so the feature set would be upstream's defaults plus buildroot's
# options. `liken` replaces the file after buildroot writes it, so the
# binary has only the features that wpa_supplicant.config beside this
# file lists, and a reviewer reads the whole list in one place.
define LIKEN_WPA_SUPPLICANT_FEATURES
	cp $(BR2_EXTERNAL_LIKEN_PATH)/wpa_supplicant.config $(WPA_SUPPLICANT_CONFIG)
endef
WPA_SUPPLICANT_POST_CONFIGURE_HOOKS += LIKEN_WPA_SUPPLICANT_FEATURES
