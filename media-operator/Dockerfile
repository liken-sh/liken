# The operator's image: one static Go binary and nothing else. The
# operator holds the cluster credentials and writes every status, so
# its image carries no shell, no libc, and no tools, which is the
# least there is to attack. The player image, mpv under its
# supervisor, is a separate build that arrives with plan 01.

FROM golang:1.26.5-bookworm AS build
WORKDIR /src
# The module file comes first, so a source edit reuses the cached
# download layer.
COPY go.mod ./
RUN go mod download
COPY *.go ./
# CGO_ENABLED=0 with -trimpath is liken's own build discipline: a
# static binary with no paths from the build machine in it. It runs
# from scratch, where there is no loader to need.
RUN CGO_ENABLED=0 go build -trimpath -o /media-operator .

FROM scratch
COPY --from=build /media-operator /media-operator
ENTRYPOINT ["/media-operator"]
