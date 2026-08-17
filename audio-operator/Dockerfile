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
# operator reads the graph. wireplumber selects each card's profile
# and creates the sinks. dbus is the bus those two look for:
# WirePlumber's device reservation speaks the system bus, and
# PipeWire's RTKit lookup falls back to it, so the image carries a bus
# for them to find.
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        dbus \
        pipewire \
        pipewire-bin \
        wireplumber \
    && rm -rf /var/lib/apt/lists/*

# WirePlumber's profile is baked in, not mounted. It is a requirement
# of this operator rather than a choice a deployment makes: the pod
# manages the one sound card its claim allocated, so every other
# hardware monitor is off. The file says why.
COPY config/50-audio-operator.conf /etc/wireplumber/wireplumber.conf.d/

COPY --from=build /audio-operator /usr/local/bin/audio-operator
COPY entrypoint.sh /usr/local/bin/entrypoint.sh

ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
