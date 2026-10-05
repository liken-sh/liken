"""Counts the bright pixels of a 16-bit FITS frame, with no library
beyond Python's own, and fails below a floor. The CCD simulator's frame
of M42 at 500 mm has about 170 pixels 500 counts above the median, and
an empty frame has none."""

import array
import sys

FLOOR = 50

with open(sys.argv[1], "rb") as f:
    data = f.read()
header = {}
offset = 0
while True:
    block = data[offset : offset + 2880]
    offset += 2880
    for i in range(0, 2880, 80):
        card = block[i : i + 80].decode("ascii")
        key = card[:8].strip()
        if "=" in card[8:10]:
            header[key] = card[10:].split("/")[0].strip()
        if key == "END":
            break
    else:
        continue
    break
width, height = int(header["NAXIS1"]), int(header["NAXIS2"])
pixels = array.array("h")
pixels.frombytes(data[offset : offset + width * height * 2])
if sys.byteorder == "little":
    pixels.byteswap()
zero = int(float(header.get("BZERO", "0")))
values = sorted(p + zero for p in pixels)
median = values[len(values) // 2]
bright = len(values) - next(i for i, v in enumerate(values) if v > median + 500) if values[-1] > median + 500 else 0
print(f"{bright} bright pixels above a median of {median}")
if bright < FLOOR:
    sys.exit(f"fewer than {FLOOR} bright pixels: the simulator drew no stars")
