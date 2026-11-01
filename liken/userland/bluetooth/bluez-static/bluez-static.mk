################################################################################
#
# bluez-static
#
################################################################################

# BlueZ for the bluetoothd image of bluetooth-operator. The image runs
# bluetoothd, and btmon while a person traces the link, and a person
# runs bluetoothctl and btmgmt with kubectl exec. Pairing is the
# operator's job, over D-Bus, so the build leaves out the CUPS printer
# backend and the OBEX file-transfer daemon. The deprecated tools stay
# out too: they drive the raw HCI socket while bluetoothd drives the
# management channel, so a command from one of them and the state that
# the daemon holds disagree.
#
# udev stays out, because the image runs no udev and buildroot's udev
# needs shared libraries. The plugins that read udev, sixaxis and
# hid2hci, are for USB pairing of game pads, which this image does not
# do.
BLUEZ_STATIC_VERSION = 5.87
BLUEZ_STATIC_SOURCE = bluez-$(BLUEZ_STATIC_VERSION).tar.xz
BLUEZ_STATIC_SITE = $(BR2_KERNEL_MIRROR)/linux/bluetooth
BLUEZ_STATIC_LICENSE = GPL-2.0+, LGPL-2.1+
BLUEZ_STATIC_LICENSE_FILES = COPYING COPYING.LIB
BLUEZ_STATIC_CPE_ID_VENDOR = bluez
BLUEZ_STATIC_CPE_ID_PRODUCT = bluez
BLUEZ_STATIC_DEPENDENCIES = host-pkgconf dbus libglib2 readline

BLUEZ_STATIC_CONF_OPTS = \
	--enable-client \
	--enable-datafiles \
	--enable-monitor \
	--enable-tools \
	--disable-manpages \
	--disable-cups \
	--disable-obex \
	--disable-systemd \
	--disable-udev \
	--disable-deprecated \
	--disable-asan \
	--disable-lsan \
	--disable-ubsan \
	--disable-pie

# readline's pkg-config file names no terminal library, so a static
# bluetoothctl would have no tputs. LIBS hands configure ncurses, which
# buildroot's readline links.
BLUEZ_STATIC_CONF_ENV = LIBS="-lncurses"

# libtool reads -static as "link this project's own .la libraries
# static" and leaves the rest dynamic. -all-static makes the whole
# binary static.
BLUEZ_STATIC_MAKE_OPTS = LDFLAGS="$(TARGET_LDFLAGS) -all-static"

# btmgmt is a noinst_PROGRAMS entry of BlueZ's build, so make install
# leaves it in the build tree.
define BLUEZ_STATIC_INSTALL_BTMGMT
	$(INSTALL) -D -m 0755 $(@D)/tools/btmgmt $(TARGET_DIR)/usr/bin/btmgmt
endef
BLUEZ_STATIC_POST_INSTALL_TARGET_HOOKS += BLUEZ_STATIC_INSTALL_BTMGMT

$(eval $(autotools-package))
