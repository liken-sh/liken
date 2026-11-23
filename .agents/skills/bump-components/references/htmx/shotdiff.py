"""Compares the screenshots that pages.mjs wrote for two builds.

For each screenshot that both directories contain, the script prints
the share of the pixels that differ and the box around them, and it
writes an image of the differing pixels to the third directory.  A
difference in a small box is usually a clock, such as "read at
16:57:58 UTC" on a logs page, so look at each image before you call a
difference a fault.  It needs Pillow:

    python3 shotdiff.py <old build's directory> <new build's directory> <diff directory>
"""

import os
import sys

from PIL import Image, ImageChops


def main(old, new, out):
    os.makedirs(out, exist_ok=True)
    same = differ = 0
    for name in sorted(f for f in os.listdir(old) if f.endswith(".png")):
        if not os.path.exists(os.path.join(new, name)):
            print(f"only in {old}: {name}")
            continue
        a = Image.open(os.path.join(old, name)).convert("RGB")
        b = Image.open(os.path.join(new, name)).convert("RGB")
        if a.size != b.size:
            print(f"differs {name}: size {a.size}, then {b.size}")
            differ += 1
            continue
        diff = ImageChops.difference(a, b)
        box = diff.getbbox()
        if box is None:
            same += 1
            continue
        mask = diff.convert("L").point(lambda v: 255 if v else 0)
        total = a.size[0] * a.size[1]
        share = 100 * (total - mask.histogram()[0]) / total
        print(f"differs {name}: {share:.3f}% of pixels, in the box {box}")
        mask.save(os.path.join(out, name))
        differ += 1
    print(f"{same} identical, {differ} different")


if __name__ == "__main__":
    if len(sys.argv) != 4:
        sys.exit(__doc__)
    main(*sys.argv[1:])
