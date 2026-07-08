# Requestable kernel modules

Milestone 18 — Not started

The vendored kernel is Ubuntu's generic build, so nearly every driver
Linux has already exists as a module in the package we fetch.
modules.conf is the deliberately fixed, reviewable list of the few
the image ships and init loads. A machine whose workloads use
hardware (a GPU for transcoding, a USB accelerator, a capture card)
needs its drivers, and today the only way to get them is to edit the
OS.

The capability: a deployment declares the extra modules it wants, the
image build ships them with their dependencies, and init loads them
at boot. Two questions to settle in the design: where the declaration
lives (on the Machine that has the hardware, or as an image-build
input, since the modules have to be in the archive either way), and
how to keep the list reviewable once it is no longer fixed.
