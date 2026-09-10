#!/usr/bin/env python3
"""The operator side of layout/smoke.sh. Run it from that script.

It speaks the control protocol on the socket the module opened, starts
two clients, places their surfaces, and reads back screenshots to check
where the pixels landed. It uses the standard library only, so a
developer machine needs no extra packages.
"""

import glob
import os
import socket
import struct
import subprocess
import sys
import time
import zlib

WORK = os.environ["WORK"]
IMAGE = os.environ["IMAGE"]
WESTON = os.environ["WESTON"]
CLIENT = os.environ["CLIENT"]

CONTROL = os.path.join(WORK, "control", "layout.sock")
RUNTIME = os.path.join(WORK, "run")
SHOTS = os.path.join(WORK, "shot")
CLAIM_SOCKET = "wayland-claim1"
CONNECTOR = "headless"

# Two rectangles that do not overlap, so a screenshot says which
# surface is where.
CLAIM_RECT = (100, 50, 400, 300)
SHARED_RECT = (700, 300, 400, 300)
MOVED_RECT = (100, 400, 400, 300)

# The mask ivi-layout applies has hard edges, and scaling a buffer to
# the destination can touch the pixel next to one. The outside check
# skips this many pixels around each rectangle.
EDGE = 2


class Control:
    """One connection to the module, with its unsolicited events kept."""

    def __init__(self, path):
        self.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        self.sock.connect(path)
        self.sock.settimeout(10)
        self.buffer = b""
        self.events = []
        self.seq = 0

    def read_line(self):
        while b"\n" not in self.buffer:
            chunk = self.sock.recv(4096)
            if not chunk:
                raise AssertionError("the module closed the connection")
            self.buffer += chunk
        line, self.buffer = self.buffer.split(b"\n", 1)
        return line.decode()

    def request(self, text):
        """Send one request and return its reply, keeping every event."""
        self.seq += 1
        self.sock.sendall(("%d %s\n" % (self.seq, text)).encode())
        while True:
            reply = self.read_line()
            words = reply.split()
            if words[0] in ("ok", "error") and words[1] == str(self.seq):
                return reply
            self.events.append(reply)

    def expect_ok(self, text):
        reply = self.request(text)
        assert reply.startswith("ok "), "%s -> %s" % (text, reply)
        report("%s: %s" % (text, reply))

    def wait_event(self, word, timeout=30):
        deadline = time.monotonic() + timeout
        while True:
            for event in self.events:
                if event.split()[0] == word:
                    self.events.remove(event)
                    return event
            self.sock.settimeout(max(0.5, deadline - time.monotonic()))
            self.events.append(self.read_line())
            if time.monotonic() > deadline:
                raise AssertionError("no %s event in %d seconds" % (word, timeout))


def connect(path, timeout=30):
    deadline = time.monotonic() + timeout
    while True:
        try:
            return Control(path)
        except OSError as error:
            if time.monotonic() > deadline:
                raise AssertionError("no control socket at %s: %s" % (path, error))
            time.sleep(0.2)


def read_png(path):
    """Decode one 8-bit RGB or RGBA PNG into (width, height, channels, rows)."""
    data = open(path, "rb").read()
    assert data[:8] == b"\x89PNG\r\n\x1a\n", "%s is not a PNG" % path
    pos, pixels, header = 8, b"", None
    while pos < len(data):
        length = struct.unpack(">I", data[pos : pos + 4])[0]
        kind = data[pos + 4 : pos + 8]
        body = data[pos + 8 : pos + 8 + length]
        if kind == b"IHDR":
            header = struct.unpack(">IIBBBBB", body)
        elif kind == b"IDAT":
            pixels += body
        pos += 12 + length
    width, height, depth, color, _, _, interlace = header
    assert depth == 8 and interlace == 0 and color in (2, 6), "unsupported PNG"
    channels = 3 if color == 2 else 4
    rows = unfilter(zlib.decompress(pixels), width, height, channels)
    return width, height, channels, rows


def unfilter(raw, width, height, channels):
    """Undo the five PNG scanline filters, which any encoder may use."""
    stride = width * channels
    rows, previous, pos = [], bytearray(stride), 0
    for _ in range(height):
        kind = raw[pos]
        row = bytearray(raw[pos + 1 : pos + 1 + stride])
        pos += 1 + stride
        for i in range(stride):
            left = row[i - channels] if i >= channels else 0
            up = previous[i]
            upleft = previous[i - channels] if i >= channels else 0
            if kind == 1:
                row[i] = (row[i] + left) & 0xFF
            elif kind == 2:
                row[i] = (row[i] + up) & 0xFF
            elif kind == 3:
                row[i] = (row[i] + ((left + up) >> 1)) & 0xFF
            elif kind == 4:
                near = abs(up - upleft), abs(left - upleft), abs(left + up - 2 * upleft)
                if near[0] <= near[1] and near[0] <= near[2]:
                    best = left
                elif near[1] <= near[2]:
                    best = up
                else:
                    best = upleft
                row[i] = (row[i] + best) & 0xFF
        rows.append(row)
        previous = row
    return rows


class Frame:
    """One screenshot, asked about by rectangle."""

    def __init__(self, path):
        self.width, self.height, self.channels, self.rows = read_png(path)
        self.name = os.path.basename(path)

    def is_painted(self, x, y):
        row = self.rows[y]
        i = x * self.channels
        return bool(row[i] or row[i + 1] or row[i + 2])

    def painted_in(self, rect):
        x0, y0, w, h = rect
        count = 0
        for y in range(y0, y0 + h):
            for x in range(x0, x0 + w):
                count += 1 if self.is_painted(x, y) else 0
        return count, w * h

    def paint_total(self, rect):
        """The sum of every channel in the rectangle, which is how
        bright it is. A surface partway through a fade is dimmer than
        the same surface at full opacity."""
        x0, y0, w, h = rect
        total = 0
        for y in range(y0, y0 + h):
            row = self.rows[y]
            for x in range(x0, x0 + w):
                i = x * self.channels
                total += row[i] + row[i + 1] + row[i + 2]
        return total

    def painted_outside(self, rects):
        count = total = 0
        for y in range(self.height):
            for x in range(self.width):
                if any(within_edge(rect, x, y) for rect in rects):
                    continue
                total += 1
                count += 1 if self.is_painted(x, y) else 0
        return count, total


def within_edge(rect, x, y):
    x0, y0, w, h = rect
    return x0 - EDGE <= x < x0 + w + EDGE and y0 - EDGE <= y < y0 + h + EDGE


def docker(*args):
    return subprocess.run(
        ("docker",) + args, check=True, capture_output=True, text=True
    ).stdout.strip()


def start_client(name, wayland_display):
    docker(
        "run", "-d", "--name", name,
        "--user", "%d:%d" % (os.getuid(), os.getgid()),
        "-v", "%s:/run/liken" % RUNTIME,
        "-e", "XDG_RUNTIME_DIR=/run/liken",
        "-e", "WAYLAND_DISPLAY=%s" % wayland_display,
        IMAGE, "weston-simple-shm",
    )


def capture(wait=2):
    """Take one screenshot, after a wait. The clients draw on their own
    frame callbacks, so the default wait lets them paint. A capture
    inside a transition passes a shorter one."""
    time.sleep(wait)
    for stale in glob.glob(os.path.join(SHOTS, "*.png")):
        os.remove(stale)
    docker(
        "exec", "-e", "XDG_RUNTIME_DIR=/run/liken", "-e", "WAYLAND_DISPLAY=wayland-0",
        "-w", "/shot", WESTON, "weston-screenshooter",
    )
    shots = glob.glob(os.path.join(SHOTS, "*.png"))
    assert shots, "weston-screenshooter wrote no file"
    frame = Frame(shots[0])
    report("screenshot %s at %dx%d" % (frame.name, frame.width, frame.height))
    return frame


def place(control, surface_id, rect, transition):
    x, y, w, h = rect
    control.expect_ok(
        "place %s %s %d %d %d %d %s" % (surface_id, CONNECTOR, x, y, w, h, transition)
    )


def report(text):
    print("   %s" % text, flush=True)


def assert_painted(frame, rect, name):
    count, total = frame.painted_in(rect)
    report("%s: %d of %d pixels painted in %s" % (name, count, total, rect))
    assert count > total // 5, "%s is nearly black" % name


def assert_black(frame, rect, name):
    count, total = frame.painted_in(rect)
    report("%s: %d of %d pixels painted in %s" % (name, count, total, rect))
    assert count == 0, "%s is painted" % name


def assert_fading(frame, rect, name, opaque):
    """A surface partway through a fade still holds paint, and it is
    dimmer than the same surface at full opacity, because the pixels
    it is blended into are the black curtain under it."""
    count, total = frame.painted_in(rect)
    brightness = frame.paint_total(rect)
    report("%s: %d of %d pixels painted in %s, brightness %d of %d"
           % (name, count, total, rect, brightness, opaque))
    assert count > 0, "%s is black" % name
    assert brightness < opaque, "%s is no dimmer than the surface it fades from" % name


def assert_nothing_outside(frame, rects):
    count, total = frame.painted_outside(rects)
    report("outside every rectangle: %d of %d pixels painted" % (count, total))
    assert count == 0, "%d pixels are painted outside the layout" % count


def main():
    control = connect(CONTROL)
    greeting = control.read_line()
    assert greeting == "hello liken-layout 1", greeting
    report("greeting: %s" % greeting)
    control.expect_ok("hello liken-operator 1")

    output = control.wait_event("output", timeout=5)
    assert output.split() == ["output", CONNECTOR, "1280", "720", "1"], output
    report("output: %s" % output)

    control.expect_ok("listen %s %s" % (CLAIM_SOCKET, CONNECTOR))
    path = os.path.join(RUNTIME, CLAIM_SOCKET)
    assert os.path.exists(path), "the module opened no socket at %s" % path
    report("the module opened %s" % path)

    start_client(CLIENT, CLAIM_SOCKET)
    surface = control.wait_event("surface").split()
    assert surface[2] == CLAIM_SOCKET, "the surface came in as %s" % surface[2]
    claim_id = surface[1]
    report("surface: %s" % " ".join(surface))

    place(control, claim_id, CLAIM_RECT, "none 0")
    control.expect_ok("order %s %s" % (CONNECTOR, claim_id))
    control.expect_ok("commit")

    frame = capture()
    assert_painted(frame, CLAIM_RECT, "the claim's surface")
    assert_nothing_outside(frame, [CLAIM_RECT])

    # A client on the shared socket belongs to no claim, and the module
    # reports it as wayland-0 whatever the client says about itself.
    start_client(CLIENT + "-shared", "wayland-0")
    surface = control.wait_event("surface").split()
    assert surface[2] == "wayland-0", "the shared surface came in as %s" % surface[2]
    shared_id = surface[1]
    report("surface: %s" % " ".join(surface))

    place(control, shared_id, SHARED_RECT, "fade 300")
    control.expect_ok("order %s %s %s" % (CONNECTOR, claim_id, shared_id))
    control.expect_ok("commit")

    frame = capture()
    assert_painted(frame, CLAIM_RECT, "the claim's surface")
    assert_painted(frame, SHARED_RECT, "the shared surface")
    assert_nothing_outside(frame, [CLAIM_RECT, SHARED_RECT])

    # The module holds a black background under every surface, so the
    # rectangle a surface moves out of goes black again.
    place(control, claim_id, MOVED_RECT, "move 300")
    control.expect_ok("commit")

    frame = capture()
    assert_black(frame, CLAIM_RECT, "the rectangle the surface left")
    assert_painted(frame, MOVED_RECT, "the moved surface")
    assert_nothing_outside(frame, [MOVED_RECT, SHARED_RECT])

    control.expect_ok("hide %s none 0" % claim_id)
    control.expect_ok("commit")

    frame = capture()
    assert_black(frame, MOVED_RECT, "the hidden surface")
    assert_painted(frame, SHARED_RECT, "the shared surface")
    opaque = frame.paint_total(SHARED_RECT)

    # A hide with a fade leaves the surface on the screen while the
    # fade runs, which is how a surface that is still alive leaves a
    # region. The headless backend repaints on its own timer, so the
    # fade progresses with no frame from the client, and the
    # screenshot below lands inside the fade because the capture takes
    # a moment of its own.
    started = time.monotonic()
    control.expect_ok("hide %s fade 600" % shared_id)
    control.expect_ok("commit")

    frame = capture(wait=0)
    report("the fade started %d ms before this frame was read"
           % (1000 * (time.monotonic() - started)))
    assert_fading(frame, SHARED_RECT, "the fading surface", opaque)

    # The second frame is read once the fade's 600 ms are past.
    frame = capture(wait=max(0, started + 0.8 - time.monotonic()))
    assert_black(frame, SHARED_RECT, "the surface the fade took off")

    control.expect_ok("close %s" % CLAIM_SOCKET)
    assert not os.path.exists(path), "%s is still there after close" % path
    report("%s is gone" % path)

    # A Deployment with the Recreate strategy and a re-run Job both keep
    # their ResourceClaim, so the kubelet closes and opens the same
    # socket name inside one compositor lifetime.
    control.expect_ok("listen %s %s" % (CLAIM_SOCKET, CONNECTOR))
    assert os.path.exists(path), "the module opened no socket at %s again" % path
    report("the module opened %s again" % path)

    start_client(CLIENT + "-again", CLAIM_SOCKET)
    surface = control.wait_event("surface").split()
    assert surface[2] == CLAIM_SOCKET, "the surface came in as %s" % surface[2]
    assert surface[1] != claim_id, "the module reused surface id %s" % surface[1]
    report("surface: %s" % " ".join(surface))

    print("   PASS", flush=True)
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except AssertionError as failure:
        print("   FAIL: %s" % failure, flush=True)
        sys.exit(1)
