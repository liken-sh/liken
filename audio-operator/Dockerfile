# The image carries the operator and the daemons it runs. That pairing
# is the device operator pattern's whole reason for a separate
# repository: PipeWire and WirePlumber ship here, in a workload's
# image, and not in the read-only root that every liken machine boots.

FROM golang:1.26.5-bookworm AS build
WORKDIR /src
# The module files come first, so a source edit reuses the cached
# download layer.
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
# CGO_ENABLED=0 with -trimpath is liken's own build discipline: a
# static binary with no paths from the build machine in it.
RUN CGO_ENABLED=0 go build -trimpath -o /audio-operator .

FROM debian:stable-slim
# pipewire-bin carries the daemon and pw-dump, which is how the
# operator reads the graph. The daemon also creates every sink, from
# the node declarations the operator writes before it starts, because
# WirePlumber's ALSA monitor needs udev and a liken machine runs no
# udevd. wireplumber links each client's stream to the sink it names.
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        pipewire \
        pipewire-bin \
        wireplumber \
    && rm -rf /var/lib/apt/lists/*

# WirePlumber's profile is baked in, not mounted. It is a requirement
# of this operator rather than a choice a deployment makes: the pod
# manages every sound card its claim allocated, so every hardware
# monitor is off, including the ALSA one that finds nothing without
# udev. The file says why.
COPY config/50-audio-operator.conf /etc/wireplumber/wireplumber.conf.d/
#
# PipeWire's own configuration is not baked in. The operator generates
# it from the card it claimed and writes it into
# /etc/pipewire/pipewire.conf.d/ at every start, creating the directory
# if the image has none.

COPY --from=build /audio-operator /usr/local/bin/audio-operator

# The operator is PID 1. It starts PipeWire and WirePlumber as its own
# children and waits on them, so a daemon that dies ends the container
# and the kubelet restarts the whole set. See daemons.go.
ENTRYPOINT ["/usr/local/bin/audio-operator"]
