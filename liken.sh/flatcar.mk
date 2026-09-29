# The Flatcar release that a new redirect host boots first. The
# Makefile includes this file to download and check the image, and
# redirects.tf reads the same lines, so the pin has one copy.
#
# The digests come from the DIGESTS file that Flatcar publishes beside
# the image and signs with its image signing key. README.md gives the
# steps to move the pin and to check that signature.
FLATCAR_VERSION := 4459.2.4
FLATCAR_MD5 := 25178d5d272a093d5dd7b4aa0bedf17f
FLATCAR_SHA512 := 48d78ccfc7f89d145c24635f60d3dcc25581bfc8fb6b76ca0ecd0de37484bb48505f4544c91ce40e003602569a8273ecda40adb152a1c315cd76a2fb72450aac
