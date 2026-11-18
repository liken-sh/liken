---
title: API
weight: 50
toc: true
---

# The screen over HTTP

You can read what a `Display` shows with a plain HTTP `GET`: one
frame as PNG or JPEG, a clip as H.264 in fragmented MP4, or an MJPEG
stream. The API stores nothing: each request starts a new capture,
and the API streams it to you as it is encoded. The
[screenshot guide](/docs/guides/screenshot/) shows how to use it.

Two processes handle each request. `display-api` is a `Deployment` in
`liken-system`. It authenticates you, authorizes the request, reads
the `Display` to find its node, and streams the response back. The
capture container in the `display-operator` pod on that node takes
the frames from the compositor and encodes them on the node's GPU.

| Item | Value |
| --- | --- |
| Service | `https://display-api.liken-system.svc` |
| CA `ConfigMap` | `display-api-ca` in `liken-system` |
| Discovery | `/v1/display` |
| OpenAPI | `/v1/display/openapi.json` |
| Shipped `ClusterRole` | `display-capture-viewer` |
| Audit record | a `Captured` `Event` on the `Display` |
| Route reference | [Routes](/docs/reference/routes/) |

The audio and media operators have the same kind of API for a
`Sink`, a `Source`, and a `Player`, with the same path layout, HTTP
behavior, and standards.

## Authentication

You identify yourself with a client certificate or a Bearer token.
The API looks for a certificate first and then for a token, in the
same order as the Kubernetes API server.

### Client certificate

If your TLS connection presents a client certificate signed by the
cluster's own certificate authority, the API identifies you as the
subject of that certificate. Your user name is the subject's common
name, and your groups are the subject's organization values. The
Kubernetes API server reads a client certificate the same way, so the
credentials in your kubeconfig identify you here as they do to
`kubectl`. The API reads the certificate authority from the
`ConfigMap` `extension-apiserver-authentication` in `kube-system`,
where the API server publishes it. It watches that `ConfigMap`, so
it uses a rotated authority without a restart.
A certificate from any other authority fails the TLS handshake.

### Bearer token

If the connection has no client certificate, the API looks for a
Bearer token (RFC 6750 section 2.1). It sends the token in a
`TokenReview` with the audience `display-api`, and it checks that the
audiences in the review result include `display-api`. A pod's default
`ServiceAccount` token does not have that audience, so the API refuses
it. If your kubeconfig uses OIDC, you have no client certificate, so
create a `ServiceAccount` token for the `display-api` audience, as the
recipe under [Examples](#examples) does.

### Authorization

After the API identifies you, it sends a `SubjectAccessReview` for the
verb `get` on the resource `displays/screen` in the API group
`display.liken.sh`, with the name of the `Display`. No CRD defines
`displays/screen`. It is only a resource name that RBAC rules match,
like `pods/log`. The discovery and OpenAPI documents need
authentication and no authorization. The info route needs `get` on
`displays`. Every route authorizes the request before it reads
anything, so a 403 does not show whether a `Display` with that name
exists.

### Grants

The base manifests include a `ClusterRole` named
`display-capture-viewer` with both rules: `displays/screen` for the
capture routes and `displays` for the info route. The manifests do not
bind it to anyone. A cluster owner binds it, for example to a
`ServiceAccount`:

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: display-viewer
  namespace: liken-system
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: display-viewer
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: display-capture-viewer
subjects:
  - kind: ServiceAccount
    name: display-viewer
    namespace: liken-system
```

To bind the same `ClusterRole` to a person, use `kind: User` with the
common name from their certificate, or `kind: Group` with one of its
organization values.

A role that grants `resources: ["*"]` on `display.liken.sh` includes
`displays/screen`, and so does `cluster-admin`. Every subject with one
of those roles can capture every screen.

`pods/exec` in `liken-system` also lets a subject capture a screen.
The containers of the `display-operator` pod share one process
namespace, so a shell in that pod can reach the capture socket.

The API writes a `Captured` `Event` on the `Display` for every request
that returned data. The message names the subject and the aspect, so
`kubectl describe display` shows who captured a screen and when.

## Routes

Every path has the form
`/v1/{domain}/[namespaces/{ns}/]{plural}/{name}/{aspect}[.{ext}]`.
`domain` is the first label of the CRD's API group, `plural` is the
CRD's plural name, and `aspect` is what you capture. A namespaced kind
has `namespaces/{ns}` before its plural. A `Display` is
cluster-scoped, so its paths have no namespace. Each domain has its
own `Service`, and the domain segment lets one ingress serve all of
them under one host name without paths that conflict.

| Method | Path | Response | Notes |
| --- | --- | --- | --- |
| GET, HEAD | `/v1/display` | 200 `application/json` | The discovery document |
| GET, HEAD | `/v1/display/openapi.json` | 200 `application/openapi+json` | OpenAPI 3.1 |
| GET, HEAD | `/v1/display/displays/{name}` | 200 `application/json` | Size, scale, refresh, formats, and links. A screen that is down still returns what the `Display` reports |
| GET, HEAD | `/v1/display/displays/{name}/screen` | 200, negotiated | Default `image/png` |
| GET, HEAD | `/v1/display/displays/{name}/screen.png` | 200 `image/png` | One frame |
| GET, HEAD | `/v1/display/displays/{name}/screen.jpg` | 200 `image/jpeg` | One frame |
| GET, HEAD | `/v1/display/displays/{name}/screen.mp4` | 200 `video/mp4; codecs="avc1.640029"` | H.264 in fragmented MP4, until you hang up or the `t=` end |
| GET, HEAD | `/v1/display/displays/{name}/screen.mjpeg` | 200 `multipart/x-mixed-replace; boundary=ffmpeg` | One `image/jpeg` part per frame |
| OPTIONS | any of the above | 204, no body | `Allow: GET, HEAD, OPTIONS` |

The [OpenAPI 3.1 document](/docs/reference/openapi.json) is generated
from the API's router, so it lists every route above. The
[Routes](/docs/reference/routes/) page renders that document with the
parameters, responses, and fields of each route. The media type
`application/openapi+json` is a draft
(draft-ietf-httpapi-rest-api-mediatypes) and is not registered. The
copy on this site comes from a development build, so its
`info.version` is `dev`. A release serves its own version.

## Query parameters

The API validates every parameter before it starts a capture.

| Parameter | Applies to | Values | Default | Rejected with 400 when |
| --- | --- | --- | --- | --- |
| `t` | every capture route | Media Fragments NPT: `t=begin,end`, `t=begin`, or `t=,end` | none: a clip or a stream runs until you hang up | `t=a,b` with `a >= b`, a begin over 60 s, or an end on a still |
| `xywh` | every capture route | `pixel:` or `percent:`, then `x,y,w,h` | `pixel:`, and the whole screen | the origin is at or past an edge of the screen |
| `width` | every capture route | the width in pixels, after the crop | none: the region's own width | `width` together with `height`, or a value larger than the source |
| `height` | every capture route | the height in pixels, after the crop | none: the region's own height | `height` together with `width`, or a value larger than the source |
| `framerate` | a clip or a stream: `screen.mp4`, `screen.mjpeg` | frames per second, at most the output's refresh rate | 15 | a value above the refresh rate, or on a still |
| `quality` | JPEG output: `screen.jpg`, `screen.mjpeg` | the JPEG quality, 1 to 100 | 85 | on PNG or MP4 |

`width=` and `height=` are parameters of this API, not of Media
Fragments. Either one scales the region down after the crop and keeps
the aspect ratio.

`t=` and `xywh=` follow W3C Media Fragments 1.0 (Recommendation,
25 September 2012), sections 4.2.1 and 4.2.2. You write them in the
query and not in the URL fragment. Section 3.1 says "a URI query
produces a new resource, while a URI fragment provides a secondary
resource", and section 7.4 says that with the query form "media type
changes are possible", which a capture needs. The parser follows
section 5.1.1. It
splits on `&` and `=` first and percent-decodes second, so `t=10%2C20`
and `t=npt%3a10` mean the same as `t=10,20` and `t=npt:10`.

In Media Fragments, `t=0` is the start of the source media. A live
screen has no start, so this API sets zero at the time the capture
container accepts the request. The container sends the response
headers and starts its encoder immediately, and it discards frames
until the begin time, which it measures on its own clock. `t=5,7`
discards five seconds and then records two. `t=,10` records ten
seconds from now. `t=5` on a still waits five seconds and then takes
one frame.

Media Fragments tells a user agent to ignore an invalid, unknown, or
non-existent dimension. This API returns 400 instead, because a
client that asked for a region must not get the whole screen without
warning. A repeated dimension and an unknown key also return 400. A
region that extends past an edge is clipped to the screen (section
6.1.2), and the API does not refuse it. With `percent:`, the origin
rounds down and the size rounds up, and then the width and height
round up to an even number because the encoder needs one. `pixel:`
counts the physical pixels of the frame. If you work in logical
pixels, multiply by the `scale` that the info document reports.

## Content negotiation

A path with an extension selects one representation. If your `Accept`
header excludes that representation, the API returns 406. RFC 9110
section 12.5.1 lets a server return 406 or ignore `Accept`, and this
API returns 406 so that you learn when the path and the header
contradict each other. A path with no extension is negotiated on
`Accept` with q-values, as section 12.5.1 describes. With no `Accept`
header, any type is acceptable, so you get the default, `image/png`.
When types tie or `Accept` has a wildcard, the API picks the first
that matches in this order: `image/png`, `image/jpeg`, `video/mp4`,
`multipart/x-mixed-replace`.

| Request | Response |
| --- | --- |
| `GET screen` | 200 `image/png`, `Content-Location: /v1/display/displays/{name}/screen.png` |
| `GET screen`, `Accept: image/jpeg` | 200 `image/jpeg` |
| `GET screen`, `Accept: image/*` | 200 `image/png`, the first image type in the order |
| `GET screen`, `Accept: video/mp4, image/png;q=0.5` | 200 `video/mp4`, a stream |
| `GET screen`, `Accept: image/png;q=0, */*` | 200 `image/jpeg` |
| `GET screen`, `Accept: audio/wav` | 406, and `acceptable` lists the four types |
| `GET screen.png`, `Accept: image/*` | 200 `image/png` |
| `GET screen.png`, `Accept: video/mp4` | 406, and `acceptable` lists `screen.png` and its three siblings |

## Response headers

Every response has these headers:

| Header | Value | Standard |
| --- | --- | --- |
| `Content-Type` | the media type of the body | RFC 9110 section 8.3 |
| `Date` | the time of the response | RFC 9110 section 6.6.1 |
| `Vary` | `Accept` | RFC 9110 section 12.5.5 |
| `Link` | `</v1/display/openapi.json>; rel="service-desc"` | RFC 8288, RFC 8631 |
| `Link` | `<https://liken.sh/display/docs/reference/api/>; rel="service-doc"` | RFC 8288, RFC 8631 |
| `Link` | on a route about one `Display`: `<https://kubernetes.default.svc/apis/display.liken.sh/v1alpha1/displays/{name}>; rel="describedby"` | RFC 8288 section 3.1 |

`Vary: Accept` is also on the extension routes, where `Accept` only
decides between 200 and 406, because section 12.5.5 also uses `Vary`
to say that the response depended on a negotiation. The `describedby`
URL is absolute because a client resolves a relative reference
against the API's own origin (RFC 8288 section 3.1), and the
Kubernetes API server is a different origin.

A document (discovery, OpenAPI, or the info route) has an `ETag` and
`Cache-Control: no-cache`. A capture has these headers instead:

| Header | Value | Standard |
| --- | --- | --- |
| `Cache-Control` | `no-store` | RFC 9111 section 5.2.2.5 |
| `Content-Disposition` | `inline; filename="{name}-{time}.{ext}"` | RFC 6266 section 4 |
| `Accept-Ranges` | `none` | RFC 9110 section 14.3 |
| `X-Content-Type-Options` | `nosniff` | [Fetch Standard section 3.6](https://fetch.spec.whatwg.org/#x-content-type-options-header) |
| `Transfer-Encoding` | `chunked`, on HTTP/1.1 | RFC 9112 section 7.1 |
| `Content-Location` | on `screen` only: the absolute path of the extension route that was served | RFC 9110 section 8.7 |
| `Link` | on `screen` only: one `rel="alternate"; type="image/png"` and so on, per extension route | RFC 8288 |

The API sends `Content-Location` only on the negotiated route.
Section 8.7 calls it "a more specific identifier for the selected
representation", and says that a `GET` on that URL returns the same
representation. A live capture is different every time, which is also
why `Accept-Ranges` is `none`. There is no `Content-Length`, because
RFC 9112 section 6.2 does not allow one with `Transfer-Encoding`. The
API streams a still in the same way as a clip. On HTTP/2 (RFC 9113),
the protocol frames the body itself.

`X-Content-Type-Options: nosniff` tells a browser to use the
`Content-Type` and not to guess a type from the bytes. A capture is an
image or a video, so a browser never renders one as a page.

The time in `filename` is RFC 3339 UTC with the colons replaced by
hyphens, for example `HDMI-A-1-2026-09-16T21-02-16Z.png`, because not
every file system that a browser saves to allows a colon in a file
name. It is the only time that the API writes in a form other than
RFC 3339.

`HEAD` does not take a frame and does not call the capture container.
A `HEAD` request that fails returns the status and the headers with no
body (RFC 9110 section 15.5).

## Errors

Every error is an `application/problem+json` document (RFC 9457) with
`type`, `title`, `status`, `detail`, and `instance`. `instance` is
the request path, then `#`, then the request id. The API's log line
for the request has the same id, so you can find the log line from
the response and the response from the log line. `detail` quotes the
source of the error: the compositor's refusal, the `TokenReview`
error, the message of a `Display` condition, or the last lines of the
encoder's output.

| Status | When | Extra header | Standard |
| --- | --- | --- | --- |
| 400 | a query that does not parse, or a value that the query parameter table rejects | | RFC 9110 section 15.5.1 |
| 401 | no client certificate and no token, or a token that the `TokenReview` refuses | `WWW-Authenticate: Bearer realm="display-api"`. For a refused token it adds `error="invalid_token"` and an `error_description` with the review's message | section 15.5.2, RFC 6750 section 3 |
| 403 | the `SubjectAccessReview` did not allow the request | `WWW-Authenticate: Bearer realm="display-api", error="insufficient_scope", scope="displays/screen"` | section 15.5.4, RFC 6750 section 3 |
| 404 | no `Display` has that name | | section 15.5.5 |
| 405 | a method other than GET, HEAD, or OPTIONS | `Allow: GET, HEAD, OPTIONS` | section 15.5.6 |
| 406 | `Accept` excludes everything the route can serve | | section 15.5.7 |
| 409 | the `Display` has no panel on its connector: its `Connected` condition is False. A retry does not help until the monitor is connected | | section 15.5.10 |
| 500 | the compositor denied the capture, which a retry does not fix, or the encoder produced no picture | | section 15.6.1 |
| 502 | the capture container returned a response that is not HTTP, or an error that is not a problem document | | section 15.6.3 |
| 503 | the output is already being captured, the compositor is not serving the screen, the capture container refused the connection, is absent, or is not ready, or the `Display` has no `status.node` yet | `Retry-After: 5` | sections 15.6.4 and 10.2.3 |
| 504 | the capture container sent no headers within the header timeout | | section 15.6.5 |

```json
{
  "type": "https://liken.sh/display/problems/capture-denied",
  "title": "The compositor denied the capture",
  "status": 500,
  "detail": "unauthorized",
  "instance": "/v1/display/displays/HDMI-A-1/screen.png#7f3c2a19"
}
```

| Type URI | Meaning | Status |
| --- | --- | --- |
| `https://liken.sh/problems/no-node` | the `Display` has no `status.node` yet | 503 |
| `https://liken.sh/problems/not-acceptable` | `Accept` excludes everything the route can serve | 406 |
| `https://liken.sh/problems/capture-busy` | the output is already being captured | 503 |
| `https://liken.sh/problems/upstream-failed` | the capture container refused the connection, is absent, or is not ready | 503 |
| `https://liken.sh/problems/away` | the `Display` has no panel on its connector. `detail` is the `Connected` condition's message | 409 |
| `https://liken.sh/display/problems/capture-denied` | the compositor denied the capture. `detail` is its `unauthorized` | 500 |
| `https://liken.sh/display/problems/compositor-down` | the compositor is not serving the screen. `detail` is the `CompositorServing` condition's message | 503 |
| `https://liken.sh/display/problems/encoder-failed` | the encode produced no picture | 500 |

The audio and media capture APIs return the types under
`https://liken.sh/problems/` too, so a client handles each one the
same way whichever API returned it. The types under
`https://liken.sh/display/problems/` belong to this API, because only
the display domain has a compositor and an encoder.

A screen with no node and a screen whose
compositor is down both return 503, with different types, because you
wait for different things. In the first case you wait for the
scheduler to place the claim, and in the second you wait for the
operator to start the compositor again. A screen with no panel
returns 409 with no `Retry-After`, because waiting does not connect a
monitor. You connect the monitor, or you capture another screen. An
error with no type of its own has
`type: about:blank` and the status phrase as its `title`. The OpenAPI
document lists every type.

When `detail` describes a failure on a node, it names the node and
the failure, for example "the capture sidecar on node-2 did not
present a certificate this API trusts". It does not include the pod's
address, the port of the private connection, or the path that the API
called. They describe the layout of the cluster, and a caller who can
capture screens does not need them. The API's log line for the
request has the whole connection error under the same request id.

## Discovery

`GET /v1/display` returns the discovery document, in the same format
as the audio and media APIs. It lists the resources that the API serves,
keyed by plural name. Each resource has a `self` template and its
aspects. Each aspect has an RFC 6570 URI template, its media types, its
extensions, and a `redirects` flag. `redirects` is `false` here,
because a capture of a `Display` is a capture of that screen, and does
not redirect to another resource. A client that expands the template
sends `t=5%2C7`, which the parser accepts.

```json
{
  "resources": {
    "displays": {
      "self": "/v1/display/displays/{name}",
      "aspects": {
        "screen": {
          "template": "/v1/display/displays/{name}/screen{.ext}{?t,xywh,width,height,framerate,quality}",
          "mediaTypes": ["image/png", "image/jpeg", "video/mp4", "multipart/x-mixed-replace"],
          "extensions": ["png", "jpg", "mp4", "mjpeg"],
          "redirects": false
        }
      }
    }
  },
  "openapi": "/v1/display/openapi.json"
}
```

## Examples

The `kubectl liken display capture` command calls this API for you,
as the [screenshot guide](/docs/guides/screenshot/) shows. To call it
yourself, use `curl`. The API has no CORS support, so a web page
served from another origin cannot read a capture.

With a `ServiceAccount` token:

```sh
kubectl -n liken-system port-forward svc/display-api 8443:443 &
kubectl -n liken-system get configmap display-api-ca \
  -o jsonpath='{.data.ca\.crt}' > display-api-ca.crt
TOKEN=$(kubectl -n liken-system create token display-viewer --audience display-api --duration 10m)
curl --cacert display-api-ca.crt -H "Authorization: Bearer $TOKEN" \
  https://localhost:8443/v1/display/displays/HDMI-A-1/screen.png > screen.png
curl --cacert display-api-ca.crt -H "Authorization: Bearer $TOKEN" \
  'https://localhost:8443/v1/display/displays/HDMI-A-1/screen.mp4?t=,10' > clip.mp4
```

If you use the client certificate from your kubeconfig, you do not
need a token. The port-forward is a TCP tunnel, so the TLS handshake
runs between `curl` and the API, and the API receives the certificate
unchanged:

```sh
kubectl config view --raw --minify \
  -o jsonpath='{.users[0].user.client-certificate-data}' | base64 -d > client.crt
kubectl config view --raw --minify \
  -o jsonpath='{.users[0].user.client-key-data}' | base64 -d > client.key
curl --cert client.crt --key client.key --cacert display-api-ca.crt \
  https://localhost:8443/v1/display/displays/HDMI-A-1/screen.png > screen.png
```

From a pod on the cluster network, the same two files work against
`https://display-api.liken-system.svc/v1/display/displays/HDMI-A-1/screen.png`
with no port-forward.

**Port-forward limits.** A port-forward works for a still and is too
slow for a stream. It is one TCP connection through the Kubernetes API
server, and its throughput is far below the bit rate of an MJPEG
stream of a 1080p screen, so the stream plays slower than real time.
Read a stream from a pod on the cluster network, or through a
`Service` that the cluster owner exposes.

## Notes

**The codec string.** `screen.mp4` gives its codec in `Content-Type`
as RFC 6381 specifies. `avc1.640029` is High profile, no constraint
flags, level 4.1, which covers every output up to 1920x1080 at 60 fps.
`avc1.640033` is level 5.1, for larger outputs. The encoder is set to
a fixed profile and level, so the API can send the codec string before
the encoder produces its first byte. The info document has the same
string in its `codecs` field, and media-api copies it from this
header into the `Content-Type` of a composed stream.

**Deleting the `display-capture-server` `Secret`.** If you delete the
`Secret` `display-capture-server`, the API creates it again as soon as
its watch reports the delete. Captures work again after the kubelet
next syncs the projected volume, and the kubelet sets that interval,
not this operator. A capture container reads the new files as the
kubelet writes them. A capture container that still has the old leaf
certificate keeps returning 200 the whole time, because that
certificate is still valid and the API still trusts it. You see a 503
only when the capture container also restarted and found no
certificate files. Then `detail` reads "the capture sidecar on node-2
did not present a certificate this API trusts".

**The info route on a screen that is down.** The screen does not need
to be working for you to read its info document.
`GET /v1/display/displays/{name}` returns 200 for a screen with no
panel on its connector, for a screen whose compositor is not serving,
and for a node that the API cannot reach. The document has the name,
the node, and the size and refresh rate from the `Display` status. It
also has `panel: disconnected`, `compositor: down`, or
`sidecar: unreachable`, with the cause in `detail`. `scale`,
`formats`, and `conversion` come from the node, so the document omits
them when the API cannot read them.

**What a capture costs.** While a capture runs, the compositor does
not use the display's hardware planes. A video that a plane would
show directly goes through the GL renderer instead. On a node whose
driver has no VA-API post-processing, the color conversion also runs
on the CPU, which needs several times more CPU cores at 1080p. The
`conversion` field of the info document names the pipeline that the
node uses, and the `display_capture_conversion` metric reports the
same value.
