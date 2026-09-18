---
title: API
weight: 50
toc: true
---

# The screen over HTTP

You can read what a `Display` shows with a plain HTTP `GET`: one
frame as PNG or JPEG, a clip as H.264 in fragmented MP4, or an MJPEG
stream. Nothing is stored. Each capture is taken when you ask for it
and streamed to you while it is made. Two processes are involved.
`display-api` is a `Deployment` in `liken-system`. It authenticates
you, authorizes the request, reads the `Display` to find its node,
and streams the answer back. The capture container in the
`display-operator` pod on that node takes the frames from the
compositor and encodes them on the node's GPU.

**At a glance**

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
`Sink`, a `Source`, and a `Player`. The three APIs share the same
paths, the same HTTP behavior, and the same standards, but no code.

## Authentication

There are two ways to identify yourself: a client certificate or a
Bearer token. The API checks for a certificate first, then for a
token, in the same order as the Kubernetes API server.

### Client certificate

If your TLS connection presents a client certificate signed by the
cluster's own certificate authority, you are that certificate's
subject. Your user name is the subject's common name, and your groups
are the subject's organization values. This is exactly how the
Kubernetes API server reads a client certificate, so the credentials
in your kubeconfig identify you here the same way they identify you
to `kubectl`. The API reads the authority from the `ConfigMap`
`extension-apiserver-authentication` in `kube-system`, which is where
the API server publishes it, and reads it again every minute. A
rotated authority takes effect with no restart. A certificate from
any other authority ends the TLS handshake.

### Bearer token

If the connection has no client certificate, the API looks for a
Bearer token (RFC 6750 section 2.1). It sends the token in a
`TokenReview` with the audience `display-api`, and checks that the
answer names that audience. A pod's ordinary API server token does
not have that audience, so it does not work here. If your kubeconfig
uses OIDC, you have no client certificate. In that case, mint a
`ServiceAccount` token for the `display-api` audience, as the recipe
under [Examples](#examples) does.

### Authorization

After it identifies you, the API sends a `SubjectAccessReview` for
the verb `get` on the resource `displays/screen` in the API group
`display.liken.sh`, with the name of the `Display`. That subresource
does not exist in any CRD. It is a string that RBAC rules match, like
`pods/log`. The discovery and OpenAPI documents need authentication
but no authorization. The info route needs `get` on `displays`. Every
route authorizes before it reads anything, so a 403 never tells you
whether a name exists.

### Grants

The base manifests include a `ClusterRole` named
`display-capture-viewer` with both rules: `displays/screen` for the
capture routes and `displays` for the info route. Nothing is bound to
it. A cluster owner binds it, for example to a `ServiceAccount`:

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

The same `ClusterRole` binds to a person. Use `kind: User` with the
common name from their certificate, or `kind: Group` with one of its
organization values.

A role that grants `resources: ["*"]` on `display.liken.sh` already
includes `displays/screen`, and so does `cluster-admin`. Every
subject with one of those may look at every screen.

`pods/exec` in `liken-system` is also a capture grant. The
`display-operator` pod shares its process namespace between its
containers, so a shell in that pod can reach the capture socket.

Every request that returned bytes writes a `Captured` `Event` on the
`Display`. The message names the subject and the aspect, so `kubectl
describe display` tells you who looked at a screen and when.

## Routes

Every path has the form
`/v1/{domain}/[namespaces/{ns}/]{plural}/{name}/{aspect}[.{ext}]`.
`domain` is the first label of the CRD's API group, `plural` is the
CRD's plural name, and `aspect` is the thing you capture. A
namespaced kind has `namespaces/{ns}` before its plural. A `Display`
is cluster-scoped, so its paths have no namespace. The domain segment
is there so that one ingress can later serve every domain's API under
one host name without a path clash. Until then, each domain is its
own `Service`.

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
from the router itself, so it lists every route above. The
[Routes](/docs/reference/routes/) page renders that document with the
parameters, responses, and fields of each route. The media type
`application/openapi+json` is provisional
(draft-ietf-httpapi-rest-api-mediatypes) and not yet registered. The
copy on this site comes from a development build, so its
`info.version` is `dev`. A release serves its own version.

## Query parameters

The API checks every parameter before it captures anything.

| Parameter | Applies to | Values | Default | Rejected with 400 when |
| --- | --- | --- | --- | --- |
| `t` | every capture route | Media Fragments NPT: `t=begin,end`, `t=begin`, or `t=,end` | none: a clip or a stream runs until you hang up | `t=a,b` with `a >= b`; a begin over 60 s (`captureBeginMax`); an end on a still |
| `xywh` | every capture route | `pixel:` or `percent:`, then `x,y,w,h` | `pixel:`, and the whole screen | the origin is at or past an edge of the screen |
| `width` | every capture route | the width in pixels, after the crop | none: the region's own width | `width` together with `height`; a value larger than the source |
| `height` | every capture route | the height in pixels, after the crop | none: the region's own height | `height` together with `width`; a value larger than the source |
| `framerate` | a clip or a stream: `screen.mp4`, `screen.mjpeg` | frames per second, at most the output's refresh rate | 15 | a value above the refresh rate; a still |
| `quality` | JPEG output: `screen.jpg`, `screen.mjpeg` | the JPEG quality, 1 to 100 | 85 | PNG and MP4 |

`width=` and `height=` are this API's own parameters. Either one
scales the region down after the crop and keeps the aspect ratio. At
quality 85, a 1080p MJPEG frame is about 100 KB, which is 12 Mbit/s
at 15 fps.

`t=` and `xywh=` follow W3C Media Fragments 1.0 (Recommendation,
25 September 2012), sections 4.2.1 and 4.2.2. They go in the query,
not in the URL fragment. Section 3.1 says "a URI query produces a new
resource, while a URI fragment provides a secondary resource", and
section 7.4 says that with the query form "media type changes are
possible". A request like `screen.png?t=5&xywh=...` is exactly that.
The parser follows section 5.1.1: it splits on `&` and `=` first and
percent-decodes second, so `t=10%2C20` and `t=npt%3a10` mean the same
as their literal forms.

Media Fragments puts the zero of `t=` at the start of the source
media. A live screen has no start, so this API puts the zero at the
instant the capture container accepts the request. The container
sends the response headers at once, starts its encoder at once, and
discards frames until the begin time on its own clock. `t=5,7`
discards five seconds and then records two. `t=,10` records ten
seconds from now. `t=5` on a still waits five seconds and takes one
frame.

Media Fragments tells a user agent to ignore an invalid, unknown, or
non-existent dimension. This API returns 400 instead. A query
produces a new resource, so a client that asked for a region must not
silently get the whole screen. A repeated dimension and an unknown
key are 400 as well. A region that runs off an edge is clipped to the
screen (section 6.1.2), not refused. With `percent:`, the origin
rounds down and the size rounds up, and then the width and height
round up to an even number for the encoder. `pixel:` counts the
frame's own physical pixels. If you work in logical pixels, multiply
by the `scale` that the info document reports.

## Content negotiation

An extension names one fixed representation. If your `Accept` header
excludes that representation, you get a 406. RFC 9110 section 12.5.1
lets a server send 406 or ignore `Accept`, and this API tells you
when the two things you said contradict each other. Without an
extension, the API negotiates on `Accept` with q-values, per section
12.5.1. No `Accept` header means any type is acceptable, so you get
the default, `image/png`. Ties and wildcards resolve in this order:
`image/png`, `image/jpeg`, `video/mp4`, `multipart/x-mixed-replace`.

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
| `Link` | `<https://display.liken.sh/docs/reference/api/>; rel="service-doc"` | RFC 8288, RFC 8631 |
| `Link` | on a route about one `Display`: `<https://kubernetes.default.svc/apis/display.liken.sh/v1alpha1/displays/{name}>; rel="describedby"` | RFC 8288 section 3.1 |

`Vary: Accept` is also on the extension routes, where `Accept` only
decides between 200 and 406. Section 12.5.5 gives a second reason for
`Vary`: to say that the response was subject to negotiation. The
`describedby` URL is absolute because RFC 8288 section 3.1 resolves a
relative reference against the API's own origin, which is the wrong
server.

A document (discovery, OpenAPI, or the info route) has an `ETag` and
`Cache-Control: no-cache`. A capture has these headers instead:

| Header | Value | Standard |
| --- | --- | --- |
| `Cache-Control` | `no-store` | RFC 9111 section 5.2.2.5 |
| `Content-Disposition` | `inline; filename="{name}-{time}.{ext}"` | RFC 6266 section 4 |
| `Accept-Ranges` | `none` | RFC 9110 section 14.3 |
| `Transfer-Encoding` | `chunked`, on HTTP/1.1 | RFC 9112 section 7.1 |
| `Content-Location` | on `screen` only: the absolute path of the extension route that was served | RFC 9110 section 8.7 |
| `Link` | on `screen` only: one `rel="alternate"; type="image/png"` and so on, per extension route | RFC 8288 |

`Content-Location` is only sent on the negotiated route. Section 8.7
calls it "a more specific identifier for the selected
representation". The same section says a `GET` on that URL returns
the same representation. That is not true for a live capture, and it
is also why `Accept-Ranges` is `none`. There is no `Content-Length`,
because RFC 9112 section 6.2 forbids one next to
`Transfer-Encoding`. A still is streamed the same way as a clip. On
HTTP/2 (RFC 9113), the protocol frames the body itself.

The time in `filename` is RFC 3339 UTC with the colons replaced by
hyphens, for example `HDMI-A-1-2026-09-16T21-02-16Z.png`. A colon is
not a legal file name character on every system a browser saves to.
This is the only place the API writes a time in a non-standard form.

`HEAD` takes no frame and does not call the capture container. A
`HEAD` on an error returns the status and the headers with no body
(RFC 9110 section 15.5).

## Errors

Every error is an `application/problem+json` document (RFC 9457) with
`type`, `title`, `status`, `detail`, and `instance`. `instance` is
the request path, then `#`, then the request id. The API's log line
for the request has the same id, so you can find one from the other.
`detail` quotes the source of the error: the compositor's refusal,
the `TokenReview` error, the `Display` condition message, or the
encoder's last lines.

| Status | When | Extra header | Standard |
| --- | --- | --- | --- |
| 400 | a query the grammar rejects, or any condition the query parameter table above names | | RFC 9110 section 15.5.1 |
| 401 | no client certificate and no token: `WWW-Authenticate: Bearer realm="display-api"`. A token the `TokenReview` refuses: the same header plus `error="invalid_token"` and an `error_description` with the review's own words | as stated | section 15.5.2, RFC 6750 section 3 |
| 403 | the `SubjectAccessReview` said no | `WWW-Authenticate: Bearer realm="display-api", error="insufficient_scope", scope="displays/screen"` | section 15.5.4, RFC 6750 section 3 |
| 404 | no `Display` has that name | | section 15.5.5 |
| 405 | a method other than GET, HEAD, or OPTIONS | `Allow: GET, HEAD, OPTIONS` | section 15.5.6 |
| 406 | `Accept` excludes everything the route can serve | | section 15.5.7 |
| 500 | the compositor denied the capture, and a retry does not help; or the encoder produced no picture | | section 15.6.1 |
| 502 | the capture container answered with something that is not HTTP, or not a problem document | | section 15.6.3 |
| 503 | the output is already being captured, the compositor is not serving the screen, the capture container refused the connection, is absent, or is not ready, or the `Display` has no `status.node` yet | `Retry-After: 5` | sections 15.6.4 and 10.2.3 |
| 504 | the capture container sent no headers within the header timeout | | section 15.6.5 |

```json
{
  "type": "https://display.liken.sh/problems/capture-denied",
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
| `https://display.liken.sh/problems/capture-denied` | the compositor denied the capture. `detail` is its `unauthorized` | 500 |
| `https://display.liken.sh/problems/compositor-down` | the compositor is not serving the screen. `detail` is the `CompositorServing` condition's message | 503 |
| `https://display.liken.sh/problems/encoder-failed` | the encode produced no picture | 500 |

The first four types are the ones the three capture APIs share. The
last three are this API's own, because only the display domain has a
compositor or an encoder. A screen with no node and a screen whose
compositor is down are both a 503, with different types, because you
are waiting for different things: the scheduler in one case, and the
operator starting the compositor again in the other. An error with no
type of its own has `type: about:blank` and the status phrase as its
`title`. The OpenAPI document lists every type.

When `detail` describes a failure on a node, it names the node and
what went wrong, for example "the capture sidecar on node-2 did not
present a certificate this API trusts". It never includes the pod's
address, the port of the private connection, or the path this API
called. Those describe the shape of the cluster, and a caller who may
read screens does not need them. The API's log line for the request
has the whole dial error under the same request id.

## Discovery

`GET /v1/display` returns the discovery document. All three capture
APIs use the same shape. It lists the resources the API serves, keyed
by plural name. Each resource has a `self` template and its aspects.
Each aspect has an RFC 6570 URI template, its media types, its
extensions, and a `redirects` flag. Here `redirects` is `false`,
because a `Display` is the thing captured and not a pointer to
something else. A client that expands the template sends `t=5%2C7`,
which the parser accepts.

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

This version has no CORS support and no `kubectl` plugin. A browser
can only reach the API on the same origin through a port-forward.
The everyday tool is `curl`.

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

With the client certificate from your kubeconfig, there is no token
to mint. The port-forward is a TCP tunnel, so the TLS handshake runs
end to end and the certificate reaches the API unchanged:

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

**Port-forward limits.** A port-forward is fine for a still and bad
for a stream. It is a single TCP connection through the API server.
In our tests it moved about 2 Mbit/s: a 3 second MJPEG stream that
the node captured in 3.06 s took 25 s to read through the forward,
and 3.09 s from a pod on the cluster network. A 1080p MJPEG stream
needs about 15 Mbit/s, so through the forward you see it at about one
eighth of real time. Stills are not affected. `screen.png` took 0.9 s
to first byte through the forward and 0.76 s from the cluster
network. Read a stream from a pod on the cluster network, or through
a `Service` the cluster owner exposes.

## Notes

**The codec string.** `screen.mp4` names its codec in `Content-Type`
the way RFC 6381 specifies. `avc1.640029` is High profile, no
constraint flags, level 4.1, which covers every output up to 1920x1080
at 60 fps. `avc1.640033` is level 5.1, for outputs above that. The
encoder pins the profile and level instead of letting them follow the
stream, so the parameter is correct before the first byte. The info
document has the same string in its `codecs` field, and media-api
copies it from this header into the `Content-Type` of a composed
stream.

**A re-minted `Secret`.** If you delete the `Secret`
`display-capture-server`, the API mints it again within a minute, and
the screens come back about 98 seconds after the delete. The API's
part takes under 30 seconds. The rest is the kubelet's own sync period
for the projected volume, which nothing in this operator controls. A
capture container that still has the old leaf certificate keeps
returning 200 the whole time, because that leaf is still valid and
this API still trusts it. You only see a 503 where the capture
container also restarted and found no certificate files. Then the
`detail` reads "the capture sidecar on node-2 did not present a
certificate this API trusts".

**The info route on a screen that is down.** You do not need the
screen to be up to ask what it is.
`GET /v1/display/displays/{name}` returns 200 for a screen whose
compositor is not serving, and for a node this API cannot reach. The
document has the name, the node, and the size and refresh rate from
the `Display` status, plus `compositor: down` or
`sidecar: unreachable` with the condition's message in `detail`.
`scale`, `formats`, and `conversion` come from the node, so they are
absent instead of guessed.

**What a capture costs.** A capture turns off the compositor's
hardware planes for its whole length. A film that a plane would
normally show goes through the GL renderer instead while a clip runs.
On a node whose driver has no VA-API post-processing, the color
conversion runs on the CPU too, which costs about four times the
cores at 1080p. The info document's `conversion` field names the
pipeline the node uses, and the `display_capture_conversion` metric
reports the same value to a dashboard.
