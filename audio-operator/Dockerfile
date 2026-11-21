# The image for every container of the audio-operator pod and of the
# audio-api Deployment. PipeWire and WirePlumber ship in this image, not
# in the read-only OS image that liken machines boot.

FROM golang AS build
# proxy.golang.org sometimes drops a module download partway through.
# With the default comma separator, go falls back to the origin only
# when the proxy returns 404 or 410, so a dropped download fails the
# build. With a pipe, go falls back to the origin after any proxy
# error, using the git in this image. GOSUMDB keeps its default, so
# the checksum database still verifies modules fetched from the origin.
ENV GOPROXY="https://proxy.golang.org|direct"
WORKDIR /src
COPY go.mod go.sum ./
# go.mod replaces the kubernetes module with ../kubernetes. The build
# copies the build context named kubernetes to /kubernetes, so
# ../kubernetes resolves from /src as it does from this component.
COPY --from=kubernetes go.mod go.sum /kubernetes/
RUN go mod download
COPY --from=kubernetes . /kubernetes/
COPY *.go ./
# -ldflags sets main.version, which the liken_build_info metric
# reports, so a running pod shows which release it was built from.
ARG VERSION=dev
# The binary is static, so it does not use the loader and libc that
# the closure ships.
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /audio-operator .
# The license files of the Go standard library and of each module the
# binary links. See notices/go.sh.
COPY --from=notices go.sh /
RUN CGO_ENABLED=0 sh /go.sh /notices .

# The packages come from the same Debian image and snapshot date as
# the base images. audio-closure.sh names module paths under
# pipewire-0.3, spa-0.2, and wireplumber-0.5, so a snapshot that moves
# one of those versions fails this build, which means the module list
# needs review. snapshot.sh and closure.sh come from vulkan/, passed
# as the build context named builder. SNAPSHOT is vulkan's version.
FROM debian AS closure
ARG SNAPSHOT
COPY --from=builder snapshot.sh snapshot.sha256 closure.sh /
# pipewire-bin provides pw-dump, which the operator runs to read the
# graph, and the other pw-* tools. wireplumber links each client's
# stream to the sink the client names, and provides wpctl.
#
# libspa-0.2-bluetooth provides the bluez5 SPA plugin and the A2DP
# codec plugins. The plugin registers the media endpoint with
# bluetoothd, receives the transport file descriptor, and encodes the
# audio, so Bluetooth playback needs nothing else from the host.
#
# flac and opus-tools provide the two encoders that the capture
# container pipes pw-record into. pw-record is pw-cat from
# pipewire-bin. libopus is already installed for the bluez5 codec
# plugin, so opus-tools only adds the opusenc encoder.
RUN sh /snapshot.sh "$SNAPSHOT" \
    && apt-get upgrade -y \
    && apt-get install -y --no-install-recommends \
        pipewire \
        pipewire-bin \
        wireplumber \
        libspa-0.2-bluetooth \
        flac \
        opus-tools \
    && rm -rf /var/lib/apt/lists/*
# closure.sh also writes the copyright file of each Debian package
# that supplied files to the closure, the list of those packages and
# their sources, and the dpkg record of each package, which syft and
# Trivy read.
COPY audio-closure.sh /
RUN sh /audio-closure.sh /out

FROM scratch
COPY --from=closure /out /

# The operator depends on this configuration, so it is part of the
# image and a deployment cannot change it. Each file gives its reasons.
# The files go under /usr/share because the pod mounts an emptyDir over
# /etc/pipewire/pipewire.conf.d and /etc/wireplumber/wireplumber.conf.d,
# which would hide anything placed there. WirePlumber reads /usr/share
# before /etc, so the declare container's fragment overrides these.
#
# The PipeWire node declarations and the fragment that enables
# WirePlumber's Bluetooth monitor are not in the image. The declare
# container writes them into those emptyDirs at pod start, from the
# cards and the media bus that the claim allocated.
COPY config/50-audio-operator.conf /usr/share/wireplumber/wireplumber.conf.d/
COPY config/51-access-rules.conf /usr/share/wireplumber/wireplumber.conf.d/
# The linking hook that 50-audio-operator.conf requires. WirePlumber
# finds a script by its path under /usr/share/wireplumber/scripts.
COPY config/restore-device-ports.lua /usr/share/wireplumber/scripts/liken/
COPY config/50-pipewire-access.conf /usr/share/pipewire/pipewire.conf.d/
COPY config/50-pipewire-log.conf /usr/share/pipewire/pipewire.conf.d/

COPY --from=build /audio-operator /audio-operator
COPY --from=build /notices /

# The pod's other containers set their own command or args. The image
# has no shell, so every command in a pod spec is a binary path.
ENTRYPOINT ["/audio-operator"]
