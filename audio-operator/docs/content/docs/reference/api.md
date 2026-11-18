---
title: API
weight: 50
toc: true
---

# API

The API streams the sound of a `Sink` or a `Source` over HTTP, as
WAV, FLAC, or Ogg Opus, so you can hear what a speaker plays or what a
microphone records. The [Listen guide](/docs/guides/listen/) uses it
through the `kubectl liken audio capture` command. A request that
streams sound is a tap. Kubernetes RBAC decides who may tap, and each
tap writes an `Event` on the `Sink` or `Source`.

`audio-api` is a `Deployment` in `liken-system`. It authenticates and
authorizes each request, finds the node that has the `Sink` or
`Source`, and forwards the request to the `capture` container of the
`audio-operator` pod on that node. That container records the sound
from PipeWire and encodes it. Neither process stores the sound.

| Item | Value |
| --- | --- |
| Service | `https://audio-api.liken-system.svc` |
| CA `ConfigMap` | `audio-api-ca` in `liken-system` |
| Discovery | `/v1/audio` |
| OpenAPI | `/v1/audio/openapi.json` |
| Shipped `ClusterRole` | `audio-capture-viewer` |
| Audit record | a `Captured` `Event` on the `Sink` or `Source` |
| Route reference | [Routes](/docs/reference/routes/) |

The display and media operators serve APIs of the same design for a
`Display` and a `Player`, with the same path layout and the same HTTP
behavior.

## Authentication

You identify yourself with a client certificate or with a bearer
token. The API checks for a certificate first and then for a token, in
the same order as the Kubernetes API server.

### Client certificate

When your TLS connection presents a client certificate that the
cluster's own certificate authority signed, the API identifies you as
the subject of that certificate. Your user name is the subject's
common name, and your groups are the subject's organization values.
The Kubernetes API server reads a client certificate the same way, so
the certificate in your kubeconfig identifies you here as it
identifies you to `kubectl`. The API reads the certificate authority
from the `ConfigMap` `extension-apiserver-authentication` in
`kube-system`, where the API server publishes it. The API watches that
`ConfigMap`, so a rotated authority takes effect without a restart. A
certificate from any other authority fails the TLS handshake.

### Bearer token

When the connection has no client certificate, the API reads a bearer
token from the `Authorization` header and validates it with a
`TokenReview` that requires the audience `audio-api`. Because of that
audience, a pod's ordinary `ServiceAccount` token, which has the API
server's audience, does not work here. An OIDC token from your
kubeconfig does not work either, because it has the audience of your
OIDC provider. In both cases, create a `ServiceAccount` token with
`--audience audio-api`, as [Examples](#examples) shows.

### Authorization

After the API identifies you, it sends a `SubjectAccessReview` for the
verb `get` on `sinks/audio` or `sources/audio` in the API group
`audio.liken.sh`. The review names the object and no namespace,
because both kinds are cluster-scoped. The info route of a `Sink` or
`Source`, such as `/v1/audio/sinks/{name}`, needs `get` on `sinks` or
`sources` itself. The discovery and OpenAPI documents need
authentication and no authorization. Every route authorizes the
request before it reads anything, so a `403` never reveals whether a
name exists.

### Grants

The operator ships the `ClusterRole` `audio-capture-viewer` for a
cluster owner to bind. It grants `get` on `sinks` and `sources` for
the info routes, and on `sinks/audio` and `sources/audio` for the
taps, so a person bound to it can use every route.

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: audio-capture-viewer
rules:
  - apiGroups: [audio.liken.sh]
    resources: [sinks, sources, sinks/audio, sources/audio]
    verbs: [get]
```

To bind the `ClusterRole` to a person, use `kind: User` with the
common name from their certificate, or `kind: Group` with one of its
organization values.

Because the taps are subresources, an owner can write a narrower rule.
This rule lets a person tap one `Sink` and nothing else. It does not
even let them read the info document of that `Sink`:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: kitchen-listener
rules:
  - apiGroups: [audio.liken.sh]
    resources: [sinks/audio]
    verbs: [get]
    resourceNames: [kitchen-pci-0000-00-1f-3-hdmi-0]
```

A role that grants `resources: ["*"]` in `audio.liken.sh` already
includes the taps, and so does `cluster-admin`.

RBAC on this API does not control every way to reach the sound. A
claim gives a pod the PipeWire socket, so a pod with any audio claim
on a node can capture that node's microphones and sinks with no RBAC
check and no `Event`. A grant on `sources` controls only this API.

A tap of a muted `Sink` still returns the sound. The tap reads the
monitor ports of the sink, which output the signal that the sink
receives, and `spec.mute` applies after those ports. So muting a
speaker silences the room and does not change what a tap returns. To
keep a person from hearing a speaker through this API, do not grant
them `sinks/audio` for it. A tap of a muted `Source` returns silence,
because the mute of a `Source` applies before the ports that the tap
reads.

Each tap posts a `Captured` `Event` on the `Sink` or `Source` when it
sends its first audio, so `kubectl describe sink` shows who listened
and when, while the tap still runs.

## Routes

Every path has the form
`/v1/{domain}/[namespaces/{ns}/]{plural}/{name}/{aspect}[.{ext}]`.
`domain` is the first label of the CRD's API group, so `audio.liken.sh`
gives `audio`. A namespaced kind has `namespaces/{ns}` before its
plural, as in Kubernetes. `Sink` and `Source` are cluster-scoped, so
their paths have no `namespaces/{ns}`. The domain is in the path so
that one ingress can serve the APIs of every domain under one host
name without a path clash. The operator ships no such ingress, and
each API is its own `Service`.

You choose the format with the extension of the path or with the
`Accept` header, and you limit the time span with a W3C Media
Fragments `t=` in the query. Any other parameter that changes only the
representation, such as the bitrate, also goes in the query. No route
accepts a request body, so the API never returns `415`.

| Method | Path | Response | Notes |
| --- | --- | --- | --- |
| GET, HEAD | `/v1/audio` | 200 `application/json` | The discovery document |
| GET, HEAD | `/v1/audio/openapi.json` | 200 `application/openapi+json` | OpenAPI 3.1 |
| GET, HEAD | `/v1/audio/sinks/{name}` | 200 `application/json` | The info document of the `Sink`: its format and links to its tap routes |
| GET, HEAD | `/v1/audio/sinks/{name}/audio` | 200, negotiated | The sound that the speakers are playing now. The default format is `audio/wav` |
| GET, HEAD | `/v1/audio/sinks/{name}/audio.wav` | 200 `audio/wav` | PCM in a RIFF WAVE stream |
| GET, HEAD | `/v1/audio/sinks/{name}/audio.flac` | 200 `audio/flac` | FLAC |
| GET, HEAD | `/v1/audio/sinks/{name}/audio.opus` | 200 `audio/ogg; codecs=opus` | Ogg Opus |
| GET, HEAD | `/v1/audio/sources/{name}` | 200 `application/json` | The info document of the `Source`: its format and links to its tap routes |
| GET, HEAD | `/v1/audio/sources/{name}/audio[.ext]` | as the sink routes above | The sound that the microphone is capturing, in the same formats |
| OPTIONS | any of the above | 204, no body | `Allow: GET, HEAD, OPTIONS` (RFC 9110 sections 9.3.7 and 10.2.1) |

`GET /v1/audio/sinks/{name}` returns the `node`, the `connectionType`,
the `rate` and `channels` that a tap uses, the `status.format` that
the `Sink` reports, the media types and extensions that the tap routes
serve, and a `Link: rel="related"` to each tap route. To build the
document, the API reads the `Sink` from the Kubernetes API server, and
the capture container on the node reads the rate and channel count
from the PipeWire graph. The info route of a `Source` returns the same
fields.

The [Routes](/docs/reference/routes/) page lists every route from the
OpenAPI document, with its parameters, responses, and fields. The API
generates the OpenAPI 3.1 document from the same route table that it
matches requests against, so the document lists exactly the routes
that the API serves. The document is at
[`/v1/audio/openapi.json`](/v1/audio/openapi.json). Its media type,
`application/openapi+json`, comes from
`draft-ietf-httpapi-rest-api-mediatypes`, so the registration is
provisional until that draft is published.

## Query parameters

| Parameter | Applies to | Values | Default | Rejected with `400` when |
| --- | --- | --- | --- | --- |
| `t` | every tap route | Media Fragments NPT: `t=begin,end`, `t=begin`, or `t=,end`. The interval is half-open, `[begin, end)` | none: the tap runs until you close the connection | the value does not match the grammar, `t=a,b` has `a >= b`, `begin` is over 60 seconds, or the dimension is repeated |
| `bitrate` | `audio` and `audio.opus` | the Opus bitrate in kbit/s per channel, 6 to 256 | `opusenc` chooses one from the sample rate | the request is for WAV or FLAC, or the value is outside 6 to 256 |

`t=0,5` streams five seconds and then ends the stream. `t=5,7`
discards the first five seconds and then streams two. With no `end`,
or with no `t=` at all, the tap runs until you close the connection.
`audio.opus?bitrate=128` streams Opus at 128 kbit/s.

`t=` is a query parameter because Media Fragments section 3.1 says
that a URI query produces a new resource, and section 7.4 says that a
query can change the media type. The parser follows section 5.1.1: it
splits on `&` and `=` first and percent-decodes afterwards, so
`t=10%2C20`, `t=%6ept:10`, and `t=npt%3a10` all parse (section
6.1.1).

Media Fragments puts the zero of NPT at the start of the source media
(section 6.1.1). A live capture has no start, so this API puts zero at
the moment that the capture container accepts the request. The
`clock:` format would state that directly, but it is in the advanced
Media Fragments document and not in version 1.0. The capture
container sends the response headers immediately, starts `pw-record`
immediately, discards samples until `begin` on its own clock, and
stops the encoder at `end`. So the first sample of the body was
recorded at the request time plus `begin`, when `pw-record` and the
encoder started before `begin` had passed.

Media Fragments tells a user agent to ignore an invalid, unknown, or
non-existent dimension (sections 6.2, 6.2.1, 6.3.1). This API returns
`400` instead, because a query produces a new resource, and a client
that asked for a span must not silently receive something else.

## Content negotiation

An extension selects one fixed format. When the path has no extension,
the API chooses a format from the `Accept` header and its q-values
(RFC 9110 section 12.5.1). When two formats have equal q-values, the
API picks the first in its own order: WAV, FLAC, Opus. A request with
no `Accept` header gets `audio/wav`.

The responses to `GET /v1/audio/sinks/kitchen/audio`:

| Request | Response |
| --- | --- |
| `Accept` absent, `*/*`, or `audio/*` | `200`, `audio/wav`, `Content-Location: /v1/audio/sinks/kitchen/audio.wav` |
| `Accept: audio/ogg; codecs=opus, audio/flac;q=0.5` | Opus, the higher q |
| `Accept: audio/flac;q=0.5, audio/ogg;q=0.5` | FLAC, the server's order at equal q |
| `Accept: audio/wav;q=0, */*` | FLAC: `q=0` excludes WAV, and the wildcard admits the rest |
| `Accept: audio/mpeg` | `406`, and `acceptable` lists the media types of the route, each with its `href` |

On `audio.wav`, `Accept: audio/flac` returns a `406` that lists
`audio.wav`, `audio.flac`, and `audio.opus`. `Accept: audio/vnd.wave`
selects WAV.

| Format | Media type | Aliases | Standard |
| --- | --- | --- | --- |
| RIFF WAVE | `audio/wav` | `audio/wave`, `audio/x-wav`, `audio/vnd.wave` | RFC 2361 for `audio/vnd.wave`, WHATWG MIME Sniffing section 6.2 for `audio/wave` |
| FLAC | `audio/flac` | `audio/x-flac`, deprecated | RFC 9639 section 12.1 |
| Ogg Opus | `audio/ogg; codecs=opus` | `audio/ogg` | RFC 5334 for `audio/ogg` and its `codecs` parameter, RFC 7845 section 9 for `opus` and the `.opus` extension |

The IANA registry names RIFF WAVE only as `audio/vnd.wave`, in RFC
2361, an informational RFC from 1998. No browser, `ffmpeg`, or `mpv`
sends that name. The WHATWG MIME Sniffing Standard calls the RIFF
signature `audio/wave`. Clients send `audio/wav`, so the API sends
that.

## Response headers

Every response has these headers:

| Header | Value | Standard |
| --- | --- | --- |
| `Content-Type` | the media type of the body | RFC 9110 |
| `Vary` | `Accept`, on every response, extension routes included | RFC 9110 section 12.5.5 |
| `Link` | `rel="service-desc"` to the OpenAPI document, and `rel="service-doc"` to this page | RFC 8631 |
| `Link` | on a response about one object: `rel="describedby"` with the absolute URL of the object in the Kubernetes API | RFC 8288 section 3.1 |

The discovery document, the OpenAPI document, and an info document
each have an `ETag` and `Cache-Control: no-cache`, and a request with
a matching `If-None-Match` gets a `304` (RFC 9110 section 8.8.3, RFC
9111 section 5.2.2.4). The `ETag` is the build version, because the
route table is compiled into the binary. An info document also has a
`rel="related"` link (RFC 4287) to each of its tap routes. A tap has
these headers instead:

| Header | Value | Standard |
| --- | --- | --- |
| `Cache-Control` | `no-store` | RFC 9111 section 5.2.2.5 |
| `Accept-Ranges` | `none` | RFC 9110 section 14.3 |
| `X-Content-Type-Options` | `nosniff` | [Fetch Standard section 3.6](https://fetch.spec.whatwg.org/#x-content-type-options-header) |
| `Content-Disposition` | `inline; filename="<name>-<time>.<ext>"` | RFC 6266 |
| `Transfer-Encoding` | `chunked`, on HTTP/1.1 | RFC 9112 section 7.1 |
| `Content-Location` | on the negotiated route only: the absolute path of the extension route that was served | RFC 9110 section 8.7 |
| `Link` | on the route with no extension: one `rel="alternate"` per fixed format | RFC 8288 |

`Content-Location` names the extension route of the format that
negotiation chose, which RFC 9110 section 8.7 calls "a more specific
identifier for the selected representation". The same section says
that a `GET` on that URL returns the same representation. That is
never true of a live capture, because a second request records new
sound, so do not read the header that way. `Accept-Ranges: none`
follows from the same fact: a live capture has no fixed bytes, and
byte 44 of two requests comes from two different moments.

`X-Content-Type-Options: nosniff` tells a browser to use the
`Content-Type` and not to guess a type from the bytes, so a browser
that opens a tap never renders it as a page.

`Vary: Accept` is on the extension routes as well, because `Accept`
can turn a `200` into a `406` there. RFC 9110 section 12.5.5 gives a
second reason for `Vary`: it tells the recipient that the response
was subject to negotiation. The `describedby` URL is absolute because
a relative reference would resolve against this API and not against
the Kubernetes API server.

`Content-Disposition` gives a browser the file name to save as. The
time in the name is RFC 3339 UTC with the colons replaced by dashes,
for example `kitchen-usb-0573-1573-a34004801402-usb-audio-2026-09-16T21-02-16Z.wav`,
because not every file system that a browser saves to allows a colon in
a file name.

The `type` attribute of a link has no media type parameters (RFC 8288
section 3.4.1), so the link to the Opus route has the type
`audio/ogg`. The API uses only registered relations. This example is
wrapped for reading, and the API sends it as one header line:

```
Link: <https://kubernetes.default.svc/apis/audio.liken.sh/v1alpha1/sinks/kitchen>; rel="describedby",
      </v1/audio/sinks/kitchen/audio.flac>; rel="alternate"; type="audio/flac",
      </v1/audio/sinks/kitchen/audio.opus>; rel="alternate"; type="audio/ogg"
```

`HEAD` returns the same headers as `GET`. It records no sound and
makes no request to the capture container (RFC 9110 section 9.3.2). A
`HEAD` response to an error has no body (section 15.5).

## Errors

Every error is an RFC 9457 problem document with the media type
`application/problem+json`. `type` is a URL. When `type` is
`about:blank`, `title` is the HTTP status phrase (section 4.2.1).
`instance` is the request path, a `#`, and the request ID. The log
line that the API writes for the request has the same ID. `detail`
contains the error text of the part that failed, word for word: the
`stderr` of `pw-record`, the `stderr` of an encoder, or the
`status.message` from the API server. A `406` also has an
`acceptable` list of `{"type", "href"}` objects. On an extension
route, the list has that route's own type and the types of the other
extension routes, with their URLs (RFC 9110 section 15.5.7).

| Status | When | Headers | From |
| --- | --- | --- | --- |
| `200` document | `/v1/audio`, `openapi.json`, or an info route | see [Response headers](#response-headers) | RFC 9110 sections 8.8.3 and 12.5.5, RFC 9111 section 5.2.2.4 |
| `304` | `If-None-Match` matches a document's `ETag` | `ETag`, `Vary: Accept` | RFC 9110 sections 13.1.2 and 15.4.5 |
| `200` tap | a tap is running | see [Response headers](#response-headers) | RFC 9110, RFC 9111 section 5.2.2.5, RFC 9112 section 7.1, RFC 6266, RFC 8288 |
| `204` | `OPTIONS` | `Allow: GET, HEAD, OPTIONS` | RFC 9110 section 10.2.1 |
| `400` | a `t=` that does not match the grammar, `t=a,b` with `a >= b`, a begin over 60 seconds, a repeated dimension, an unknown query parameter, or a parameter that the format does not accept | `application/problem+json` | RFC 9457. Media Fragments says to ignore these, and the API refuses them for the reason given under [Query parameters](#query-parameters) |
| `401` | no client certificate and no token: `WWW-Authenticate: Bearer realm="audio-api"`. A token that the `TokenReview` refuses: the same header with `error="invalid_token"` and an `error_description` that contains the review's message | `WWW-Authenticate` | RFC 9110 section 15.5.2, RFC 6750 section 3 |
| `403` | the `SubjectAccessReview` denied the request | `WWW-Authenticate: Bearer realm="audio-api", error="insufficient_scope", scope="sinks/audio"` | RFC 9110 section 15.5.4, RFC 6750 section 3.1 |
| `404` | no `Sink` or `Source` has that name, or PipeWire has no node for it | `application/problem+json` | RFC 9110 section 15.5.5 |
| `405` | a method other than GET, HEAD, or OPTIONS | `Allow` | RFC 9110 section 15.5.6 |
| `406` | `Accept` excludes every representation the route can serve | `application/problem+json` with `acceptable` | RFC 9110 sections 12.5.1 and 15.5.7 |
| `409` | the object is away, which means it has no `status.node`. `detail` tells you to power on or connect the device | `application/problem+json`, type `away` | RFC 9110 section 15.5.10 |
| `500` | the tap connected to a node other than the one that you asked for | `application/problem+json`, type `wrong-target` | RFC 9110 section 15.6.1 |
| `502` | the capture container returned a response that is not HTTP, or an error response that is not a problem document | `application/problem+json` | RFC 9110 section 15.6.3 |
| `503` | the API could not read the object from the API server, the node has no Ready capture container, the capture container is at its tap limit, refused the connection, has no certificate yet, or could not read the PipeWire graph, or PipeWire refused `pw-record` | `Retry-After: 5`, `application/problem+json` | RFC 9110 sections 15.6.4 and 10.2.3 |
| `504` | the capture container sent no response headers within the header timeout | `application/problem+json` | RFC 9110 section 15.6.5 |

The API returns `409` only when you can fix the cause, and `detail`
says how.

`wrong-target` is specific to this API. The display and media APIs
return the other problem types with the same meaning.

| Type URI | Meaning | Status |
| --- | --- | --- |
| `https://liken.sh/problems/no-node` | no `Sink` or `Source` has that name, or PipeWire has no node for it | `404` |
| `https://liken.sh/problems/not-acceptable` | `Accept` excludes every representation the route can serve | `406` |
| `https://liken.sh/problems/capture-busy` | the capture container is at its tap limit | `503` |
| `https://liken.sh/problems/upstream-failed` | the capture container returned a response that this API cannot pass on, or sent no response headers in time | `502`, `504` |
| `https://liken.sh/problems/away` | the object is away, which means it has no `status.node` | `409` |
| `https://liken.sh/audio/problems/wrong-target` | the tap connected to a node other than the one that you asked for | `500` |

## Discovery

`GET /v1/audio` returns the discovery document, which has the same
shape in the audio, display, and media capture APIs. The templates
follow RFC 6570: `{name}` is simple expansion (section 3.2.2), `{.ext}`
is label expansion (3.2.5), and `{?t,bitrate}` is form-style query
expansion (3.2.8). Form-style expansion percent-encodes the comma in
`t=5,7`, and the parser accepts that form (see
[Query parameters](#query-parameters)).

```json
{
  "resources": {
    "sinks": {
      "self": "/v1/audio/sinks/{name}",
      "aspects": {
        "audio": {
          "template": "/v1/audio/sinks/{name}/audio{.ext}{?t,bitrate}",
          "mediaTypes": ["audio/wav", "audio/flac", "audio/ogg"],
          "extensions": ["wav", "flac", "opus"],
          "redirects": false
        }
      }
    },
    "sources": {
      "self": "/v1/audio/sources/{name}",
      "aspects": {
        "audio": {
          "template": "/v1/audio/sources/{name}/audio{.ext}{?t,bitrate}",
          "mediaTypes": ["audio/wav", "audio/flac", "audio/ogg"],
          "extensions": ["wav", "flac", "opus"],
          "redirects": false
        }
      }
    }
  },
  "openapi": "/v1/audio/openapi.json"
}
```

## Examples

The `kubectl liken audio capture` command makes this request for you,
as the [Listen guide](/docs/guides/listen/) shows. The commands below
make the same request with `kubectl` and `curl`, through a
port-forward. They work the same way through an ingress that the
cluster owner sets up.

```sh
kubectl -n liken-system create serviceaccount listener
kubectl create clusterrolebinding listener --clusterrole audio-capture-viewer \
    --serviceaccount liken-system:listener
kubectl -n liken-system port-forward svc/audio-api 8443:443 &
kubectl -n liken-system get configmap audio-api-ca -o jsonpath='{.data.ca\.crt}' > ca.crt
curl --cacert ca.crt --resolve audio-api.liken-system.svc:8443:127.0.0.1 \
    -H "Authorization: Bearer $(kubectl -n liken-system create token listener --audience audio-api --duration 10m)" \
    "https://audio-api.liken-system.svc:8443/v1/audio/sinks/kitchen-pci-0000-00-1f-3-hdmi-0/audio.wav?t=0,5" \
    -o kitchen.wav
```

If your kubeconfig has a client certificate, send that instead, and
you need no `ServiceAccount` and no token. The port-forward is a TCP
tunnel, so the TLS handshake runs between `curl` and the API, and the
certificate reaches the API unchanged.

```sh
kubectl config view --raw --minify \
    -o jsonpath='{.users[0].user.client-certificate-data}' | base64 -d > client.crt
kubectl config view --raw --minify \
    -o jsonpath='{.users[0].user.client-key-data}' | base64 -d > client.key
curl --cacert ca.crt --cert client.crt --key client.key \
    --resolve audio-api.liken-system.svc:8443:127.0.0.1 \
    "https://audio-api.liken-system.svc:8443/v1/audio/sinks/kitchen-pci-0000-00-1f-3-hdmi-0/audio.wav?t=0,5" \
    -o kitchen.wav
```

From a pod on the cluster network, the same requests work against
`https://audio-api.liken-system.svc/v1/audio/...` with no
port-forward and no `--resolve`.

To listen live, give the URL to `mpv` in place of `curl -o`.

The API has no CORS support, so a browser can reach it only from the
same origin, through a port-forward.

## Metrics

The API and the capture container each serve `/healthz`, `/readyz`,
and `/metrics`, and each exports `liken_build_info`. The API listens
on port 9200 in its own pod, and the capture container listens on port
9201 in the `audio-operator` pod. The `route` label is the RFC 6570
template and never the concrete path, so no `Sink` or `Source` name
appears in a label.

| Process | Metric | Type |
| --- | --- | --- |
| audio-api | `audio_api_requests_total{route,method,status}` | counter |
| audio-api | `audio_api_request_seconds{route}` | histogram, seconds until the response headers |
| audio-api | `audio_api_streams_active{aspect}` | gauge |
| audio-api | `audio_api_certificate_expiry_seconds` | gauge |
| audio-capture | `audio_capture_ready` | gauge |
| audio-capture | `audio_capture_bytes_total{aspect,format}` | counter |
| audio-capture | `audio_capture_seconds_total{aspect,format}` | counter, seconds of sound streamed |
| audio-capture | `audio_captures_active{aspect}` | gauge |
| audio-capture | `audio_capture_failures_total{reason}` | counter: `connect`, `target`, `wrong-target`, `encoder`, `limit`, `certificate` |

Only the capture container exports the `audio_capture` counters, so
each tap is counted once.
