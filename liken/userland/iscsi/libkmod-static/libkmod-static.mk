################################################################################
#
# libkmod-static
#
################################################################################

# The library of kmod, built static for iscsid. iscsid asks libkmod
# only whether iscsi_tcp is loaded, and init loads the module before
# k3s starts, so the build leaves out the tools and every compression
# library: libkmod never reads a module file here.
LIBKMOD_STATIC_VERSION = 34.3
LIBKMOD_STATIC_SOURCE = kmod-$(LIBKMOD_STATIC_VERSION).tar.xz
LIBKMOD_STATIC_SITE = $(BR2_KERNEL_MIRROR)/linux/utils/kernel/kmod
LIBKMOD_STATIC_LICENSE = LGPL-2.1+
LIBKMOD_STATIC_LICENSE_FILES = libkmod/COPYING
LIBKMOD_STATIC_CPE_ID_VENDOR = kernel
LIBKMOD_STATIC_CPE_ID_PRODUCT = kmod
LIBKMOD_STATIC_INSTALL_STAGING = YES
LIBKMOD_STATIC_INSTALL_TARGET = NO
LIBKMOD_STATIC_DEPENDENCIES = host-pkgconf

LIBKMOD_STATIC_CONF_OPTS = \
	-Dtools=false \
	-Dmanpages=false \
	-Dlogging=false \
	-Dzstd=disabled \
	-Dxz=disabled \
	-Dzlib=disabled \
	-Dopenssl=disabled \
	-Dbashcompletiondir=no \
	-Dfishcompletiondir=no \
	-Dzshcompletiondir=no

$(eval $(meson-package))
