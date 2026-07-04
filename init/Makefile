# The init domain: liken itself, the only program here that isn't
# vendored. main.go opens with the story of what it is.

# CGO_ENABLED=0 forbids C bindings, which makes Go emit a fully static
# binary — no libc, no dynamic loader. That's not an optimization here,
# it's a requirement: the initramfs contains no libraries to load, and
# PID 1 has no one to ask for them. -trimpath keeps this machine's
# filesystem layout out of the binary, a small step toward reproducible
# builds.
dist/liken: go.mod go.sum $(wildcard *.go)
	CGO_ENABLED=0 go build -trimpath -o dist/liken .

.PHONY: all clean

all: dist/liken

clean:
	rm -rf dist
