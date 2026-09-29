# The redirect service's image: one static binary on scratch, and the CA
# certificates it needs to reach Let's Encrypt when it serves HTTPS.
FROM golang:1.27.0-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /redirect .

FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /redirect /redirect
USER 65534:65534
EXPOSE 8080 8443
ENTRYPOINT ["/redirect"]
