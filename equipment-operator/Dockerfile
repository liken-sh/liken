# The operator's image: one static Go binary and nothing else. The
# operator holds the cluster credentials and writes every status, so
# its image carries no shell, no libc, and no tools, which is the
# least there is to attack.

FROM golang:1.27.0-bookworm AS build
WORKDIR /src
# The module files come first, so a source edit reuses the cached
# download layer.
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
# CGO_ENABLED=0 with -trimpath is liken's own build discipline: a
# static binary with no paths from the build machine in it. It runs
# from scratch, where there is no loader to need.
RUN CGO_ENABLED=0 go build -trimpath -o /equipment-operator .

FROM scratch
COPY --from=build /equipment-operator /equipment-operator
ENTRYPOINT ["/equipment-operator"]
