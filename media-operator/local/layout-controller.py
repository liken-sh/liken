# The smallest thing that stands in for the display operator, for local/video.
#
# liken-layout.so places nothing on its own: a surface stays invisible until
# a controller states a rectangle for it. On a cluster that controller is the
# display operator, which needs Kubernetes. Here it is this script, which
# reproduces that operator's default layout: every surface over the whole
# screen, newest on top. The display's surface arrives after the film's, so
# the display draws over it.

import os
import socket
import sys
import time

PATH = os.environ.get("LIKEN_LAYOUT_SOCKET", "/sockets/layout.sock")


class Controller:
    def __init__(self, connection):
        self.connection = connection
        self.sequence = 0
        self.outputs = {}
        # Surface ids in the order the module reported them, which is
        # the order they arrived in.
        self.surfaces = []

    def send(self, line):
        self.sequence += 1
        self.connection.sendall(f"{self.sequence} {line}\n".encode())

    def output(self, words):
        self.outputs[words[0]] = (int(words[1]), int(words[2]))

    def place_all(self):
        if not self.outputs:
            return
        connector, (width, height) = next(iter(self.outputs.items()))
        for surface in self.surfaces:
            self.send(f"place {surface} {connector} 0 0 {width} {height} none 0")
        if self.surfaces:
            self.send(f"order {connector} {' '.join(str(s) for s in self.surfaces)}")
        self.send("commit")

    def line(self, line):
        words = line.split()
        if not words:
            return
        verb, rest = words[0], words[1:]
        if verb == "output":
            self.output(rest)
            self.place_all()
        elif verb == "surface":
            surface = int(rest[0])
            if surface not in self.surfaces:
                self.surfaces.append(surface)
            print(f"controller: surface {surface} on {rest[1]} at {rest[2]}x{rest[3]}", flush=True)
            self.place_all()
        elif verb == "surface-gone":
            surface = int(rest[0])
            if surface in self.surfaces:
                self.surfaces.remove(surface)
            self.place_all()
        elif verb == "error":
            print(f"controller: {line}", file=sys.stderr, flush=True)


def connect():
    # The compositor binds the control socket as it loads the module,
    # which is after this container may have started.
    for _ in range(120):
        connection = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        try:
            connection.connect(PATH)
            return connection
        except OSError:
            connection.close()
            time.sleep(0.5)
    raise SystemExit(f"nothing listens on {PATH}")


def main():
    connection = connect()
    controller = Controller(connection)
    controller.send("hello local-video 1")

    buffered = b""
    while True:
        chunk = connection.recv(4096)
        if not chunk:
            return
        buffered += chunk
        while b"\n" in buffered:
            line, buffered = buffered.split(b"\n", 1)
            controller.line(line.decode())


main()
