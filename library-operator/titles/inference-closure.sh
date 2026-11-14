#!/bin/sh
# Collects the inference runtime of `titles` into one directory tree:
# OpenVINO's runtime with its CPU plugin and its ONNX frontend. The tree
# is one layer of the library-operator-titles image, on the ffmpeg base.
#
# Usage: inference-closure.sh <out> <base> <openvino archive>
#
# OpenVINO is not in Debian, so the runtime comes from Intel's own
# release archive, which the Dockerfile fetches and checks by sha256.
# The script copies named files, not the archive: the archive holds
# every plugin and frontend OpenVINO builds, and the image needs two of
# them. `titles read` runs its models on the CPU, because the text
# detector gives other boxes at the precision an Intel GPU runs it at
# by default, so the image holds no GPU plugin and no OpenCL runtime.
# inference-closure.sh in appearances/ collects those for an image that
# runs its models on a GPU.
#
# The files go to /opt/openvino/lib, flat, because the runtime opens
# each plugin from the directory that holds libopenvino.so. The image
# names that directory in LD_LIBRARY_PATH.
set -eu

out=$1
base=$2
archive=$3

lib=/usr/lib/x86_64-linux-gnu

work=$(mktemp -d)
mkdir -p "$work/openvino"
tar -xzf "$archive" -C "$work/openvino" --strip-components=1

# The OpenVINO core, its C API, the ONNX frontend, each with its links,
# and the CPU plugin. The runtime finds a frontend by its file name in
# its own directory. The patterns name each library with every version
# suffix the archive gives it, so a new release of OpenVINO needs no
# change here. oneTBB is the CPU plugin's thread pool. libtbbbind and
# hwloc only pin threads to NUMA nodes, and oneTBB runs without them.
mkdir -p "$out/opt/openvino/lib"
runtime=$work/openvino/runtime/lib/intel64
for pattern in \
	'libopenvino.so*' \
	'libopenvino_c.so*' \
	'libopenvino_onnx_frontend.so*' \
	'libopenvino_intel_cpu_plugin.so'; do
	found=0
	for file in "$runtime"/$pattern; do
		[ -e "$file" ] || continue
		cp -a "$file" "$out/opt/openvino/lib/"
		found=1
	done
	if [ "$found" = 0 ]; then
		echo "inference-closure.sh: the archive holds no $pattern" >&2
		exit 1
	fi
done
found=0
for file in "$work/openvino/runtime/3rdparty/tbb/lib"/libtbb.so.12*; do
	[ -e "$file" ] || continue
	cp -a "$file" "$out/opt/openvino/lib/"
	found=1
done
if [ "$found" = 0 ]; then
	echo "inference-closure.sh: the archive holds no libtbb.so.12" >&2
	exit 1
fi

# Each license of the files above asks that its notice travel with
# them, and the copies above leave the notices behind. The archive's
# docs/licensing/ holds Intel's distribution license, which covers the
# OpenVINO runtime's libraries, and the notices of the third-party code
# in them. TBB carries its own license file.
mkdir -p "$out/usr/share/doc/openvino"
cp -a "$work/openvino/docs/licensing/." "$out/usr/share/doc/openvino/"
cp "$work/openvino/runtime/3rdparty/tbb/TBB-LICENSE" "$out/usr/share/doc/openvino/"

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
# and asks its own loader to resolve each library.
check=$work/check
mkdir -p "$check"
cp -a "$base/." "$check/"
cp -a "$out/." "$check/"
find "$out" -type f -name '*.so*' | while read -r file; do
	path=${file#"$out"}
	listing=$(chroot "$check" /lib64/ld-linux-x86-64.so.2 \
		--library-path /opt/openvino/lib --list "$path")
	if printf '%s\n' "$listing" | grep -q 'not found'; then
		echo "inference-closure.sh: $path needs a library the image does not have:" >&2
		printf '%s\n' "$listing" | grep 'not found' >&2
		exit 1
	fi
done

rm -rf "$work"
