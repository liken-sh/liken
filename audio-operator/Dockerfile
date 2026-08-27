# One image, and the pod runs it four times: the declaration that
# runs before PipeWire, the two daemons, and the operator. The daemons
# ship here, in a workload's image, and not in the read-only root that
# every liken machine boots. That pairing is the device operator
# pattern's whole reason for a separate repository.

FROM golang:1.27.0-bookworm AS build
WORKDIR /src
# The module files come first, so a source edit reuses the cached
# download layer.
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
# CGO_ENABLED=0 with -trimpath is liken's own build discipline: a
# static binary with no paths from the build machine in it. The binary
# runs beside a closure that holds a loader and a libc, and it needs
# neither.
RUN CGO_ENABLED=0 go build -trimpath -o /audio-operator .

# The suite is pinned because the closure script names pipewire 0.3,
# spa 0.2, and wireplumber 0.5. A Debian that moves one of them fails
# this build, which is the report that the module set needs reading
# again.
FROM debian:trixie-slim AS closure
# pipewire-bin provides the daemon, pw-dump, which is how the operator
# reads the graph, and pw-cli's family. The daemon creates every sink,
# from the node declarations the declare container writes before it
# starts, because WirePlumber's ALSA monitor needs udev and a liken
# machine runs no udevd. wireplumber links each client's stream to the
# sink it names, and provides wpctl.
#
# libspa-0.2-bluetooth carries the bluez5 SPA plugin and the A2DP
# codec plugins beside it. The plugin registers the media endpoint
# with bluetoothd, holds the transport descriptor, and encodes the
# samples, so Bluetooth playback takes nothing else from the host.
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        pipewire \
        pipewire-bin \
        wireplumber \
        libspa-0.2-bluetooth \
    && rm -rf /var/lib/apt/lists/*
COPY audio-closure.sh /
RUN sh /audio-closure.sh /out

FROM scratch
COPY --from=closure /out /

# WirePlumber's profile is baked in, not mounted. It is a requirement
# of this operator rather than a choice a deployment makes: the pod
# manages every sound card its claim allocated, so every hardware
# monitor is off, including the ALSA one that finds nothing without
# udev. The file says why.
#
# Both files go under /usr/share, because the pod mounts an emptyDir
# over /etc/wireplumber/wireplumber.conf.d and that mount would hide
# anything placed there. WirePlumber merges the fragments of
# /usr/share first and /etc second, so the declare container's
# fragment in the emptyDir is read after these two and overrides
# them.
COPY config/50-audio-operator.conf /usr/share/wireplumber/wireplumber.conf.d/
# The access rule beside it stops WirePlumber's default policy from
# restricting this pod's clients, itself included. The file says why.
COPY config/51-access-rules.conf /usr/share/wireplumber/wireplumber.conf.d/

# PipeWire's access mode is baked in for the same reason. It goes
# under /usr/share because the pod mounts an emptyDir over
# /etc/pipewire/pipewire.conf.d, and that mount would hide anything
# placed there. The file says why the legacy mode cannot work in a
# pod.
COPY config/50-pipewire-access.conf /usr/share/pipewire/pipewire.conf.d/
#
# PipeWire's node declarations are not baked in. The declare init
# container generates them from the cards the claim allocated and
# writes them into /etc/pipewire/pipewire.conf.d/, an emptyDir the
# PipeWire container mounts at the same path.
#
# WirePlumber's Bluetooth monitor is not baked in either. The same
# declare container writes the fragment that turns it on into
# /etc/wireplumber/wireplumber.conf.d/, and only when the claim
# delivered a Bluetooth media bus.

COPY --from=build /audio-operator /usr/local/bin/audio-operator

# The operator is the default entrypoint, and the three other
# containers name their own program. The image holds no shell, so
# every command in the pod spec is a binary path.
ENTRYPOINT ["/usr/local/bin/audio-operator"]
