# The screen over HTTP

Plan 22. Planned.

A `GET` on a `Display` answers with what its screen shows: one frame
as PNG or JPEG, a clip as H.264 in fragmented MP4, or an MJPEG stream.
`display-api`, a `Deployment` in `liken-system`, authenticates the
caller, finds the node, and streams the answer back. A capture
sidecar in the `display-operator` pod on every node takes frames from
the compositor and encodes them on the node's GPU. Nothing is stored.

This is the display instance of a shape three operators share:
audio-operator's plan 09 taps sinks and sources, and media-operator's
plan 34 composes a `Player`'s screen and sound from the two. They
share the route vocabulary, the HTTP conduct, and the standards, and
no code.

## The problem

Every capture on a `liken` screen is denied today. Weston 14.0.2 has
`weston_capture_v1`, but the answer comes from the authorities
registered with `weston_compositor_add_screenshot_authority()`, and
the installed `libweston.h` documents that with none registered every
attempt is forbidden. ivi-shell's one authority admits only a client
started from a Super+S binding, and these machines have no keyboard.
So a person who wants to know what a screen shows walks to it. The
answer has to be light while unused: the pod that holds the screen
is `system-node-critical` on machines with 1 GB of memory.

## The design

```
  kubectl / curl / agent / media-api
        |  HTTPS, Bearer token with audience display-api
        v
  display-api           Deployment, one per cluster, in liken-system
        |  reads Display.status.node, finds the sidecar pod on that node
        v
  capture               fourth container in the display-operator pod on every node
        |  weston_capture_v1 on /etc/weston/wayland-capture
        v
  weston                the compositor the pod already runs
```

### The routes

The path grammar is
`/v1/{domain}/[namespaces/{ns}/]{plural}/{name}/{aspect}[.{ext}]`:
`domain` is the first label of the CRD's API group, `plural` the
CRD's plural, `aspect` the thing tapped, and a namespaced kind puts
`namespaces/{ns}` before its plural. The domain segment lets one
ingress later mount every domain's API under one host name with no
path clash, and lets a future `video` domain fit beside `audio`. That
ingress is not v1 work; v1 is one `Service` per domain.

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

Every response carries `Content-Type` (RFC 9110 section 8.3), `Date`
(section 6.6.1), `Vary: Accept` (section 12.5.5), and two `Link`
fields (RFC 8288): `</v1/display/openapi.json>; rel="service-desc"`
and `<https://display.liken.sh/docs/reference/api/>; rel="service-doc"`,
the relations RFC 8631 defines. `Vary` goes on extension routes too,
where `Accept` only decides between 200 and 406, for 12.5.5's second
purpose: to say the response was subject to negotiation. Every route
also carries
`<https://kubernetes.default.svc/apis/display.liken.sh/v1alpha1/displays/{name}>; rel="describedby"`,
absolute because RFC 8288 section 3.1 resolves a relative reference
against the API's own origin.

The three document routes carry `ETag` (the build version), answer
`If-None-Match` with 304 and `Vary: Accept`, and carry
`Cache-Control: no-cache`. A capture response carries these instead:

| Header | Value | Standard |
| --- | --- | --- |
| `Cache-Control` | `no-store` | RFC 9111 section 5.2.2.5 |
| `Content-Disposition` | `inline; filename="{name}-{time}.{ext}"` | RFC 6266 section 4 |
| `Accept-Ranges` | `none` | RFC 9110 section 14.3 |
| `Transfer-Encoding` | `chunked`, on HTTP/1.1 | RFC 9112 section 7.1 |
| `Content-Location` | on `screen` only: the absolute path of the extension form served | RFC 9110 section 8.7 |
| `Link` | on `screen` only: `rel="alternate"; type="image/png"` and so on, one per extension form | RFC 8288 |

`Content-Location` is sent on the negotiated route only, under 8.7's
clause that it is "a more specific identifier for the selected
representation". 8.7's identity guarantee, that a `GET` on it returns
the same representation, does not hold for a live capture, which is
also why `Accept-Ranges` is `none`. No `Content-Length`, because
RFC 9112 section 6.2 forbids one beside `Transfer-Encoding`; a still
streams like a clip so the headers go out before the frame. HTTP/2
(RFC 9113) frames the body itself. `type` on a `Link` carries no
media type parameters. The `filename` time is RFC 3339 UTC with the
colons replaced by hyphens, `HDMI-A-1-2026-09-16T21-02-16Z.png`,
because a colon is not legal in a file name everywhere a browser
saves; this is the one place the time is not in the standard form.

`HEAD` takes no frame and makes no call to the sidecar. A `HEAD` on
an error carries no body (RFC 9110 section 15.5). The errors, each an
RFC 9457 problem document:

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

No route accepts a body, so 415 is never sent; a `POST` gets 405 and
`Allow`. 409 is reserved for a state the caller can act on, and a
`Display` waiting for its node is not one.

### Selection

`t=` and `xywh=` follow the ABNF of W3C Media Fragments 1.0
(Recommendation, 25 September 2012), sections 4.2.1 and 4.2.2. They go
in the query because section 3.1 says "a URI query produces a new
resource, while a URI fragment provides a secondary resource", and
section 7.4 says that with the query form "media type changes are
possible. For example, a spatial fragment from a video at a certain
time offset could be retrieved as a jpeg using a specific HTTP
'Accept' header", which is `screen.png?t=5&xywh=...` in one sentence.
The parser follows section 5.1.1: split on `&` and `=` first,
percent-decode second, so `t=10%2C20` and `t=npt%3a10` (section
6.1.1) parse as their literal forms.

Media Fragments fixes NPT's zero at the start of the source media
(section 6.1.1). A live tap has no start, so this API defines the
source media's zero as the instant the sidecar accepts the request.
The wall-clock `clock:` format that would say this directly is in the
advanced document, not in 1.0 (section 4.2.1). The sidecar sends the
headers at once and starts its pipeline at once, and discards frames
until `begin` on its own clock, so frame zero of the body is
origin plus `begin`. `t=5,7` discards five seconds, then records two;
`t=,10` records ten from now; `t=5` on a still discards five seconds
and takes one frame.

Media Fragments tells a user agent to ignore an invalid, unknown, or
non-existent dimension (sections 6.2, 6.2.1, 6.3.1). This API answers
400 instead, because a query produces a new resource and a client that
asked for a region must not silently get the whole screen. A repeated
dimension, an unknown key, `t=a,b` with `a >= b`, and a `t=` end on a
still are all 400. An out-of-range `xywh` is clipped per section
6.1.2, not refused. `percent:` rounds the origin down and the size up
(section 6.1.2, whose published formula transposes the operands),
then rounds width and height up to even for the encoder.

`xywh=` is `pixel:` by default, in the frame's own pixels, the
physical pixels `weston_capture_source_v1.size` reports; plan 15's
`scale=2` changes what clients draw at, not the frame. A client in
logical pixels multiplies by the `scale` the info document reports,
and a `Layout` region, stated as fractions, maps to `percent:`.

`width=` and `height=` are this API's own knobs; neither spec has a
word for a scale. Either scales down after the crop with the aspect
kept; both together, or a value above the source, is a 400.
`framerate=` is the frames per second of a clip or MJPEG stream,
default 15, ceiling the output's refresh, a 400 on a still. Every
frame costs the sidecar a copy of the whole frame, so the default
halves the encode cost of the common request. `quality=` is the JPEG
quality, 1 to 100, default 85, a 400 on PNG and MP4; at 85 a 1080p
MJPEG frame is about 100 KB, 12 Mbit/s at 15 fps. Every knob is
validated before anything is captured.

### Negotiation

An extension names one fixed representation, and an `Accept` that
excludes it gets 406: RFC 9110 section 12.5.1 lets a server send 406
or ignore `Accept`, and a client that said two contradictory things
gets told. Without an extension, `Accept` negotiates per section
12.5.1 with q-values; no `Accept` means any type is acceptable, so
the default `image/png` is served. Ties and wildcards resolve in the
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

The 406 document's `acceptable` member is a list of
`{"type": "image/png", "href": "/v1/display/displays/{name}/screen.png"}`,
the "representation characteristics and corresponding resource
identifiers" RFC 9110 section 15.5.7 asks for, an extension member
RFC 9457 section 3.2 allows.

### Errors

Every error is `application/problem+json` with `type`, `title`,
`status`, `detail`, and `instance`. `instance` is the request path
plus `#` plus the request id, and the log line carries the same id.
Shared types live under `https://liken.sh/problems/` (`no-node`,
`not-acceptable`, `capture-busy`, `upstream-failed`); display's own
under `https://display.liken.sh/problems/` (`capture-denied`). With
`about:blank`, `title` is the status phrase (RFC 9457 section 4.2.1).
`detail` carries the source's own words: a weston `failed` event's
`msg`, the `CompositorServing` condition's message, or ffmpeg's
stderr tail.

```json
{
  "type": "https://display.liken.sh/problems/capture-denied",
  "title": "The compositor denied the capture",
  "status": 500,
  "detail": "unauthorized",
  "instance": "/v1/display/displays/HDMI-A-1/screen.png#7f3c2a19"
}
```

### Discovery

`GET /v1/display` is the shape the three APIs share: `resources`,
keyed by plural, each with `self` and its `aspects`; every aspect
carries an RFC 6570 template, its media types, its extensions, and
`redirects`, false here.

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

RFC 6570's form-style expansion percent-encodes commas and colons,
so a client that expands the template sends `t=5%2C7`, which the
parser's decoding step accepts. `GET /v1/display/displays/{name}`
answers `name`, `node`, `width`, `height`, `scale`, `refresh`, and
`formats`, from the sidecar's own `wl_output` events, with
`rel="related"` links (RFC 4287, registered) to its capture routes.
Any extension relation the three APIs need lives under
`https://liken.sh/rel/`, never under a per-domain host.

The router holds every route in one table that both documents render
from. OpenAPI 3.1 has no `{.ext}`, so the document enumerates the
extension paths as separate path items. The served copy injects
`servers: [{url: ...}]` from the configured public base, else the
request's own origin; the committed copy holds a placeholder, and the
equality test ignores `servers`. `application/openapi+json` is
provisional (draft-ietf-httpapi-rest-api-mediatypes), not yet
registered. `display-operator openapi` prints the document, and
`docs/Makefile` runs it into `content/docs/reference/openapi.json`
beside a `reference/api.md` page, the way `crdref` generates
`displays.md`.

### The capture sidecar

**The door.** `wet_module_init` in `liken-layout.so` gains a call to
`bind_listening_socket` plus `wl_display_add_socket_fd` on the fixed
path `/etc/weston/wayland-capture`, with its own `listening_socket`
entry, named `wayland-capture`, that carries no connector. That is
new code: today only `do_listen` reaches those two calls, and it
requires a connector. `read_client_origin()` then needs nothing,
because it matches the accepted socket's basename against
`listening_sockets`. The path is in the `weston-config` `emptyDir` on
purpose: the claim sockets live under `XDG_RUNTIME_DIR`, the
directory CDI delivers to consumer pods, and the capture socket must
never be delivered. The module registers a screenshot authority that
sets `att->authorized = true` when the client's origin is
`wayland-capture` and sets nothing otherwise, which leaves every
other client denied. Weston is never started with `--debug`, whose
`screenshot_allow_all()` admits everyone. The pod's
`shareProcessNamespace` means `pods/exec` in `liken-system` reaches
the socket through `/proc`, so that grant is a capture grant, and the
manual says so.

**The client.** The sidecar is the `display-operator` binary in a
fourth role, `capture`, in `ghcr.io/liken-sh/display-capture`, a new
`Dockerfile` target `FROM ffmpeg` (plan 19) plus the static binary.
The operator's final stage gains a name and `release.yaml`'s operator
build gains `target:`, because a stage after an unnamed last one
would change which image ships as `display-operator`. The client
speaks Wayland through `wayland.go`, plus four requests new to that
code: `wl_shm.create_pool` with a descriptor over `SCM_RIGHTS`
(`WriteMsgUnix`, `unix.UnixRights`), `wl_shm_pool.create_buffer`,
`weston_capture_v1.create`, and `weston_capture_source_v1.capture`,
and the `wl_output.scale` event, opcode 3.

The protocol is `weston-output-capture.xml` from `libweston-14-dev`
14.0.2-5, read 2026-09-16. The client sends `create` with the
`framebuffer` source, which "copies the contents of the final
framebuffer", "temporarily disables all use of hardware planes", and
"is always available". It reads `format` and `size`, makes a
`wl_shm` buffer, sends `capture`, and waits for `complete`, `retry`,
or `failed` before the next `capture`; a second `capture` in flight
is the `sequence` protocol error, which kills the connection. The
output "will go through a repaint some time after this request", so
a still screen yields a frame and a film on an overlay plane is
composited in. The `format` event is a DRM fourcc; the sidecar maps
it to the `wl_shm` enum (`ARGB8888` 0, `XRGB8888` 1, the rest
unchanged) and derives ffmpeg's `-pixel_format` from it, because the
GL renderer's read format is not fixed. Stride is exactly
`width * 4`; weston's PBO path assumes it. `retry` means the size or
format changed: on a still the sidecar reallocates and reissues, on
a clip it ends the response and logs both sizes, because
`-video_size` cannot follow. The buffer is a `memfd`, 33 MB at 4K,
one output at a time, unmapped when the request ends.

**The encoders.** Every format runs through ffmpeg, one process per
request, one code path. The sidecar cuts the frame to `xywh=` in its
own write, so the pipe carries the region only. The device is the
render node the pod's `render` request delivers, `/dev/dri/renderD<n>`.

```
# a still
ffmpeg -f rawvideo -pixel_format bgr0 -video_size 1920x1080 -i pipe:0 \
  -frames:v 1 -c:v png -f image2pipe pipe:1

# a clip
ffmpeg -f rawvideo -pixel_format bgr0 -video_size 1920x1080 -framerate 15 \
  -thread_queue_size 8 -i pipe:0 \
  -init_hw_device vaapi=gpu:/dev/dri/renderD128 -filter_hw_device gpu \
  -vf 'hwupload,scale_vaapi=w=1920:h=-2:format=nv12' \
  -c:v h264_vaapi -g 15 -bf 0 \
  -f mp4 -movflags frag_keyframe+empty_moov+default_base_moof -frag_duration 1000000 pipe:1

# the low-end stream
ffmpeg ... -vf 'hwupload,scale_vaapi=format=nv12' -c:v mjpeg_vaapi -global_quality 85 -jfif 1 \
  -g 15 -bf 0 -f mpjpeg pipe:1
```

`h264_vaapi` takes only `vaapi` frames (ffmpeg 8.0.1), so `hwupload`
is in every clip's graph, with its device from `-init_hw_device` and
`-filter_hw_device` (ffmpeg.org/ffmpeg.html, 2026-09-16). The color
conversion runs on the GPU in `scale_vaapi=format=nv12`;
`format=nv12,hwupload` is the software fallback only if the node's
driver refuses a `bgr0` upload. `scale_vaapi` is where `width=`
lands, and a clip on a 4K output is scaled to 1080p unless `width=`
asks for more. `-g <framerate> -bf 0` puts a keyframe every second,
because `h264_vaapi` defaults to a 120-frame GOP and `frag_keyframe`
("Fragment at video keyframes") cuts only there; `-frag_duration` is
the second cut. `empty_moov` ("Make the initial moov atom empty") and
`default_base_moof` ("Set the default-base-is-moof flag in tfhd
atoms") let a browser or mpv play the stream as it arrives.
`-thread_queue_size` is bounded so a slow encoder cannot grow the
process. MJPEG is the `mpjpeg` muxer,
`multipart/x-mixed-replace; boundary=ffmpeg`, a type IANA registers
(W3C, 2014) with `boundary` required; no RFC defines it.
`mjpeg_vaapi` takes `-global_quality` on a 1 to 100 scale.

**One at a time.** One capture per output; outputs on one card are
independent. A second request on a busy output gets 503,
`Retry-After: 5`, and a `detail` naming the running capture's end or
"until its client closes". The knob is the environment variable
`CAPTURE_PER_OUTPUT`, default 1.

**The container**, a fourth in `deploy/operator.yaml`:

```yaml
        - name: capture
          image: ghcr.io/liken-sh/display-capture:latest
          args: ["capture"]
          env:
            - name: CAPTURE_ADDR
              value: ":9201"
          ports:
            - name: capture
              containerPort: 9201
          securityContext:
            capabilities: { drop: ["ALL"] }
            privileged: false
            allowPrivilegeEscalation: false
          resources:
            requests: { cpu: 5m, memory: 16Mi }
            limits: { memory: 384Mi }
            # The encoder needs the render node and nothing else.
            # DRM master and the i2c wires belong to the compositor
            # and the operator.
            claims:
              - { name: gpu, request: render }
          volumeMounts:
            - name: weston-config
              mountPath: /etc/weston
            - name: capture-tls
              mountPath: /var/run/display-capture-tls
              readOnly: true
          # No readiness probe: a late credential must not hold this
          # system-node-critical pod NotReady and stall the
          # DaemonSet's one-node-at-a-time roll. A restart of this
          # container ends every running capture on the node and
          # nothing else.
          livenessProbe:
            httpGet: { path: /healthz, port: capture, scheme: HTTPS }
            periodSeconds: 30
            failureThreshold: 3
      volumes:
        - name: capture-tls
          secret:
            secretName: display-capture-server
            optional: true
```

The sidecar's leaf reaches it as an `optional` `Secret` volume,
reloaded on file change, so the pod starts before the API has minted
it and the DaemonSet's `ServiceAccount` needs no grant on the
`Secret`. Until the files exist, `display_capture_ready` is 0 and the
API answers 503. On a cgroup v2 machine, an ffmpeg that exceeds the
limit kills the whole container cgroup, so one runaway encode ends
every capture on the node and the kubelet restarts the container.
The pod's limits become 64 + 512 + 128 + 384 = 1088Mi on a machine
with 1 GB, which is why a 4K clip is scaled to 1080p and why the
drill decides the number.

**Authorizing the API.** Encryption comes from a certificate,
identity from Kubernetes. Every request from the API carries a
projected `ServiceAccount` token with audience `display-capture` as a
Bearer token; the sidecar checks it with a `TokenReview` for that
audience, confirms `status.audiences` contains it, and matches the
username to `system:serviceaccount:liken-system:display-api`.
`/metrics`, `/healthz`, and `/readyz` need no token.

### The Deployment

`display-api` is the same binary in the role `api`, in
`ghcr.io/liken-sh/display-api`, `FROM scratch` with the binary
alone, `strategy: Recreate`, and a create that loses the race reads
the winner. The `Service` `display-api` in `liken-system` maps 443
to 8443. The API runs one informer on pods in `liken-system` with the
label `app=display-operator` and answers "no Ready sidecar on that
node" from memory.

**Authentication and authorization.** Every request carries
`Authorization: Bearer` (RFC 6750 section 2.1). The API sends the
token in a `TokenReview` with `spec.audiences: ["display-api"]` and
checks `status.audiences`, so a pod's ordinary API-server token does
not open it. The cost: a person on an OIDC kubeconfig must mint a
`ServiceAccount` token for it, which is the recipe. Then a
`SubjectAccessReview` for verb `get`, group `display.liken.sh`,
resource `displays`, subresource `screen`, name the `Display`. It
carries `user`, `groups`, `uid`, and `extra` from the review and an
empty namespace, which unit tests assert. The subresource exists in
no CRD; it is a string RBAC matches, as `pods/log` is. Every route
authorizes before it reads, so a 403 never says whether a name
exists. The documents need authentication and no authorization; the
info route needs `get` on `displays`. Verdicts are cached by the
SHA-256 of the raw token for `min(exp, 60 s)`; denials are never
cached and the key is never logged. The base ships a `ClusterRole`
`display-capture-viewer` with the one `displays/screen` rule for
owners to bind, and media-api holds that binding. The manual says
beside the grant examples that a wildcard `resources: ["*"]` role
and `cluster-admin` gain capture on the day this ships.

**RBAC for `display-api`.** A `ClusterRoleBinding` to
`system:auth-delegator` for the two reviews. A `ClusterRole` with
`get` on `displays` and `create`, `patch` on `events`. A `Role` in
`liken-system` with `list`, `watch` on `pods`; `create` on `secrets`
and `configmaps` (`create` cannot be scoped to a name); and `get`,
`update`, `watch` on the `Secret`s `display-api-tls` and
`display-capture-server` and the `ConfigMap` `display-api-ca`. The
DaemonSet's `ServiceAccount` gains a hand-rolled `create` on
`tokenreviews` only.

**TLS.** The API serves HTTPS only, because the bearer token is a
`ServiceAccount` token a pod network would carry in the clear, and a
frame of a home's screen is a picture of the room. One CA per domain.
At first start the API looks for the `Secret` `display-api-tls`
(`kubernetes.io/tls`). If absent, it mints a CA and a serving leaf
for the `Service`'s four names and `localhost`. If present, it serves
what it holds, so an owner with cert-manager owns the `Secret`. The
same CA signs the sidecar leaf, SAN `display-capture`, into the
`Secret` `display-capture-server`; the two leaves are told apart by
their SANs, and the API verifies the sidecar under that name because
pod IPs are not names. The CA certificate alone goes into the
`ConfigMap` `display-api-ca`, so a client reads the trust anchor with
an ordinary `get` and never touches a `Secret`. The CA lives ten
years and the leaves one; the API re-mints a leaf under a third of
its life, and `display_api_certificate_expiry_seconds` shows the
date. CA rotation is two steps: publish the new CA appended to
`ca.crt` in the `ConfigMap`, wait, then switch the leaves.

**The record.** Every request that produces bytes writes a Kubernetes
`Event` on the `Display`: `reason: Captured`, `type: Normal`, the
subject and aspect in the message, so `kubectl describe display`
answers who looked at a screen and when. The log line is the detail
record, one per request with the route as its RFC 6570 template, the
caller's subject, status, bytes, duration, and the request id; it
never carries the token.

### Observability

Both processes serve `liken_build_info{component,version}`,
`/metrics`, `/healthz`, and `/readyz`. The API's metrics are on
`:9200`, the port every `liken` process uses; the sidecar cannot
share 9200 in its pod, so its one listener on 9201 serves metrics
and captures together. The `route` label is the template, never the
concrete path.

| Process | Series | Type |
| --- | --- | --- |
| api | `display_api_requests_total{route,method,status}` | counter |
| api | `display_api_request_seconds{route}` | histogram, time to headers |
| api | `display_api_streams_active{aspect}` | gauge |
| api | `display_api_certificate_expiry_seconds` | gauge |
| capture | `display_capture_ready` | gauge |
| capture | `display_capture_bytes_total{aspect,format}` | counter |
| capture | `display_capture_seconds_total{aspect,format}` | counter, stream time |
| capture | `display_captures_active{aspect}` | gauge |
| capture | `display_capture_frames_total{format}` | counter |
| capture | `display_capture_failures_total{reason}` | counter: `denied`, `busy`, `compositor`, `encoder` |

The capture counters are the sidecar's only, so nothing double
counts. The dashboard gains one row: captures active, failures by
reason, API request rate by status. `deploy/monitoring` gains a
`PodMonitor` for the API's pod and this endpoint on the existing one:

```yaml
    - port: capture
      scheme: https
      # The leaf's only SAN is display-capture and its CA is minted
      # after this file is written; the series carry no capture
      # content, so the scrape skips verification.
      tlsConfig:
        insecureSkipVerify: true
      relabelings:
        - sourceLabels: [__meta_kubernetes_pod_node_name]
          targetLabel: node
```

### The recipe

v1 has no CORS, so a browser reaches the API only on the same origin
through a port-forward, and v1 ships no `kubectl` plugin. The
everyday use is `curl`:

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

## What it costs, estimated

These are workstation measurements (Intel Core Ultra 7 165H, iHD
26.1.2, ffmpeg 8.0.1), not stick1's numbers; the drill replaces them.

| Cost | Estimate |
| --- | --- |
| Sidecar idle | one static Go binary, no Wayland connection, no frame: target under 15 MB RSS and no CPU |
| 1080p clip at 15 fps, GPU conversion | 0.084 cores, 111 MB RSS, against 0.32 cores with the software fallback |
| 4K clip at 15 fps, GPU conversion | 0.49 cores, 231 MB RSS, against 1.06 cores; stick1's cores are about a quarter of the workstation's, so the fallback cannot run 4K there |
| Still | 0.07 s wall for 1080p, 0.70 s for 4K, process start included |
| Interference with mpv | planes are off for the whole clip, so mpv's film is composited through the GL renderer for its length; plan 17 measured weston at 99 millicores composing four surfaces on stick1 |

## What was considered and set aside

**A Job or sidecar running `weston-screenshooter`.** A draw claim's
socket is authorized to draw, not to capture; the tool captures every
output into one PNG on disk; a pod schedule costs seconds.

**Capture inside the module.** `weston_screenshooter_shoot()` is
`WL_DEPRECATED` in `libweston.h` and takes a buffer only a client's
`wl_shm` can make. Weston 16 deleted it.

**A screenshot as an annotation and a `Secret`.** It cost API-server
storage, a 1 MiB budget that scaled the picture down, a TTL sweep,
and a `create secrets` grant, and it had no way to carry a clip.

**`services/proxy` as the public face.** The API server's proxy
strips the caller's identity. The end state is an aggregated
`APIService`, below.

**Client certificates on the private leg.** Mutual TLS is one more
`Secret` that has to exist before a capture works, and a second
identity system beside the one Kubernetes has. The sidecar's
certificate gives encryption; a `TokenReview` gives identity.

**A `Secret` watch in the sidecar.** It costs the DaemonSet's
`ServiceAccount` a grant that an `optional` volume does not.

**A shared bearer secret over plain HTTP, or TLS at an Ingress
only.** Both put a `ServiceAccount` token, or the pixels, on the pod
network in the clear.

**A frame source shared by every consumer of one output.** It
removes the 503 for a busy output, holds two frames per output,
66 MB at 4K, and makes one client's pace another's. Below.

**Stills in Go's `image/png` and `image/jpeg`.** No process per
still, and a second code path. The drill's time to first byte is the
signal to revisit.

**`framerate` default 30.** Twice the encode cost on a stick-class
node, and planes are off for the clip's length either way.

**`xywh=` in logical pixels.** Media Fragments' `pixel:` unit is the
resource's own pixels, and `percent:` is the unit that ignores scale.

**`Warning` for the disabled planes.** RFC 9111 deprecated the
header; the manual says it instead.

## How the work is proved

Unit drills run with no compositor and no GPU. Every row of the
route and negotiation tables goes through the router, `HEAD` on an
error included. The parser drill runs every Media Fragments example,
the three percent-encoded cases of section 6.1.1 (`t=10%2C20`,
`t=%6ept:10`, `t=npt%3a10`), the clipping cases of 6.1.2, and this
plan's 400s. One round trip expands the published RFC 6570 template
and calls the result. The capture exchange runs against
a socket that plays weston's part, `complete`, `retry`, and `failed`
included, with the descriptor over `SCM_RIGHTS`. The
`SubjectAccessReview` drill asserts the four subject fields and the
empty namespace. Every route is found in the rendered OpenAPI
document. `layout/smoke.sh` gains a client on the capture socket
that reads `complete` and one on `wayland-0` that reads `failed`
with `unauthorized`, the privacy proof.

The drill on liken-1, on stick1, records these when the plan closes:

| Number | How |
| --- | --- |
| Sidecar idle RSS and CPU | `kubectl top pod --containers` an hour after the roll |
| Node disk added and first pull time | `crictl images` and the pod's first start after the roll |
| Time to first byte, still | `curl -w '%{time_starttransfer}'` on `screen.png` and `screen.jpg`, 1080p and 4K if the panel offers it |
| Time to first byte, clip | the same on `screen.mp4?t=,5` |
| CPU of a 1080p clip at 30 fps | `kubectl top pod --containers` for `capture` and `weston`, and the package temperature |
| CPU of a 4K clip at 30 fps | the same on a 4K mode, or recorded as not run |
| GPU busy during each clip | an i915 engine reader if one runs on the node, else unmeasured with ffmpeg's CPU as the proxy |
| Which conversion graph the node runs | ffmpeg's log: `scale_vaapi` after a `bgr0` upload, or the software fallback |
| Which fourcc the compositor reports | the sidecar's first-frame log line |
| Fence-fd path or five-refresh timer | the achieved frame rate: a ceiling near 12 fps at 60 Hz is the timer path |
| MJPEG bytes per 1080p frame at `quality=85` | `display_capture_bytes_total` over one second at 15 fps, against the 100 KB estimate |
| Whether mpv drops frames over a whole 30 fps clip | mpv's `frame-drop-count` over IPC if the player exposes it, else by eye and the player's log |
| A mode change during a clip | the response ends, the log carries both sizes |
| A second request during a clip | 503, `Retry-After: 5`, the problem document's words |
| A capture from a draw claim's socket | `failed` with `unauthorized`, 500 `capture-denied` |
| The `Captured` `Event` | `kubectl describe display` after one still |

The 4K numbers decide the `framerate` default and the memory limit.
The mpv number decides whether a capture may run while a film plays.

## What it still owes

**An aggregated `APIService`.** It needs its own API group, because
`v1alpha1.display.liken.sh` is served by the CRDs and an `APIService`
would take it over. It moves the CA into `spec.caBundle` rather than
removing it. It needs the `extension-apiserver-authentication-reader`
`Role` in `kube-system`. Its paths are a second vocabulary beside
v1's, not a move.

**A one-line client.** A `kubectl` plugin, or a `liken` CLI verb, that
opens the forward, reads the CA, mints the token, and follows a 307
into a sibling `Service` with the caller's token.

**CORS.** Preflight without credentials, `Access-Control-Allow-Origin`
from configuration, and `Access-Control-Expose-Headers` for
`Content-Location`, `Link`, and `Content-Disposition`.

**`ClusterTrustBundle`.** The k8s-native home for the trust anchors,
stable in Kubernetes 1.37; `liken` pins k3s 1.36, so it replaces the
`ConfigMap` at the next version.

**A shared frame source per output.** While a standing MJPEG viewer
holds a screen, every still on it is a 503.

**A clip that pauses when the screen does not change.** The
`framebuffer` source holds planes off and forces a repaint per frame;
`writeback` is "often not available" in Weston 14.

**Audio in a clip.** A `Display` has no sink; media-operator's plan
34 composes this API's `screen.mp4` with audio-operator's
`audio.opus`.
