################################################################################
#
# open-iscsi-static
#
################################################################################

# iscsid and iscsiadm, the host half of the iscsi feature. CSI drivers
# chroot into the host and run its iscsiadm, which talks to the
# running iscsid, so the OS image carries both, and the feature's
# DaemonSet runs the same iscsid. iSNS is a discovery directory that
# the feature does not offer, so the build leaves it out.
OPEN_ISCSI_STATIC_VERSION = 2.1.13
OPEN_ISCSI_STATIC_SOURCE = open-iscsi-$(OPEN_ISCSI_STATIC_VERSION).tar.gz
OPEN_ISCSI_STATIC_SITE = $(call github,open-iscsi,open-iscsi,$(OPEN_ISCSI_STATIC_VERSION))
OPEN_ISCSI_STATIC_LICENSE = GPL-2.0+, GPL-3.0+, LGPL-3.0+
OPEN_ISCSI_STATIC_LICENSE_FILES = COPYING README libopeniscsiusr/COPYING
OPEN_ISCSI_STATIC_CPE_ID_VENDOR = open-iscsi_project
OPEN_ISCSI_STATIC_CPE_ID_PRODUCT = open-iscsi
OPEN_ISCSI_STATIC_DEPENDENCIES = host-pkgconf libkmod-static openssl util-linux

OPEN_ISCSI_STATIC_CONF_OPTS = \
	-Dno_systemd=true \
	-Disns=disabled

$(eval $(meson-package))
