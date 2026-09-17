# The screen over HTTP

Plan 22. Built on 2026-09-16, and drilled on liken-1 on 2026-09-16
and 2026-09-17.

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
| GET, HEAD | `/v1/display/displays/{name}/screen.mp4` | `video/mp4; codecs="avc1.640029"` | H.264 in fragmented MP4, until hang-up or the `t=` end |
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
streams like a clip. HTTP/2 (RFC 9113) frames the body itself.

The sidecar takes the first frame before the status line goes out,
and sends the status line and the headers as soon as that frame is in
hand, milliseconds after accept. The compositor's answer to the first
capture request is where a denial arrives, and a denial after a 200
has no status left to carry it, so the first frame is the check that
turns `unauthorized` into a 500. The headers wait for nothing else:
media-api composes this stream with sound by comparing the arrival of
the two streams' headers, so headers that waited out a `t=` lead-in
would read as a clock difference of the lead-in's length. `type` on a `Link` carries no
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
| 500 | the compositor denied the capture, `unauthorized`, type `capture-denied`; the encoder could not start, type `encoder-failed`, with ffmpeg's last error line as `detail` | none, a retry never clears it | section 15.6.1 |
| 502 | the sidecar answered something that is not HTTP or not a problem document | | section 15.6.3 |
| 503 | the output is being captured, type `capture-busy`; the compositor is not serving, type `compositor-down`, with the `CompositorServing` condition's message as `detail`; the sidecar refused the connection, is absent, is not ready, or presented a leaf the API does not trust, type `upstream-failed`; the `Display` has no `status.node` yet, type `no-node` | `Retry-After: 5` | sections 15.6.4 and 10.2.3 |
| 504 | the sidecar sent no headers within the header timeout | | section 15.6.5 |

No route accepts a body, so 415 is never sent; a `POST` gets 405 and
`Allow`. 409 is reserved for a state the caller can act on, and a
`Display` waiting for its node is not one.

An encoder that started and then wrote nothing cannot become a 500,
because the status line is already on the wire. The body ends without
its terminating chunk, so the caller reads an unexpected end of file,
which is the one thing that tells a truncated capture from a complete
one, and the log carries ffmpeg's last line. Only an encoder that
cannot start is a 500.

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
advanced document, not in 1.0 (section 4.2.1). The sidecar takes the
first frame, sends the headers, starts its pipeline, and discards
frames until `begin` on its own clock, so frame zero of the body is
origin plus `begin`. `t=5,7` discards five seconds, then records two;
`t=,10` records ten from now; `t=5` on a still discards five seconds
and takes one frame.

Media Fragments tells a user agent to ignore an invalid, unknown, or
non-existent dimension (sections 6.2, 6.2.1, 6.3.1). This API answers
400 instead, because a query produces a new resource and a client that
asked for a region must not silently get the whole screen. A repeated
dimension, an unknown key, `t=a,b` with `a >= b`, and a `t=` end on a
still are all 400. An `xywh` that runs off an edge is clipped per
section 6.1.2, not refused. The one region section 6.1.2 clips to
nothing is a rectangle whose origin is at or past an edge of the
screen, and that is a 400, for the same reason: the client asked for
a region and must not silently get a strip of the edge. `percent:`
rounds the origin down and the size up (section 6.1.2, whose
published formula transposes the operands). Every crop then rounds
its width and height up to even, because the encoder takes no odd
dimension; an origin that no longer fits moves back, and a frame
whose own size is odd rounds down instead.

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
under `https://display.liken.sh/problems/` (`capture-denied`,
`compositor-down`, `encoder-failed`), because only this domain has a
compositor and an encoder. With `about:blank`, `title` is the status
phrase (RFC 9457 section 4.2.1). `detail` carries the source's own
words: a weston `failed` event's `msg`, the `CompositorServing`
condition's message, or ffmpeg's last error line. A 401 and a 403
carry a `detail` too. A sidecar the API cannot reach is described by
the node and the class of the failure, "the capture sidecar on
`stick-1` did not present a certificate this API trusts"; the pod
address, the port, and the path on the private leg are the shape of
the cluster, so they go to the log line and never to the caller.

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
answers `name`, `node`, `width`, `height`, `scale`, `refresh`,
`formats`, `codecs`, and `conversion`, from the sidecar's own
`wl_output` events and its startup probe, with `rel="related"` links
(RFC 4287, registered) to its capture routes. A screen whose
compositor is not serving, or whose sidecar the API cannot reach,
answers 200 from the `Display` object alone: the name, the node, and
the size and refresh its status reports, with `compositor: down` or
`sidecar: unreachable` and the condition's words in `detail`. A
caller asking what a screen is does not need the screen to be up,
and `scale`, `formats`, and `conversion` come from the node, so they
are absent rather than guessed.
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
one output at a time, unmapped when the request ends. The sidecar
logs one line at the first frame of every capture: the fourcc, the
`-pixel_format` derived from it, the size and scale the output
reported, the socket, and the conversion graph, so a drill reads the
node's own answer there.

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
  -use_wallclock_as_timestamps 1 -thread_queue_size 8 -i pipe:0 \
  -init_hw_device vaapi=gpu:/dev/dri/renderD128 -filter_hw_device gpu \
  -vf 'hwupload,scale_vaapi=w=1920:h=-2:format=nv12' \
  -c:v h264_vaapi -profile:v high -level 4.1 -g 15 -bf 0 -flush_packets 1 \
  -f mp4 -movflags frag_keyframe+empty_moov+default_base_moof -frag_duration 1000000 pipe:1

# the low-end stream
ffmpeg ... -vf 'hwupload,scale_vaapi=format=nv12' -c:v mjpeg_vaapi -global_quality 85 -jfif 1 \
  -g 15 -bf 0 -f mpjpeg pipe:1
```

`h264_vaapi` takes only `vaapi` frames (ffmpeg 8.0.1), so `hwupload`
is in every clip's graph, with its device from `-init_hw_device` and
`-filter_hw_device` (ffmpeg.org/ffmpeg.html, 2026-09-16). The color
conversion runs on the GPU in `scale_vaapi=format=nv12` where the
node's driver has VA-API post-processing, and on the CPU in
`format=nv12,hwupload` where it does not. The sidecar probes its
conversion graph once at startup: it encodes one synthetic 64 by 64
frame through `hwupload,scale_vaapi` and `h264_vaapi` to nothing, and
a failure selects the software conversion for the life of the
process, because a driver does not gain a pipeline while a pod runs.
Apollo Lake's iHD driver has no VA-API post-processing, so `stick-1`
converts on the CPU and `liken-1` converts in `scale_vaapi`. Each node
states which graph it took and why in one startup log line, reports
it as the gauge `display_capture_conversion{graph}`, 1 on the graph it
runs and 0 on the other, and in the info document's `conversion`
member. `CAPTURE_CONVERSION=software` or `=vaapi` overrides the probe
in either direction. Both graphs scale to the same size: `scale_vaapi`
or `scale` is where `width=` lands, and a clip on a 4K output is
scaled to 1080p unless `width=` asks for more. `-g <framerate> -bf 0`
puts a keyframe every second,
because `h264_vaapi` defaults to a 120-frame GOP and `frag_keyframe`
("Fragment at video keyframes") cuts only there; `-frag_duration` is
the second cut. `empty_moov` ("Make the initial moov atom empty") and
`default_base_moof` ("Set the default-base-is-moof flag in tfhd
atoms") let a browser or mpv play the stream as it arrives.
`-thread_queue_size` is bounded so a slow encoder cannot grow the
process. A clip pins `h264_vaapi` to profile high and the level the
encoded size needs, 4.1 up to 1920x1080 at 60 fps and 5.1 above, so
the `Content-Type` states `codecs="avc1.640029"` (RFC 6381) before the
first byte, and media-api copies it into the composed stream's own
type. `-framerate` is the nominal rate, and each raw frame is stamped
by the wall clock, `-use_wallclock_as_timestamps 1`, because a node
that cannot hold the cadence writes fewer frames than it promised,
and without the wall clock ffmpeg stamps them as though it had held
it, so every event in the clip drifts earlier: at 30 fps on `stick-1`,
marks two seconds apart in the source landed 1.7 seconds apart in the
body. MJPEG is the `mpjpeg` muxer,
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
`Secret`. Until the files exist, the sidecar serves a self-signed leaf
of its own making, so the liveness probe's handshake succeeds and the
kubelet does not restart it; `display_capture_ready` is 0, because the
API trusts only the CA it published, and the API answers 503 with
`Retry-After: 5`. A sidecar that still holds the projected leaf after
the `Secret` alone is deleted keeps answering 200, because the leaf is
still valid; the 503 appears only where the sidecar also restarted and
found no files. On a cgroup v2 machine, an ffmpeg that exceeds the
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
`display-capture-viewer` with two rules for owners to bind: `get` on
`displays/screen`, and `get` on `displays`, because the info route
reads the `Display` itself. media-api holds that binding. The manual says
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
answers who looked at a screen and when. An `Event` is namespaced and
a `Display` is not, so the `Event`s land in `default`, the convention
a `Node`'s own `Event`s follow, and `involvedObject` carries the
`Display`'s `uid`, which is what `kubectl describe` searches by. The
log line is the detail
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
| capture | `display_capture_conversion{graph}` | gauge, 1 on `software` or `vaapi` |
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

A port-forward carries a still well and a stream badly. It is one
TCP connection through the API server, and on liken-1 it moved about
2 Mbit/s, where a 1080p MJPEG stream needs about 15 Mbit/s, so a
viewer on the forward sees the stream at about an eighth of real
time. The manual says to read a stream from a pod on the cluster
network, or through a `Service` the cluster owner exposes.

## What it costs, estimated

These are workstation measurements (Intel Core Ultra 7 165H, iHD
26.1.2, ffmpeg 8.0.1), not stick-1's numbers; the drill table under
"How the work is proved" holds stick-1's.

| Cost | Estimate |
| --- | --- |
| Sidecar idle | one static Go binary, no Wayland connection, no frame: target under 15 MB RSS and no CPU |
| 1080p clip at 15 fps, GPU conversion | 0.084 cores, 111 MB RSS, against 0.32 cores with the software fallback |
| 4K clip at 15 fps, GPU conversion | 0.49 cores, 231 MB RSS, against 1.06 cores; stick-1's cores are about a quarter of the workstation's, so the fallback cannot run 4K there |
| Still | 0.07 s wall for 1080p, 0.70 s for 4K, process start included |
| Interference with mpv | planes are off for the whole clip, so mpv's film is composited through the GL renderer for its length; plan 17 measured weston at 99 millicores composing four surfaces on stick-1 |

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
strips the caller's identity.

**An aggregated `APIService`.** A spike on 2026-09-17 registered a
throwaway extension server in front of this API and measured it
through the API server. The aggregation layer streams: every chunk
passed at its own size about 35 ms later, and every header, query
form and problem body survived. Two things stopped it. The API
server's `--request-timeout` cuts every stream at 60 s with no
terminating chunk, and in Kubernetes 1.36 the only exemption is a
hardcoded set of verbs and subresources (`watch`, `proxy`, `log`,
`exec`, `attach`, `portforward`); `?timeout=` can only shorten the
deadline. Keeping the grammar would mean raising the timeout for the
whole API server, and staying under the default would mean a `proxy`
or `watch` segment in every path. The API server also refuses any
3xx with a `Location`. Chris ruled that the duration limit and the
path it would force are showstoppers, so this hand-rolled front door
stays.

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

Two drills ran on liken-1, both against `boe-1080-display` on
`stick-1`, an Intel Celeron J3455 (Apollo Lake, 4 cores, 1.5 GHz)
with iHD 25.2.3, VA-API 1.22, and ffmpeg 8.0.1, at 1920x1080@60
showing the media browser's home. The first drill, 2026-09-17 02:14
to 02:45 UTC, ran build `2026.09.16-001-dev-002` through a
port-forward, and took its clip numbers with `CAPTURE_CONVERSION=software`
set by hand, because that build had no probe and every clip on the
GPU graph answered 200 with an empty body. The second drill, 03:08 to
03:20 UTC, ran build `-dev-004` with no knob set, and read clips and
the stream from a pod on the cluster network. The second `Display`,
`gsm-7716-lg-hdr-wqhd` on `liken-1`, has no panel and its compositor
is down, which is the only screen over 1080p in the lab.

| Number | Measured |
| --- | --- |
| Sidecar idle RSS and CPU | First drill, from the container cgroup: 1 millicore and 8.4 MiB `memory.current` over 9 idle seconds; `/proc` `VmRSS` 16.2 MB at start, 20.0 MB after captures, `VmHWM` 29.1 MB. Second drill, `kubectl top`: 2 millicores and 8 MiB idle. The estimate was under 15 MB RSS and no CPU: the CPU holds, the RSS does not. Read 20 minutes after the roll, not an hour, because the pod restarted twice during the first drill. |
| Node disk added and first pull time | First drill: `ghcr.io/liken-sh/display-capture` is 180,963,891 bytes (172.6 MiB) unpacked, the only new image on `stick-1`. First pull 2.88 s on `stick-1` and 480 ms on `liken-1`. |
| Time to first byte, still | First drill, through the port-forward: `screen.png` 1.18, 0.85, and 0.97 s over three calls, 1,240,302 bytes; `screen.jpg` 0.65, 0.84, and 0.57 s, 82,417 bytes. From a pod on the cluster network `screen.png` was 0.76 s to first byte and 0.82 s in total, so about 0.4 s of the port-forward reading is the forward. No 4K panel: not run. |
| Time to first byte, clip | First drill, port-forward: `screen.mp4?t=,5` 0.95 s, 5.64 s in total, 415,975 bytes. Second drill, cluster network: 0.375 s, 5.100 s in total, 416,241 bytes, 73 frames, 4.867 s of video, a keyframe at every second and five fragments. `screen.mp4?t=5,7`: first byte 5.148 s, 30 frames, duration exactly 2.000000 s. `screen.mjpeg?t=,3`: 0.362 s. |
| CPU of a 1080p clip at 30 fps | First drill, `cpu.stat` per second from the container cgroups, software conversion: `capture` 1874 millicores, `weston` 132 millicores, `capture` memory 100 MiB, package 71 to 76 C against 61 to 65 C idle. At 15 fps: `capture` 1117 millicores, `weston` 57 millicores, up to 69 C. Second drill, `kubectl top` over a 45 s clip at 15 fps: `capture` 798 to 1198 millicores, `weston` 26 to 62 millicores. About one core at 15 fps and about two at 30 fps on this chip. |
| CPU of a 4K clip at 30 fps | Not run. The only screen over 1080p is `gsm-7716-lg-hdr-wqhd`, which has no panel and whose compositor is down. |
| GPU busy during each clip | Unmeasured. No i915 engine reader runs on the node, and ffmpeg's CPU is the proxy. |
| Which conversion graph the node runs | First drill: the GPU graph fails on `stick-1`, `Failed to create processing pipeline config: 12 (the requested VAProfile is not supported)`, and `h264_vaapi` itself runs, `VAProfileH264High`, `VAEntrypointEncSliceLP`. Second drill: the startup line reads `has no VA-API post-processing, so this node converts on the CPU` on `stick-1` and `converts in scale_vaapi` on `liken-1`; `display_capture_conversion` reads `software` 1 on `stick-1` and `vaapi` 1 on `liken-1`; the info document reads `"conversion": "software"`. |
| Which fourcc the compositor reports | Second drill, the first-frame line: `fourcc AR24 (0x34325241) as bgra`. |
| Fence-fd path or five-refresh timer | Not the timer path. First drill: a 15 s clip at `framerate=30` held 441 frames over 14.7 s, 30.0 fps, against the timer path's ceiling near 12 fps at 60 Hz; at `framerate=15`, 222 frames over 14.8 s. |
| MJPEG bytes per 1080p frame at `quality=85` | First drill: 135,491 bytes per frame, every frame the same size, 43 frames and 5,693,320 bytes over 3.06 s, 14.9 Mbit/s. Second drill: 135,431 bytes per frame, 42 frames and 5,690,800 bytes over 3.083 s, 14.8 Mbit/s. The estimate was about 100 KB and 12 Mbit/s, so a frame costs a third more. |
| Whether mpv drops frames over a whole 30 fps clip | Not run. No `Play` ran on the screen during either drill. |
| A mode change during a clip | First drill: `spec.mode` moved to `1280x720@60` four seconds into a `t=,20` clip, the body ended at 61 frames and 4.07 s, and the status stayed 200. The end line now carries the size the clip encoded at and the size the screen serves; the second drill did not run a mode change. |
| A second request during a clip | First drill: 503, `Retry-After: 5`, type `capture-busy`, `detail` `HDMI-A-1 is being captured until its client closes`. |
| A capture from a draw claim's socket | First drill: a second `capture` process on the draw socket answered 500, type `capture-denied`, `detail` `unauthorized`. |
| Whether a clip ever puts two captures in flight | Never. No `sequence` protocol error and no client disconnection in the compositor's log over 1,334 captured frames in the first drill, two 15 s clips and a 30 s clip, and none over 898 in the second. |
| The `Captured` `Event` | Second drill: `kubectl describe display` lists five, in the namespace `default`, and a search by `involvedObject.uid` finds the same five. |
| A lost sidecar leaf | Second drill. With the `Secret` alone deleted, stills stay 200 and the API re-mints it 34 s after the delete. With the `Secret` and the pod deleted, the new sidecar self-signs and the API answers 503, `Retry-After: 5`, `detail` `the capture sidecar on stick-1 did not present a certificate this API trusts`; the `Secret` is re-minted 29 s after the delete and the first 200 arrives 69 s after that, 98 s after the delete. The 69 s are the kubelet's own projected-volume sync, which nothing in this operator drives. |
| An encoder that cannot build its graph | Second drill, with `CAPTURE_CONVERSION=vaapi` forcing the graph the chip cannot build: `screen.mp4?t=,2` answered 500 in 0.99 s with ffmpeg's words, and `screen.mjpeg` the same, where the first drill's build answered 200 with `content-length: 0`. |
| A stream through the recipe | First drill: the same 3 s MJPEG request took 25.07 s through the port-forward at 0.25 MB/s and 3.09 s from a pod on the cluster network at 1.84 MB/s, with 3.06 s of capture in both. |
| The errors | Second drill: 401 with `detail` `no Authorization field carries a Bearer token`; 403 with `detail` `not allowed to get displays/screen on boe-1080-display`; the down screen 503 with type `compositor-down` and the `CompositorServing` condition's message as `detail`; the info route 200 under the viewer role alone. First drill: every 400 in the table above, 404, 405, 406 with four `acceptable` entries, `HEAD` in 0.156 s taking no frame, and every selection query at the size it asked for. |

## What it still owes

**A failed atomic commit for every captured frame.** On `stick-1`
weston logs `atomic: couldn't commit new state: Invalid argument` and
`repaint-flush failed: No such file or directory` once per frame this
API takes: 1,334 pairs against 1,334 frames in the first drill, 898
against 898 in the second, and no other repeated message. The cause
is the disabled planes. weston 14.0.2's `output-capture.c` calls
`weston_output_disable_planes_incr()` while a capture is pending,
`compositor.c` then skips `assign_planes` and puts every view on the
primary plane, and that repaint's atomic commit comes back `EINVAL`
from this chip. The second line prints `strerror(errno)` after the
state has been freed, so its errno is whatever the free path left and
not a second cause. `drm.c` then calls
`weston_output_schedule_repaint_reset`, which drops the output out of
the repaint loop until the next capture or client commit schedules
another one. The captured frames are correct, because the renderer
composed them before the commit. What the kernel objects to is not in
weston's log, and whether the panel's own scanout lags during a clip
was not measured. The one avoidance a client holds is to take the
next capture only after the previous one retired, which the protocol
already requires and this client already does.

**A `framerate` default the stick's CPU decides.** With the
conversion on the CPU, a 1080p clip costs about one core at the
default of 15 fps and about two at 30 fps on `stick-1`, with the
package at 76 C over a 15 s clip at 30 fps. The 4K numbers were to
decide the default, and they were not run.

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
