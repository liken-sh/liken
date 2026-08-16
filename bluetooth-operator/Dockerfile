# The image carries the operator and the daemon it runs. That pairing
# is the device operator pattern's whole reason for a separate
# repository: bluetoothd ships here, in a workload's image, and not in
# the read-only root that every liken machine boots.

FROM golang:1.26.5-bookworm AS build
WORKDIR /src
# The module files come first, so a source edit reuses the cached
# download layer.
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
# CGO_ENABLED=0 with -trimpath is liken's own build discipline: a
# static binary with no paths from the build machine in it.
RUN CGO_ENABLED=0 go build -trimpath -o /bluetooth-operator .

FROM debian:stable-slim
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        bluez \
        dbus \
    && rm -rf /var/lib/apt/lists/*

# AutoEnable is baked in, because an adapter that this operator holds
# must power itself on, and no deployment has a reason to say
# otherwise. input.conf is not here: the entrypoint writes it at start
# from BLUETOOTH_CLASSIC_BONDED_ONLY, because that setting is a
# security choice a person makes for one pairing session.
COPY config/main.conf /etc/bluetooth/

COPY --from=build /bluetooth-operator /usr/local/bin/bluetooth-operator
COPY entrypoint.sh /usr/local/bin/entrypoint.sh

ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
