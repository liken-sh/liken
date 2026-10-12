"""Writes the synthetic star field that the smoke check solves.

The smoke check runs on a CI runner with no index files, because the
real ones are hundreds of megabytes. So the check solves a field whose
stars are made up: this script places them on an invented patch of
sky, draws the frame that a camera would take of them, and writes
their positions as a catalog. build-astrometry-index turns the catalog
into an index file small enough to commit, and the engine solves the
frame against it with the same programs and options as a real solve.

Usage: python3 field.py <frame.fits> <catalog.fits>

The script uses only the standard library and a fixed seed, so it
writes the same two files every time. The committed field.fits and
index.fits came from:

    python3 field.py field.fits stars.fits
    build-astrometry-index -i stars.fits -o index.fits -P 2 -E -S MAG -I 1
"""

import math
import random
import struct
import sys

# The center of the invented patch, in degrees, and the scale of the
# frame. 2.235 arcseconds per pixel is the lab's refractor and camera,
# so the scale hints match the ones that the worker passes.
CENTER_RA = 150.0
CENTER_DEC = 30.0
SCALE = 2.235 / 3600
SIZE = 256

# The catalog covers twice the frame's width on each axis, so the
# index holds quads that cross the frame's edges, as a real one does.
SPAN = 2 * SIZE * SCALE
STARS = 400


def card(key, value):
    if isinstance(value, str):
        value = f"'{value:<8}'"
    elif isinstance(value, bool):
        value = "T" if value else "F"
    return f"{key:<8}= {value:>20}".ljust(80)


def header(cards):
    text = "".join(card(k, v) for k, v in cards) + "END".ljust(80)
    return pad(text.encode("ascii"), b" ")


def pad(data, fill):
    return data + fill * (-len(data) % 2880)


def project(ra, dec):
    """The gnomonic (TAN) projection of FITS WCS, to pixels."""
    ra0, dec0 = math.radians(CENTER_RA), math.radians(CENTER_DEC)
    ra, dec = math.radians(ra), math.radians(dec)
    cos_c = math.sin(dec0) * math.sin(dec) + math.cos(dec0) * math.cos(dec) * math.cos(ra - ra0)
    xi = math.cos(dec) * math.sin(ra - ra0) / cos_c
    eta = (math.cos(dec0) * math.sin(dec) - math.sin(dec0) * math.cos(dec) * math.cos(ra - ra0)) / cos_c
    # East is to the left, as on the sky seen from below.
    x = (SIZE + 1) / 2 - math.degrees(xi) / SCALE
    y = (SIZE + 1) / 2 + math.degrees(eta) / SCALE
    return x, y


def main(frame_path, catalog_path):
    rng = random.Random(4208)
    stars = []
    for _ in range(STARS):
        dec = CENTER_DEC + rng.uniform(-SPAN / 2, SPAN / 2)
        ra = CENTER_RA + rng.uniform(-SPAN / 2, SPAN / 2) / math.cos(math.radians(dec))
        mag = rng.uniform(8, 15)
        stars.append((ra, dec, mag))

    # A background of 100 counts with its noise, and each star a
    # Gaussian of 1.5 pixels whose peak falls with its magnitude.
    pixels = [[100 + rng.gauss(0, 3) for _ in range(SIZE)] for _ in range(SIZE)]
    sigma = 1.5
    for ra, dec, mag in stars:
        x, y = project(ra, dec)
        peak = 30000 * 10 ** (-0.4 * (mag - 8))
        for j in range(int(y) - 6, int(y) + 7):
            for i in range(int(x) - 6, int(x) + 7):
                if 1 <= i <= SIZE and 1 <= j <= SIZE:
                    r2 = (i - x) ** 2 + (j - y) ** 2
                    pixels[j - 1][i - 1] += peak * math.exp(-r2 / (2 * sigma**2))

    # The brightest stars saturate at the top of a signed 16-bit pixel,
    # as they would on a camera.
    data = b"".join(struct.pack(">h", max(0, min(32767, round(v)))) for row in pixels for v in row)
    with open(frame_path, "wb") as f:
        f.write(header([("SIMPLE", True), ("BITPIX", 16), ("NAXIS", 2), ("NAXIS1", SIZE), ("NAXIS2", SIZE)]))
        f.write(pad(data, b"\0"))

    rows = b"".join(struct.pack(">ddd", ra, dec, mag) for ra, dec, mag in stars)
    with open(catalog_path, "wb") as f:
        f.write(header([("SIMPLE", True), ("BITPIX", 8), ("NAXIS", 0), ("EXTEND", True)]))
        f.write(
            header(
                [
                    ("XTENSION", "BINTABLE"),
                    ("BITPIX", 8),
                    ("NAXIS", 2),
                    ("NAXIS1", 24),
                    ("NAXIS2", len(stars)),
                    ("PCOUNT", 0),
                    ("GCOUNT", 1),
                    ("TFIELDS", 3),
                    ("TTYPE1", "RA"),
                    ("TFORM1", "D"),
                    ("TTYPE2", "DEC"),
                    ("TFORM2", "D"),
                    ("TTYPE3", "MAG"),
                    ("TFORM3", "D"),
                ]
            )
        )
        f.write(pad(rows, b"\0"))


if __name__ == "__main__":
    main(sys.argv[1], sys.argv[2])
