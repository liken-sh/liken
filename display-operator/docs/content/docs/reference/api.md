---
title: API
weight: 50
toc: true
---

# The screen over HTTP

A `GET` on a `Display` answers with what its screen shows: one frame
as PNG or JPEG, a clip as H.264 in fragmented MP4, or an MJPEG
stream. Nothing is stored. Every capture is taken when it is asked
for and streamed to the caller as it is made.

Two processes answer. `display-api` is a `Deployment` in
`liken-system`. It authenticates the caller, authorizes the request,
reads the `Display` to find its node, and streams the answer back.
The capture container in the `display-operator` pod on every node
takes frames from the compositor and encodes them on the node's GPU.

The audio and media operators serve the same kind of API for a
`Sink`, a `Source`, and a `Player`. The three share the route
vocabulary, the HTTP conduct, and the standards, and no code.

## The routes

The path grammar is
`/v1/{domain}/[namespaces/{ns}/]{plural}/{name}/{aspect}[.{ext}]`.
`domain` is the first label of the CRD's API group, `plural` is the
CRD's plural, `aspect` is the thing captured, and a namespaced kind
puts `namespaces/{ns}` before its plural. A `Display` is
cluster-scoped, so its paths carry no namespace. The domain segment
lets one ingress later mount every domain's API under one host name
with no path clash; until then each domain is one `Service`.

| Method | Path | 200 `Content-Type` | Notes |
| --- | --- | --- | --- |
| GET, HEAD | `/v1/display` | `application/json` | The discovery document |
| GET, HEAD | `/v1/display/openapi.json` | `application/openapi+json` | OpenAPI 3.1 |
| GET, HEAD | `/v1/display/displays/{name}` | `application/json` | Size, scale, refresh, formats, links |
| GET, HEAD | `/v1/display/displays/{name}/screen` | negotiated | Default `image/png` |
| GET, HEAD | `/v1/display/displays/{name}/screen.png` | `image/png` | One frame |
| GET, HEAD | `/v1/display/displays/{name}/screen.jpg` | `image/jpeg` | One frame |
| GET, HEAD | `/v1/display/displays/{name}/screen.mp4` | `video/mp4` | H.264 in fragmented MP4, until hang-up or the `t=` end |
| GET, HEAD | `/v1/display/displays/{name}/screen.mjpeg` | `multipart/x-mixed-replace; boundary=ffmpeg` | One `image/jpeg` part per frame |
| OPTIONS | any of the above | none, 204 | `Allow: GET, HEAD, OPTIONS` |

The [OpenAPI 3.1 description](/docs/reference/openapi.json) of every
route above is generated from the router itself.

`application/openapi+json` is provisional
(draft-ietf-httpapi-rest-api-mediatypes) and not yet registered. The
copy on this site is a development build's, so its `info.version`
reads `dev` where a release serves its own version.

## The fields every answer carries

Every response carries `Content-Type` (RFC 9110 section 8.3), `Date`
(section 6.6.1), `Vary: Accept` (section 12.5.5), and two `Link`
fields (RFC 8288): `</v1/display/openapi.json>; rel="service-desc"`
and `<https://display.liken.sh/docs/reference/api/>; rel="service-doc"`,
the relations RFC 8631 defines. `Vary` goes on the extension routes
too, where `Accept` only decides between 200 and 406, for section
12.5.5's second purpose: to say the response was subject to
negotiation.

Every route about one `Display` also carries
`<https://kubernetes.default.svc/apis/display.liken.sh/v1alpha1/displays/{name}>; rel="describedby"`,
absolute because RFC 8288 section 3.1 resolves a relative reference
against the API's own origin.

A capture carries these instead of a document's `ETag` and
`Cache-Control: no-cache`:

| Header | Value | Standard |
| --- | --- | --- |
| `Cache-Control` | `no-store` | RFC 9111 section 5.2.2.5 |
| `Content-Disposition` | `inline; filename="{name}-{time}.{ext}"` | RFC 6266 section 4 |
| `Accept-Ranges` | `none` | RFC 9110 section 14.3 |
| `Transfer-Encoding` | `chunked`, on HTTP/1.1 | RFC 9112 section 7.1 |
| `Content-Location` | on `screen` only: the absolute path of the extension form served | RFC 9110 section 8.7 |
| `Link` | on `screen` only: `rel="alternate"; type="image/png"` and so on, one per extension form | RFC 8288 |

`Content-Location` is sent on the negotiated route only, under
section 8.7's clause that it is "a more specific identifier for the
selected representation". Section 8.7's identity guarantee, that a
`GET` on it returns the same representation, does not hold for a
live capture, which is also why `Accept-Ranges` is `none`. There is
no `Content-Length`, because RFC 9112 section 6.2 forbids one beside
`Transfer-Encoding`; a still streams like a clip. HTTP/2 (RFC 9113)
frames the body itself.

The `filename` time is RFC 3339 UTC with the colons replaced by
hyphens, `HDMI-A-1-2026-09-16T21-02-16Z.png`, because a colon is not
legal in a file name everywhere a browser saves. This is the one
place a time is not in the standard form.

`HEAD` takes no frame and makes no call to the capture container. A
`HEAD` on an error carries the status and the fields and no body
(RFC 9110 section 15.5).

## Selecting a region and a time

`t=` and `xywh=` follow W3C Media Fragments 1.0 (Recommendation,
25 September 2012), sections 4.2.1 and 4.2.2. They go in the query,
not in the fragment, because section 3.1 says "a URI query produces
a new resource, while a URI fragment provides a secondary resource",
and section 7.4 says that with the query form "media type changes
are possible", which is `screen.png?t=5&xywh=...` in one sentence.
The parser follows section 5.1.1: it splits on `&` and `=` first and
percent-decodes second, so `t=10%2C20` and `t=npt%3a10` parse as
their literal forms.

Media Fragments fixes the zero of `t=` at the start of the source
media. A live screen has no start, so this API defines the zero as
the instant the capture container accepts the request. The container
sends the response headers at once, starts its encoder at once, and
discards frames until the begin on its own clock. `t=5,7` discards
five seconds, then records two; `t=,10` records ten from now; `t=5`
on a still waits five seconds and takes one frame. A begin may be at
most 60 seconds.

Media Fragments tells a user agent to ignore an invalid, unknown, or
non-existent dimension. This API answers 400 instead, because a query
produces a new resource and a client that asked for a region must not
silently get the whole screen. A repeated dimension, an unknown key,
`t=a,b` with `a >= b`, a `t=` end on a still, and a region whose
origin is at or past an edge of the screen are all 400. A region that
runs off an edge is clipped to the screen (section 6.1.2), not
refused. `percent:` rounds the origin down and the size up, then
rounds the width and height up to even for the encoder. `xywh=` is
`pixel:` by default, in the frame's own physical pixels; a client
that works in logical pixels multiplies by the `scale` the info
document reports.

`width=` and `height=` are this API's own. Either scales the region
down after the crop with the aspect kept; both together, or a value
above the source, is a 400. `framerate=` is the frames per second of
a clip or MJPEG stream, 15 by default, at most the output's refresh,
and a 400 on a still. `quality=` is the JPEG quality, 1 to 100, 85 by
default, and a 400 on PNG and MP4. At 85 a 1080p MJPEG frame is about
100 KB, 12 Mbit/s at 15 fps. Every knob is checked before anything is
captured.

## Choosing a format

An extension names one fixed representation, and an `Accept` that
excludes it gets 406: RFC 9110 section 12.5.1 lets a server send 406
or ignore `Accept`, and a client that said two contradictory things
is told. Without an extension, `Accept` negotiates per section 12.5.1
with q-values. No `Accept` means any type is acceptable, so the
default `image/png` is served. Ties and wildcards resolve in the
route's order: `image/png`, `image/jpeg`, `video/mp4`,
`multipart/x-mixed-replace`.

| Request | Answer |
| --- | --- |
| `GET screen` | 200 `image/png`, `Content-Location: /v1/display/displays/{name}/screen.png` |
| `GET screen`, `Accept: image/jpeg` | 200 `image/jpeg` |
| `GET screen`, `Accept: image/*` | 200 `image/png`, the first image listed |
| `GET screen`, `Accept: video/mp4, image/png;q=0.5` | 200 `video/mp4`, a stream |
| `GET screen`, `Accept: image/png;q=0, */*` | 200 `image/jpeg` |
| `GET screen`, `Accept: audio/wav` | 406, `acceptable` lists four |
| `GET screen.png`, `Accept: image/*` | 200 `image/png` |
| `GET screen.png`, `Accept: video/mp4` | 406, `acceptable` lists `screen.png` and its three siblings |

## The errors

Every error is an `application/problem+json` document (RFC 9457) with
`type`, `title`, `status`, `detail`, and `instance`. `instance` is
the request path plus `#` plus the request id, and the API's log line
for the request carries the same id. `detail` carries the source's
own words: the compositor's refusal, the `TokenReview`'s error, the
`Display`'s condition message, or the encoder's last lines.

| Status | When | Extra header | Standard |
| --- | --- | --- | --- |
| 400 | a query the grammar rejects; a repeated or unknown query key; `t=a,b` with `a >= b`; `width` with `height`; a `t=` end on a still; a `t=` begin over `captureBeginMax`, 60 s; `framerate` above the refresh | | RFC 9110 section 15.5.1 |
| 401 | no token: `WWW-Authenticate: Bearer realm="display-api"`; a token the `TokenReview` refuses: the same plus `error="invalid_token"` and `error_description` with the review's own words | as stated | section 15.5.2, RFC 6750 section 3 |
| 403 | the `SubjectAccessReview` says no | `WWW-Authenticate: Bearer realm="display-api", error="insufficient_scope", scope="displays/screen"` | section 15.5.4, RFC 6750 section 3 |
| 404 | no `Display` of that name | | section 15.5.5 |
| 405 | a method other than GET, HEAD, OPTIONS | `Allow: GET, HEAD, OPTIONS` | section 15.5.6 |
| 406 | `Accept` excludes everything the route serves | | section 15.5.7 |
| 500 | the compositor denied the capture, `unauthorized`; type `capture-denied` | none, a retry never clears it | section 15.6.1 |
| 502 | the sidecar answered something that is not HTTP or not a problem document | | section 15.6.3 |
| 503 | the output is being captured; the compositor is not serving; the sidecar refused the connection, is absent, or is not ready; the `Display` has no `status.node` yet | `Retry-After: 5` | sections 15.6.4 and 10.2.3 |
| 504 | the sidecar sent no headers within the header timeout | | section 15.6.5 |

```json
{
  "type": "https://display.liken.sh/problems/capture-denied",
  "title": "The compositor denied the capture",
  "status": 500,
  "detail": "unauthorized",
  "instance": "/v1/display/displays/HDMI-A-1/screen.png#7f3c2a19"
}
```

The problem types the three capture APIs share live under
`https://liken.sh/problems/`: `no-node`, `not-acceptable`,
`capture-busy`, and `upstream-failed`. This domain's own,
`capture-denied`, lives under `https://display.liken.sh/problems/`.
An error with no type of its own is `about:blank`, and its `title`
is the status phrase.

## Discovery

`GET /v1/display` is the shape the three capture APIs share. A client
reads from it which resources the API serves, keyed by plural, each
with its `self` template and its aspects. Every aspect carries an
RFC 6570 template, its media types, its extensions, and `redirects`,
which is `false` here because a `Display` is the thing itself. A
client that expands the template sends `t=5%2C7`, which the parser
accepts.

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

## Who may look at a screen

Every request carries a Bearer token (RFC 6750 section 2.1). The API
sends it in a `TokenReview` with the audience `display-api` and
checks that the answer names that audience, so a pod's ordinary
API-server token does not open it. The cost is that a person on an
OIDC kubeconfig cannot use their own credentials: they mint a
`ServiceAccount` token for that audience, which is what the recipe
below does.

The API then sends a `SubjectAccessReview` for the verb `get` on
`displays/screen` in the group `display.liken.sh`, naming the
`Display`. The subresource exists in no CRD; it is a string RBAC
matches, the way `pods/log` is. The discovery and OpenAPI documents
need authentication and no authorization, and the info route needs
`get` on `displays`. Every route authorizes before it reads, so a 403
never says whether a name exists. The base ships a `ClusterRole`
`display-capture-viewer` with both rules, `displays/screen` for the
capture routes and `displays` for the info route beside them, and
binds it to nobody; a cluster owner binds it:

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

A role with a wildcard `resources: ["*"]` on `display.liken.sh`, and
`cluster-admin`, already grant `displays/screen`, so every subject
that holds one may look at every screen.

`pods/exec` in `liken-system` reaches the capture socket through the
`display-operator` pod's shared process namespace, so that grant is a
capture grant too.

Every request that produced bytes writes a `Captured` `Event` on the
`Display`, with the subject and the aspect in its message, so
`kubectl describe display` answers who looked at a screen and when.

## The recipe

This version has no CORS and ships no `kubectl` plugin, so a browser
reaches the API only on the same origin through a port-forward, and
the everyday use is `curl`:

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

A capture holds the compositor's hardware planes off for its whole
length, so a film that a plane would show is composited through the
GL renderer while a clip runs.
