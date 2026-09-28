# The redirect service's image: one static binary on scratch. It needs
# no CA certificates, because it makes no outbound connection.
FROM golang:1.27.0-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
COPY *.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /redirect .

FROM scratch
COPY --from=build /redirect /redirect
USER 65534:65534
EXPOSE 8080
ENTRYPOINT ["/redirect"]
