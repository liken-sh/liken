#!/usr/bin/env python3
"""The liken_arrangement_v1 side of layout/smoke.sh. Run it as smoke.sh arrange.

It places stand-in films of one claim over the whole output, arranges
them with liken-arrange-drill, and reads screenshots and the control
socket. It asserts:

- a client on wayland-0 or the capture socket is refused not_a_claim,
  a second client of the claim is refused busy, and after the first
  arranger goes a new client is granted;
- granted comes with the region's size and a count for each app_id;
- a surface that no entry names fills the region below the named ones;
- the order of the entries is the stacking order;
- an entry's opacity dims the surface;
- a commit partway through a tween starts from where the surface shows;
- two entries with different delays move one after the other;
- a resize entry sends its client exactly one configure, when it lands,
  and the client's pixels then show one to one;
- a film whose own content is a solid colour, with a picture on a
  subsurface, keeps its backdrop over the region while the picture
  moves, because weston turns a backdrop that covers the output into
  the CRTC's background colour;
- when the arranger's client is killed, the surfaces tween back to
  the default;
- the arranger hears the region's new size when the operator places the
  claim at another size;
- the control socket reports surface-app-id, arranger, arrangement, and
  arranger-gone lines.
"""

import os
import queue
import subprocess
import sys
import threading
import time

from smoke_client import (
    CAPTURE_DISPLAY, CLAIM_SOCKET, CONNECTOR, CONTROL, IMAGE, RUNTIME,
    WESTON, WESTON_CONFIG, capture, connect, docker, place, report,
)

PREFIX = "liken-layout-smoke-"
WIDTH, HEIGHT = 1280, 720
FULL = (0, 0, WIDTH, HEIGHT)
TARGET = (64, 180, 640, 360)
# Pixels this close to a rectangle's edge may blend with their
# neighbours when a renderer scales a buffer.
EDGE = 2

RED = (255, 0, 0)
BLUE = (0, 0, 255)
GREEN = (0, 255, 0)
GREY = (64, 64, 64)


def colour_at(frame, x, y):
    row = frame.rows[y]
    i = x * frame.channels
    return row[i], row[i + 1], row[i + 2]


def near(colour, want):
    return all(abs(a - b) < 48 for a, b in zip(colour, want))


def inner(rect):
    x, y, w, h = rect
    return x + EDGE, y + EDGE, w - 2 * EDGE, h - 2 * EDGE


def count_colour(frame, rect, want):
    x0, y0, w, h = rect
    return sum(
        1 for y in range(y0, y0 + h) for x in range(x0, x0 + w)
        if near(colour_at(frame, x, y), want)
    )


def box_of(frame, want):
    """The bounding box of the pixels of one colour, as x, y, w, h."""
    xs, ys = [], []
    for y in range(frame.height):
        for x in range(0, frame.width, 1):
            if near(colour_at(frame, x, y), want):
                xs.append(x)
                ys.append(y)
    if not xs:
        return (0, 0, 0, 0)
    return (min(xs), min(ys), max(xs) - min(xs) + 1, max(ys) - min(ys) + 1)


def assert_filled(frame, rect, want, name):
    region = inner(rect)
    count = count_colour(frame, region, want)
    total = region[2] * region[3]
    report("%s: %d of %d pixels in %s" % (name, count, total, region))
    assert count == total, "%s does not fill %s" % (name, rect)


def assert_box(frame, want, rect, name):
    box = box_of(frame, want)
    report("%s covers %s" % (name, box))
    assert all(abs(a - b) <= EDGE for a, b in zip(box, rect)), (
        "%s covers %s, want %s" % (name, box, rect))


def run_args(name):
    return [
        "--name", PREFIX + name,
        "--user", "%d:%d" % (os.getuid(), os.getgid()),
        "-v", "%s:/run/liken" % RUNTIME,
        "-v", "%s:/etc/weston" % WESTON_CONFIG,
        "-e", "XDG_RUNTIME_DIR=/run/liken",
        IMAGE, "liken-arrange-drill",
    ]


def start_film(name, *args):
    docker("run", "-d", *run_args(name), "--film", *args, CLAIM_SOCKET)


def drawn_sizes(name):
    """Every size a stand-in film drew at, from its log."""
    log = docker("logs", PREFIX + name)
    return [line.split()[2] for line in log.splitlines() if line.split()[1:2] == ["drawn"]]


class Arranger:
    """liken-arrange-drill reading commands from its standard input."""

    def __init__(self, name):
        self.name = PREFIX + name
        self.process = subprocess.Popen(
            ["docker", "run", "-i", "--rm"] + run_args(name) + [CLAIM_SOCKET, "stdin"],
            stdin=subprocess.PIPE, stdout=subprocess.PIPE, text=True,
        )
        self.lines = queue.Queue()
        self.seen = []
        threading.Thread(target=self.read, daemon=True).start()

    def read(self):
        for line in self.process.stdout:
            self.lines.put(line.strip())
        self.lines.put(None)

    def expect(self, *words, timeout=15):
        """Wait for an output line whose words after the time start with words."""
        deadline = time.monotonic() + timeout
        while True:
            for line in self.seen:
                if line.split()[1:1 + len(words)] == list(words):
                    self.seen.remove(line)
                    return line.split()
            try:
                line = self.lines.get(timeout=max(0.1, deadline - time.monotonic()))
            except queue.Empty:
                line = ""
            assert line is not None, "the arranger exited while waiting for %s" % (words,)
            if line:
                report("arranger: %s" % line)
                self.seen.append(line)
            assert time.monotonic() < deadline, "the arranger printed no %s" % (words,)

    def arrange(self, name, ms, entries, curve="smoothstep", wait=True):
        for entry in entries:
            self.send("entry %s %d %d %d %d %s %s %d" % entry)
        self.send("commit %s %d %s" % (name, ms, curve))
        self.expect("commit")
        if wait:
            self.send("wait")
            self.expect("landed")

    def send(self, command):
        self.process.stdin.write(command + "\n")
        self.process.stdin.flush()

    def kill(self):
        docker("rm", "-f", self.name)
        self.process.wait(timeout=30)


def entry(app_id, rect, opacity=1, fit="scale", delay=0):
    return (app_id,) + tuple(rect) + (str(opacity), fit, delay)


def wait_surface(control):
    surface = control.wait_event("surface").split()
    report("surface: %s" % " ".join(surface))
    assert surface[2] == CLAIM_SOCKET, "the surface came in as %s" % surface[2]
    return surface[1]


def wait_line(control, *words):
    line = control.wait_event(words[0]).split()
    report("control: %s" % " ".join(line))
    assert line[1:len(words)] == list(words[1:]), "%s, want %s" % (line, words)
    return line


def refused_on(display):
    result = subprocess.run(
        ["docker", "exec", "-e", "XDG_RUNTIME_DIR=/run/liken", WESTON,
         "liken-arrange-drill", display, "hold", "0"],
        capture_output=True, text=True,
    )
    report("an arranger on %s: exit %d, %s" % (display, result.returncode, result.stdout.strip()))
    return result


def place_all(control, ids, rect):
    for surface in ids:
        place(control, surface, rect, "none 0")
    control.expect_ok("order %s %s" % (CONNECTOR, " ".join(ids)))
    control.expect_ok("commit")


def main():
    control = connect(CONTROL)
    assert control.read_line() == "hello liken-layout 1"
    control.expect_ok("hello liken-operator 1")
    control.wait_event("output", timeout=5)
    control.expect_ok("listen %s %s" % (CLAIM_SOCKET, CONNECTOR))

    for display in ("wayland-0", CAPTURE_DISPLAY):
        result = refused_on(display)
        assert result.returncode == 3 and "refused not_a_claim" in result.stdout

    start_film("film", "ff0000")
    film = wait_surface(control)
    wait_line(control, "surface-app-id", film, "liken-film")
    start_film("other", "0000ff", "--app-id", "other")
    other = wait_surface(control)
    wait_line(control, "surface-app-id", other, "other")
    ids = [film, other]
    place_all(control, ids, FULL)

    arranger = Arranger("arranger")
    arranger.expect("granted")
    arranger.expect("region", str(WIDTH), str(HEIGHT))
    arranger.expect("surfaces", "liken-film", "1")
    arranger.expect("surfaces", "other", "1")
    wait_line(control, "arranger", CLAIM_SOCKET)

    second = refused_on(CLAIM_SOCKET)
    assert second.returncode == 3 and "refused busy" in second.stdout

    # The operator stacks other above the film. An arrangement that
    # names only the film puts it above other, which fills the region.
    arranger.arrange("small", 0, [entry("liken-film", TARGET)])
    wait_line(control, "arrangement", CLAIM_SOCKET, "1", "small")
    frame = capture(wait=0.5)
    assert_box(frame, RED, TARGET, "the named film")
    assert_filled(frame, (800, 0, 480, 720), BLUE, "the unnamed surface")

    # The order of the entries is the stacking order.
    a, b = (200, 100, 600, 400), (500, 300, 600, 400)
    overlap = (500, 300, 300, 200)
    arranger.arrange("film-on-top", 0, [entry("other", b), entry("liken-film", a)])
    assert_filled(capture(wait=0.5), overlap, RED, "the overlap, film last")
    arranger.arrange("other-on-top", 0, [entry("liken-film", a), entry("other", b)])
    assert_filled(capture(wait=0.5), overlap, BLUE, "the overlap, other last")

    # Opacity.
    arranger.arrange("dim", 0, [entry("other", (0, 0, 10, 10), 0),
                                entry("liken-film", FULL, 0.5)])
    r, g, bl = colour_at(capture(wait=0.5), 640, 360)
    report("the film at half opacity reads %d %d %d" % (r, g, bl))
    assert 100 <= r <= 160 and g < 30 and bl < 30, "half opacity reads %d %d %d" % (r, g, bl)

    # A commit partway through a tween starts from where the film shows.
    arranger.arrange("full", 0, [entry("other", (0, 0, 10, 10), 0),
                                 entry("liken-film", FULL)])
    arranger.arrange("shrink", 3000, [entry("other", (0, 0, 10, 10), 0),
                                      entry("liken-film", TARGET)], wait=False)
    first = box_of(capture(wait=1.2), RED)
    report("partway through the shrink, the film covers %s" % (first,))
    assert TARGET[2] < first[2] < WIDTH, "the film did not shrink partway"
    arranger.arrange("grow", 3000, [entry("other", (0, 0, 10, 10), 0),
                                    entry("liken-film", FULL)], wait=False)
    second_box = box_of(capture(wait=0), RED)
    report("just after the grow began, the film covers %s" % (second_box,))
    assert TARGET[2] < second_box[2] < WIDTH, "the film jumped at the second commit"
    assert abs(second_box[2] - first[2]) < 320, "the second commit did not start mid-tween"
    arranger.send("wait")
    arranger.expect("landed", "7")
    assert_box(capture(wait=0.5), RED, FULL, "the film after the grow")

    # A stagger: the film moves first, other waits its delay.
    low, high = (700, 400, 400, 300), (700, 50, 400, 300)
    arranger.arrange("apart", 0, [entry("liken-film", FULL), entry("other", low)])
    arranger.arrange("stagger", 1000, [entry("liken-film", TARGET),
                                       entry("other", high, delay=2500)],
                     curve="linear", wait=False)
    frame = capture(wait=1.3)
    assert_box(frame, RED, TARGET, "the film, landed before other moves")
    assert_box(frame, BLUE, low, "other, still waiting its delay")
    arranger.send("wait")
    arranger.expect("landed", "9")
    assert_box(capture(wait=0.5), BLUE, high, "other after its delay")

    # A resize entry: one configure when the entry lands, and the
    # client's pixels one to one once it draws at that size.
    start_film("panel", "00ff00", "--app-id", "panel", "--pattern")
    panel = wait_surface(control)
    ids.append(panel)
    place_all(control, ids, FULL)
    arranger.expect("surfaces", "panel", "1")
    time.sleep(1)
    before = drawn_sizes("panel")
    report("the panel drew at %s" % before)
    rect = (100, 100, 400, 300)
    arranger.arrange("panel", 800, [entry("panel", rect, fit="resize")], wait=False)
    time.sleep(0.4)
    during = drawn_sizes("panel")
    assert during == before, "the panel was configured during the tween: %s" % during
    arranger.send("wait")
    arranger.expect("landed")
    time.sleep(1)
    after = drawn_sizes("panel")
    report("after landing, the panel drew at %s" % after)
    assert after == before + ["400x300"], "the panel got %s" % after[len(before):]
    frame = capture(wait=0)
    x0, y0, w, h = rect
    blended = sum(1 for y in range(y0, y0 + h) for x in range(x0, x0 + w)
                  if colour_at(frame, x, y) not in ((0, 255, 0), (0, 0, 0)))
    report("the panel at its new size: %d of %d pixels blended" % (blended, w * h))
    assert blended == 0, "the panel is still scaled"
    assert colour_at(frame, x0, y0) != colour_at(frame, x0 + 1, y0), "no pattern"

    # A film built as mpv builds it: a solid backdrop and a picture.
    docker("rm", "-f", PREFIX + "film")
    wait_line(control, "surface-gone", film)
    start_film("picture", "404040,ff0000")
    film = wait_surface(control)
    # The film goes on top of the operator's stack, so it shows when
    # the arranger is gone and the operator's order holds again.
    ids = [other, panel, film]
    place_all(control, ids, FULL)
    arranger.arrange("picture", 600, [entry("other", (0, 0, 10, 10), 0),
                                      entry("panel", (0, 0, 10, 10), 0),
                                      entry("liken-film", TARGET)])
    frame = capture(wait=0.5)
    assert_box(frame, RED, (224, 270, 320, 180), "the picture")
    assert_filled(frame, (0, 0, 880, 170), GREY, "the backdrop over the region")
    assert_filled(frame, (720, 460, 240, 80), GREY, "the backdrop where the picture was")

    # The arranger crashes: everything tweens back to the default.
    arranger.kill()
    wait_line(control, "arranger-gone", CLAIM_SOCKET, "disconnected")
    frame = capture(wait=0.8)
    assert_box(frame, RED, (320, 180, 640, 360), "the picture after the arranger went")

    # A new arranger is granted, and hears the region's new size.
    arranger = Arranger("again")
    arranger.expect("granted")
    wait_line(control, "arranger", CLAIM_SOCKET)
    place_all(control, ids, (0, 60, WIDTH, 600))
    arranger.expect("region", str(WIDTH), "600")

    # An arranger that ends its input destroys its arranger, so the
    # module reports it as released. A new connection of the operator
    # gets every surface's app_id again.
    arranger.process.stdin.close()
    arranger.process.wait(timeout=30)
    wait_line(control, "arranger-gone", CLAIM_SOCKET, "released")
    replay = connect(CONTROL)
    assert replay.read_line() == "hello liken-layout 1"
    replay.expect_ok("hello liken-operator 1")
    replayed = {tuple(replay.wait_event("surface-app-id").split()[1:]) for _ in range(3)}
    report("the replay named %s" % sorted(replayed))
    assert replayed == {(film, "liken-film"), (other, "other"), (panel, "panel")}, replayed

    print("   PASS", flush=True)
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except AssertionError as failure:
        print("   FAIL: %s" % failure, flush=True)
        sys.exit(1)
