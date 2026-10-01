#!/bin/sh
# Collects the inference runtime of `appearances` into one directory
# tree: OpenVINO's runtime with its CPU and GPU plugins and its ONNX
# frontend, and Intel's OpenCL runtime for the GPU plugin. The tree is
# one layer of the library-operator-appearances image, on the ffmpeg
# base.
#
# Usage: inference-closure.sh <out> <base> <openvino archive> <deb>...
#
# Neither runtime is in Debian trixie, the snapshot the bases install
# from, so both come from Intel's own release files, which the
# Dockerfile fetches and checks by sha256. The script copies named
# files, not packages: the archive holds every plugin and frontend
# OpenVINO builds, and the image needs four of them.
#
# The files keep the paths their release gives them. OpenVINO's go to
# /opt/openvino/lib, flat, because the runtime opens each plugin from
# the directory that holds libopenvino.so. Intel's packages install
# the OpenCL driver under the multiarch directory and the graphics
# compiler under /usr/local/lib. The image names both directories
# outside the multiarch one in LD_LIBRARY_PATH.
set -eu

out=$1
base=$2
archive=$3
shift 3

lib=/usr/lib/x86_64-linux-gnu

work=$(mktemp -d)
mkdir -p "$work/openvino" "$work/intel"
tar -xzf "$archive" -C "$work/openvino" --strip-components=1
for deb in "$@"; do
	dpkg-deb -x "$deb" "$work/intel"
done

# The OpenVINO core, its C API, the CPU and GPU plugins, and the ONNX
# frontend, each with its links. The runtime finds a frontend by its
# file name in its own directory. The NPU, AUTO, HETERO, and BATCH
# plugins and the other frontends stay out: `appearances` names the
# CPU or the GPU and reads ONNX alone. oneTBB is the CPU plugin's thread
# pool. libtbbbind and hwloc only pin threads to NUMA nodes, and
# oneTBB runs without them.
mkdir -p "$out/opt/openvino/lib"
runtime=$work/openvino/runtime/lib/intel64
for name in \
	libopenvino.so.2026.4.1 libopenvino.so.2641 libopenvino.so \
	libopenvino_c.so.2026.4.1 libopenvino_c.so.2641 libopenvino_c.so \
	libopenvino_onnx_frontend.so.2026.4.1 libopenvino_onnx_frontend.so.2641 libopenvino_onnx_frontend.so \
	libopenvino_intel_cpu_plugin.so \
	libopenvino_intel_gpu_plugin.so; do
	cp -a "$runtime/$name" "$out/opt/openvino/lib/"
done
cp -a "$work/openvino/runtime/3rdparty/tbb/lib/libtbb.so.12.13" \
	"$work/openvino/runtime/3rdparty/tbb/lib/libtbb.so.12" \
	"$out/opt/openvino/lib/"

# Intel's OpenCL driver (NEO), its memory manager (gmmlib), the ICD
# file that tells the OpenCL loader where the driver is, and the
# graphics compiler (IGC) that the driver opens by name to compile
# the GPU plugin's kernels. The loader, libOpenCL.so.1, is in the
# ffmpeg base already.
for path in \
	"$lib/intel-opencl/libigdrcl.so" \
	"$lib/libigdgmm.so.12" "$lib/libigdgmm.so.12.10.0" \
	/etc/OpenCL/vendors/intel.icd \
	/usr/local/lib/libigc.so.2 /usr/local/lib/libigc.so.2.41.5+1788943183 \
	/usr/local/lib/libigdfcl.so.2 /usr/local/lib/libigdfcl.so.2.41.5+1788943183 \
	/usr/local/lib/libopencl-clang2.so.22.1; do
	mkdir -p "$out$(dirname "$path")"
	cp -a "$work/intel$path" "$out$path"
done

# oneTBB names libdl.so.2 and libpthread.so.0, which glibc keeps as
# empty files for old programs, and no program in the base names
# either. They must come from the same glibc as the base's libc.so.6,
# so the build fails when this builder's glibc differs.
if ! cmp -s "$lib/libc.so.6" "$base$lib/libc.so.6"; then
	echo "inference-closure.sh: this builder's libc.so.6 differs from the base's" >&2
	exit 1
fi
mkdir -p "$out$lib"
cp -a "$lib/libdl.so.2" "$lib/libpthread.so.0" "$out$lib/"

# Every library above must load in the image, against the base's own
# libraries, and the first run in a pod is the first point where a
# missing one shows otherwise. So the check assembles the image's tree
# and asks its own loader to resolve each library. The GPU driver and
# the compiler load only on a machine with an Intel GPU, so this is
# the only place a build without one proves them.
check=$work/check
mkdir -p "$check"
cp -a "$base/." "$check/"
cp -a "$out/." "$check/"
find "$out" -type f -name '*.so*' | while read -r file; do
	path=${file#"$out"}
	listing=$(chroot "$check" /lib64/ld-linux-x86-64.so.2 \
		--library-path /opt/openvino/lib:/usr/local/lib --list "$path")
	if printf '%s\n' "$listing" | grep -q 'not found'; then
		echo "inference-closure.sh: $path needs a library the image does not have:" >&2
		printf '%s\n' "$listing" | grep 'not found' >&2
		exit 1
	fi
done

rm -rf "$work"
