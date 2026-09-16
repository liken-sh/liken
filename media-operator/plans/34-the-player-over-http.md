# 34, The player over HTTP

A `media-api` Deployment answers HTTP for a `Player`. Its `screen.*`
routes redirect to the display-operator's `display-api`, its
`audio.*` routes redirect to the audio-operator's `audio-api`, and its
`media.*` routes compose the two into one muxed stream. It is the
media instance of the capture API design that display-operator plan
22 and audio-operator plan 09 share: one shape, one vocabulary, no
shared code.

## The problem

Nothing answers "what is on this `Player` right now" in a form an
agent or another operator can consume. The display and audio APIs
will answer for a `Display` and a `Sink`. A person would have to read
`status.screen.monitor` off the `Player` and call the display route
by hand, and the film with its sound would be two streams that do not
line up.

The `Player` resolves its display: `status.screen` carries the node
and the monitor id, the `Display` name (`PlayerScreenStatus` in
`api.go`). It does not resolve its sinks. `spec.sinks` is a list of
selections (`PlayerDevice`), and `buildClaim` in `claims.go` turns
each into a claim request `audio0`, `audio1`, and so on. Nothing
writes the `Sink` the scheduler picked where a client can read it.

## The design

```
  kubectl / curl / agent / another liken API
        |  HTTPS, Bearer token
        v
  media-api             Deployment, one per cluster, in liken-system
        |  reads the Player: status.screen.monitor, status.sinks[]
        |  screen.* and audio.*: 307 to display-api or audio-api
        |  media.*: two upstream requests, one ffmpeg -c copy mux
        v
  display-api  /  audio-api      the sibling Deployments
```

`media-api` holds no hardware and has no sidecar, so it runs no pod
informer and finds no node. It reads one object, the `Player`, and
calls the two siblings as an ordinary Bearer client under its own
ServiceAccount, the pattern for one liken API calling another. A
browser is not a v1 client: there is no CORS, and a browser reaches
the API only on the same origin through a port-forward.

### The operator publishes `status.sinks[]`

```yaml
status:
  sinks:
    - request: audio0
      name: hdmi-0-pch
    - request: audio1
      name: aa-bb-cc-dd-ee-ff
```

`request` is the claim's request name and `name` is the `Sink`
object's name. The operator reads the playback claim
`<play>-devices`, keeps every `status.allocation.devices.results`
entry whose `driver` is `audio.liken.sh` and whose `request` starts
with `audio`, and copies `device` into `name`. The types exist in
`screen.go` (`DeviceRequestAllocationResult`), and `peripheralOf` in
`peripherals.go` reads the same list for the Bluetooth driver.

The device name is the `Sink` name: the audio-operator publishes each
endpoint as a device under its inventory name (`devices.go`) and
creates the `Sink` under that name (`reconcileSink` in
`sinkcontrol.go`). A Bluetooth speaker's device name is its address
in lowercase dashed form (`speakerName` in `names.go`), and so is its
`Sink`.

The list is memory, like `status.screen`. The idle claim holds the
draw device alone (`buildIdleClaim` in `idlepod.go`), so a sink is
allocated only while a `Play` runs; the operator writes the list from
an allocated playback claim and keeps it after. The tap does not
follow the memory: `audio.*` and the audio tracks of `media.*`
require a running `Play` (`status.activity` is `Playing`). Otherwise
`audio.*` answers 409 `not-playing`, and `media.*` redirects to the
screen. A remembered `Sink` that another unit is using is never
tapped through this one. The players CRD gains the field and `crdref`
regenerates the manual page.

**Set aside: `media-api` reads the claim itself.** It would need
`get` on `resourceclaims` everywhere, and between runs there is no
claim. **Set aside: `media-api` evaluates the CEL selector.** The
rejected plan `pre-flight-co-location-condition.md` states the rule:
nothing re-derives an allocation the scheduler owns.

### Routes

The shared grammar is
`/v1/{domain}/[namespaces/{ns}/]{plural}/{name}/{aspect}[.{ext}]`.
`domain` is the first label of the CRD's API group, so
`media.liken.sh` gives `media`, and a namespaced kind carries
`namespaces/{ns}` before its plural, as the Kubernetes API does. The
domain segment lets one ingress later mount every domain's API under
one host name with no path clash, and a future `video` domain fits
beside `audio`. That ingress is not v1 work. Discovery is at
`/v1/media` and OpenAPI at `/v1/media/openapi.json`. Below, `.../`
stands for `/v1/media/namespaces/{ns}/players/{name}/`.

| Route | Answer | Status | Headers |
| --- | --- | --- | --- |
| `GET /v1/media` | discovery document | 200 | `application/json`, `ETag`, `Cache-Control: no-cache`, `Vary: Accept` |
| `GET /v1/media/openapi.json` | OpenAPI 3.1 | 200 | `application/openapi+json`, `ETag`, `Cache-Control: no-cache` |
| `GET /v1/media/namespaces/{ns}/players/{name}` | the Player's capture facts | 200 | `application/json`, `ETag`, `Cache-Control: no-cache`, `Link` `related` to each capture route |
| document route with a matching `If-None-Match` | not modified | 304 | `ETag`, `Vary: Accept` |
| `GET .../screen[.ext]` | redirect to the Display route | 307 | `Location`, `Link` (`related` to `media.mp4`), `Cache-Control: no-store`, `Vary: Accept` |
| `GET .../audio[.ext]` | redirect to the Sink route | 307 | as above |
| `GET .../media` | the composed stream, negotiated | 200 | as the next row, plus `Content-Location` and `Link` `alternate` to `media.mp4` and `media.mkv` |
| `GET .../media.mp4`, `.../media.mkv` | the composed stream | 200 | `Content-Type`, `Content-Disposition`, `Cache-Control: no-store`, `Vary: Accept`, `Accept-Ranges: none`, `Link` (`https://liken.sh/rel/screen`, one `https://liken.sh/rel/audio` per track), `Transfer-Encoding: chunked` on HTTP/1.1 |
| `HEAD` on any route | the `GET`'s headers | as the `GET` | no body, no upstream call, no `codecs` parameter |
| `OPTIONS` on any route | the methods | 204 | `Allow: GET, HEAD, OPTIONS` |
| any other method | refused | 405 | `Allow`, problem document |
| no token | refused | 401 | `WWW-Authenticate: Bearer realm="media-api"` |
| the TokenReview refuses the token | refused | 401 | `WWW-Authenticate: Bearer realm="media-api", error="invalid_token", error_description="<the TokenReview's words>"` |
| the SubjectAccessReview denies | refused | 403 | `WWW-Authenticate: Bearer realm="media-api", error="insufficient_scope", scope="players/media"` |
| no such Player, or an upstream 404 | refused | 404 | problem document, `upstream` member on a relayed one |
| no `status.screen`, or no running Play for an audio aspect | refused | 409 | problem document whose `detail` names the clearing action |
| a query the grammar refuses, or an upstream 400 | refused | 400 | problem document, `upstream` member on a relayed one |
| the `Accept` excludes the extension's type, or nothing offered is acceptable | refused | 406 | problem document with `acceptable` |
| an upstream 503, a refused connection, or this API's own composition limit | refused | 503 | `Retry-After: 5` (an upstream's value relayed), problem document |
| an upstream answer that is not HTTP or not a problem document | refused | 502 | problem document `upstream-failed` |
| upstream headers take over 10 s plus `begin` | refused | 504 | problem document |
| `/healthz`, `/readyz` | liveness, readiness | 200 or 503 | `text/plain` |
| `/metrics` on `:9200` | Prometheus | 200 | `text/plain; version=0.0.4` |

Every response carries `Link: </v1/media/openapi.json>;
rel="service-desc"` and `Link: <https://media.liken.sh/docs/reference/api/>;
rel="service-doc"` (RFC 8631), and `Link: <https://kubernetes.default.svc/apis/media.liken.sh/v1alpha1/namespaces/{ns}/players/{name}>;
rel="describedby"` on every `Player` route. Every response carries
`Vary: Accept`, on the extension routes too, because RFC 9110 section
12.5.5 gives the field a second purpose: it tells a cache which request
fields the server reads, whether or not they changed the answer.
Document routes carry none of the capture headers. A `HEAD` on an
error carries no body, per RFC 9110 section 15.5.

The standards, each fetched on 2026-09-16:

* RFC 9110 (HTTP Semantics, June 2022): methods, status codes,
  `Accept` with q-values (12.5.1), `Vary`, `Allow`, 405, 406. Section
  15.4.8 defines 307: "the target resource resides temporarily under
  a different URI and the user agent MUST NOT change the request
  method if it performs an automatic redirection to that URI. Since
  the redirection can change over time, the client ought to continue
  using the original target URI for future requests." A `Player`'s
  screen moves when its claim moves, so the answer is 307 and never
  301 or 302. Section 8.7 gives `Content-Location` on a negotiated
  route as "a more specific identifier for the selected
  representation", the one sense this API uses. The field's identity
  guarantee, that a `GET` on the URI returns the same representation,
  does not hold for a live capture, so extension routes do not send
  it. Section 14.3: `Accept-Ranges: none`, because two requests for
  one URI produce two byte sequences.
* RFC 9112 (HTTP/1.1, June 2022) section 7.1: chunked transfer on
  every stream, because a live stream has no length in advance.
  HTTP/2 frames the body itself; Go's `net/http` serves it over TLS.
* RFC 9457 (Problem Details, July 2023): every error body is
  `application/problem+json` with `type`, `title`, `status`,
  `detail`, and `instance`. `detail` carries the source's own words,
  this repository's error rule in `AGENTS.md`. `instance` is the
  request path plus `#` plus the request id, which the log line
  carries too. Shared types are `https://liken.sh/problems/no-node`,
  `not-acceptable`, `capture-busy`, `upstream-failed`, `not-playing`,
  and `away`; with `about:blank`, `title` is the status phrase
  (4.2.1). Extension members (`upstream`, `acceptable`) are allowed
  by 3.2 and unknown ones are ignored.
* RFC 8288 (Web Linking, October 2017): `Link` syntax
  `<URI-Reference>; rel="..."`, with a `type` parameter that carries a
  bare media type and no parameters (3.4.1). Section 2.1.2:
  "Applications that don't wish to register a relation type can use an
  extension relation type, which is a URI that uniquely identifies the
  relation type."
* RFC 6266 (June 2011) section 4.3, advice to recipients on
  `filename`. Every capture carries `Content-Disposition: inline;
  filename="{ns}-{name}-{time}.{ext}"`. The time is RFC 3339 UTC with
  the colons replaced by hyphens, because a colon is not legal in a
  file name everywhere a browser saves; this is the one place the
  time is not in the standard form.
* RFC 9111 (HTTP Caching, June 2022): `Cache-Control: no-store` on
  every capture and redirect, `no-cache` on documents. Section 5.5
  marks `Warning` obsoleted, so a no-sink redirect is a plain 307.
* RFC 6750 (October 2012) section 3: no token gets `realm` alone; a
  refused token gets `error="invalid_token"` with the TokenReview's
  words in `error_description`; a denial gets
  `error="insufficient_scope"` with the failed check in `scope`.
* RFC 9559 (Matroska, 2024) registers `video/matroska` and names
  `video/x-matroska` as its deprecated alias.
* RFC 6570 (March 2012) level 3 templates; RFC 6381 (August 2011)
  section 3.3 for `codecs`; RFC 4337 for `video/mp4`; RFC 4287 for
  `related`; RFC 8631 for `service-desc` and `service-doc`.

### Media Fragments

`t=` and `xywh=` are W3C Media Fragments 1.0. The parser follows
section 5.1.1: split on `&` and `=` first, percent-decode second, so
`t=10%2C20`, `t=%6ept:10`, and `t=npt%3a10` parse as the spec's
section 6.1.1 says they must. `t=begin,end` is half-open, "the begin
time is considered part of the interval whereas the end time is
considered to be the first time point that is not part of the
interval". `xywh=` takes `pixel:` (default) or `percent:`; an
out-of-range region is clipped per section 6.1.2, by the display
sidecar that owns the frame.

The spec tells a user agent to ignore an invalid, unknown, or
non-existent dimension (sections 6.2, 6.2.1, 6.3.1). This API answers
400 instead, because a query produces a new resource (sections 3.1
and 7.4), and a client that asked for a region must not silently get
the whole screen. `t=a,b` with `a >= b`, a repeated dimension, an
unknown key, `width=` with `height=`, and a `begin` over
`captureBeginMax`, 60 s, are all 400.

Media Fragments fixes NPT's zero at the start of the source media
(section 6.1.1). A live tap has no start, so this API defines the
source media's zero as the instant the sidecar accepts the request.
`clock:` is in the advanced document, not in 1.0. The sidecar sends
headers at once, starts its pipeline at once, and discards frames or
samples until `begin` on its own clock, so `t=5,7` discards five
seconds and records two.

### Link relations

| Relation | Registered | On | Points to |
| --- | --- | --- | --- |
| `alternate` | IANA, referenced to HTML: "Refers to a substitute for this context" | the extensionless `media` route | `media.mp4` and `media.mkv`, each with a bare `type` |
| `related` | IANA, RFC 4287: "Identifies a related resource" | a `screen.*` or `audio.*` 307; the info document | the composed `media.mp4` with the same `t=`; each capture route |
| `describedby` | IANA, referenced to W3C POWDER | every `Player` route | the `Player` object on the API server |
| `service-desc`, `service-doc` | IANA, RFC 8631 | every response | the OpenAPI document; the manual's API page |
| `https://liken.sh/rel/screen` | extension relation, a URI per RFC 8288 section 2.1.2 | a composed response | the display-api route the video came from |
| `https://liken.sh/rel/audio` | extension relation | a composed response, once per audio track | the audio-api route each track came from |

The IANA rows come from https://www.iana.org/assignments/link-relations/
(fetched 2026-09-16). A redirect links the composed form as
`related`, because the screen with sound muxed in is a different
aspect of the `Player` and no substitute for the screen. The two
extension relations stay unregistered: registration needs a
specification and expert review, and a URI already identifies the
relation. `describedby` is absolute because a relative reference
would resolve against this server.

### Negotiation

An extension names one fixed representation. No extension negotiates
on `Accept` with q-values; no `Accept` means the aspect's default,
`video/mp4` for `media`. The negotiated route answers
`Content-Location` as an absolute path, `/v1/media/namespaces/{ns}/players/{name}/media.mp4`.
A redirect carries the extension the negotiation chose, so the
sibling negotiates nothing again. `video/x-matroska` is the deprecated
alias of `video/matroska` and matches too.

| Request | Answer |
| --- | --- |
| `GET .../media`, no `Accept` | 200 `video/mp4`, `Content-Location: .../media.mp4` |
| `GET .../media`, `Accept: video/matroska` | 200 `video/matroska`, `Content-Location: .../media.mkv` |
| `GET .../media`, `Accept: video/*;q=0.5, video/matroska` | 200 `video/matroska` |
| `GET .../media.mp4`, `Accept: image/png` | 406, `acceptable: [{type: video/mp4, href: .../media.mp4}, {type: video/matroska, href: .../media.mkv}]` |
| `GET .../screen`, `Accept: image/jpeg` | 307 to `/v1/display/displays/{d}/screen.jpg` |
| `GET .../screen`, no `Accept` | 307 to `/v1/display/displays/{d}/screen.png` |
| `GET .../audio`, `Accept: audio/ogg` | 307 to `/v1/audio/sinks/{s}/audio.opus` |

A 406's `acceptable` member lists `{"type", "href"}` pairs, and on an
extension route it lists the route's own type plus its siblings (RFC
9110 section 15.5.7).

### The composed stream

`media.mp4` is H.264 in fragmented MP4 with Opus audio. `media-api`
requests `screen.mp4` from `display-api` and `audio.opus` from
`audio-api`, with `xywh=`, `width=`, `height=`, `framerate=`, and
`quality=` forwarded to the display request and `bitrate=` to the
audio request. It validates the query first, so a 400 costs no
capture. Then:

```
ffmpeg -nostdin -loglevel error \
  -f mp4 -i pipe:3 \
  -itsoffset <offset> -f ogg -i pipe:4 \
  -map 0:v:0 -map 1:a:0 -c copy \
  -movflags frag_keyframe+empty_moov+default_base_moof \
  -f mp4 pipe:1
```

`pipe:3` and `pipe:4` are the upstream bodies as inherited file
descriptors, `pipe:1` the response body. `-c copy` copies "one input
elementary stream's packets without decoding, filtering, or encoding
them" (ffmpeg manual, fetched 2026-09-16), so the API node needs no
codec and no GPU. The formats manual (fetched 2026-09-16) documents
`frag_keyframe` as "start a new fragment at each video keyframe" and
`default_base_moof` as the flag that "avoids writing the absolute
base_data_offset field in tfhd atoms". The HTML manual has no
`empty_moov` entry; `libavformat/movenc.c` registers it as "Make the
initial moov atom empty" (fetched 2026-09-16). Because `empty_moov`
puts the `moov` first, a fragmented MP4 demuxes progressively from a
pipe: the review measured two live producers into fMP4 with the
first output byte at 2.32 s of a 10 s capture. The private leg stays
fMP4.

**Opus in MP4.** The container follows "Encapsulation of Opus in ISO
Base Media File Format", version 0.6.8 of 28 April 2016 (fetched from
opus-codec.org, 2026-09-16): an `OpusSampleEntry` with codingname
`Opus` and a `dOps` box, which `movenc.c` writes. That document has
no `codecs` section. The string comes from RFC 6381 section 3.3,
where the first element is the sample entry's four-character code
and "values are case sensitive": `video/mp4;
codecs="avc1.64001f,Opus"`, the `avc1` bytes copied from the display
upstream. Browsers took lowercase `opus` from the MSE byte-stream
convention, so the drill checks `MediaSource.isTypeSupported` for
both spellings and the manual records which browsers take which.
MDN's audio codec guide (fetched 2026-09-16) says "Safari supports
Opus in the `<audio>` element only when packaged in a CAF file", and
caniuse.com marks Safari partial through version 27. Chrome, Firefox,
mpv, and VLC play it. AAC is not in v1: `audio-api` serves none, and
an encode on the API node breaks the rule that the public face never
encodes.

**`media.mkv`.** The same streams through the `matroska` muxer with
`live` set, "Write files assuming it is a live stream" (formats
manual, 2026-09-16), as `video/matroska`. WebM is not offered: it
allows only VP8, VP9, and AV1 video.

**`HEAD`.** The `codecs` parameter is determined while generating the
content, so a `HEAD` omits it per RFC 9110 section 9.3.2 and its
`Content-Type` differs from the `GET`'s. A `HEAD` makes no upstream
call.

### Two clocks, one offset

Each sidecar's `t=` origin is the instant it accepts the request, so
the two streams' timestamp 0 are two instants. Frame or sample zero
of a body is origin plus `begin` only when the pipeline started
within `begin`. A start-up is a keyframe on one side and a PipeWire
link, or an A2DP transport opening, on the other.

So `media-api` composes with a lead-in `L` of 1 s. It asks both
upstreams for `t=begin+L,end+L`, and the composed stream is one
second behind "now"; the manual and the discovery document
(`"leadIn": "1s"`) both say so. Both sidecars then have a running
pipeline before their zero, and the correction is the difference of
the two accept instants. `media-api` measures those as the
header-arrival instants on its own clock: it opens both requests at
once, records each instant, and starts ffmpeg with `-itsoffset` on
the audio input equal to `headersAt[audio] - headersAt[video]`. The
ffmpeg manual (fetched 2026-09-16): "The offset is added to the
timestamps of the input files. Specifying a positive offset means
that the corresponding streams are delayed by the time duration
specified in offset." Until ffmpeg starts, the kernel holds each body
in its socket buffer.

Two limits. A sidecar whose pipeline takes longer than `L` to start
shifts its zero later by the excess, and the drill's residual shows
it. And `-itsoffset` does not move the first packet of the track it
shifts. In an `empty_moov` file that packet is clamped to 0, so the
drill measures the residual from a mark at least one second in. The
v1 target is a residual under one frame, 33 ms at 30 fps. ITU-R
BT.1359-1 puts the detectability thresholds at audio leading by 45 ms
or lagging by 125 ms; the target is under both.

**Set aside: `-use_wallclock_as_timestamps`** ("Use wallclock as
timestamps if set to 1", formats manual). It rewrites every packet's
timestamp with its arrival time, so network bursts become frame
timing. **Set aside: first packet timestamps.** Both start at 0 and
carry no information about the origins. **Set aside: no correction.**
Two pipeline start-ups differ by hundreds of milliseconds, so a
stated tolerance would not hold.

### Two sinks, and the stream count

A `Player` with two sinks composes both: one video track and one
audio track per sink, in `spec.sinks` order, the first as the default
track. Each track comes from its own `audio.opus` request with its
own `-itsoffset` and `-map N:a:0`, and carries its own `rel/audio`
link. Two Opus tracks produce ffmpeg's "codec frame size is not set"
warning, which is expected. The resource is the `Player`, and a
`Player` with two sinks plays through both.

**Set aside: the first sink only.** It drops a track the unit plays.
**Set aside: mix into one track.** A mix is a decode, a filter, and
an encode on the API node, and it doubles the level of a stream both
speakers play. **Set aside: 300 Multiple Choices.** The client asked
for the `Player`'s media, and the `Player` has one answer.

The same rule applies to every `media.*` request. `media-api` counts
the streams the `Player` resolves: one video for `status.screen`, and
one audio per `status.sinks` entry while a `Play` runs. More than one
composes. Exactly one redirects with 307 to that stream's own route:
a screen with no sink, or with no running `Play`, goes to the Display
route; a sink with no screen goes to the Sink route. Zero is a 409
whose `detail` says to run a `Play`.

### A worked example

```
GET /v1/media/namespaces/media/players/studio/media.mp4?t=0,10&width=960 HTTP/1.1
Host: media-api.liken-system.svc
Authorization: Bearer eyJ...

HTTP/1.1 200 OK
Content-Type: video/mp4; codecs="avc1.64001f,Opus"
Content-Disposition: inline; filename="media-studio-2026-09-16T21-02-16Z.mp4"
Cache-Control: no-store
Vary: Accept
Accept-Ranges: none
Transfer-Encoding: chunked
Link: <https://display-api.liken-system.svc/v1/display/displays/boe-1080/screen.mp4?t=1,11&width=960>;
      rel="https://liken.sh/rel/screen"
Link: <https://audio-api.liken-system.svc/v1/audio/sinks/hdmi-0-pch/audio.opus?t=1,11>;
      rel="https://liken.sh/rel/audio"
Link: <https://kubernetes.default.svc/apis/media.liken.sh/v1alpha1/namespaces/media/players/studio>;
      rel="describedby"
Link: </v1/media/openapi.json>; rel="service-desc"
Link: <https://media.liken.sh/docs/reference/api/>; rel="service-doc"
```

The `Link` fields are wrapped for reading; each is one field line on
the wire. The upstream `t=` is the caller's plus the lead-in. The
same request for `screen.png` answers 307 with an absolute
`Location: https://display-api.liken-system.svc/v1/display/displays/boe-1080/screen.png`,
because the client reaches that server with its own token, and a
relative `Link: </v1/media/namespaces/media/players/studio/media.mp4>;
rel="related"; type="video/mp4"`. A busy upstream answers 503 with
its `Retry-After` relayed and a problem document `capture-busy` whose
`detail` is the upstream's own and whose `upstream` member is its
URL. A failure after the stream began ends the response; the fault
reaches the client as a truncated body and the request log line.

### Concurrency and timeouts

The sidecars hold the rule, one capture at a time per output, and a
second composed request against one `Player` gets the upstream's 503
relayed. `media-api` adds `MEDIA_API_MAX_COMPOSITIONS`, default 4,
one ffmpeg process each; past it, 503 `capture-busy` with
`Retry-After: 5`.

An unbounded stream is unbounded on purpose. `captureBeginMax` is
60 s. The API bounds the time to upstream headers at 10 s plus
`begin`, then 504. It bounds the idle time on an upstream body at
30 s, counted from the first body byte or from `begin`, whichever is
later, so the bound never fires during `begin`. This API's bound is
the loosest link in the chain, because it wraps two upstreams that
each pay their own latency. Its own read-header timeout is 10 s, with
no write timeout on a stream.

Memory: on cgroup v2, which every `liken` machine runs, one ffmpeg
over the container limit kills the whole container cgroup, so one
oversized composition restarts `media-api` and ends every
composition with it. The limit below is sized for four.

### Auth

Every request carries a Bearer token. `media-api` validates it with a
`TokenReview` that names the audience `media-api` and checks
`status.audiences`, so a pod's default API-server token does not
open it. A token from an external OIDC issuer must carry that
audience too, which is a cost the manual states. It authorizes with a
`SubjectAccessReview` that copies `user`, `groups`, `uid`, and
`extra` from the `TokenReview` status, for verb `get` on
`players/screen`, `players/audio`, or `players/media` in
`media.liken.sh`, with the `Player`'s namespace from the path. A unit
test asserts both. Every route authorizes before it reads, so a 403
never leaks that a name exists. Discovery and OpenAPI need
authentication and no authorization. The info route needs `get` on
`players`. RBAC does not check that a subresource exists, so an owner
grants capture with the shipped `ClusterRole` `media-capture-viewer`
(`get` on the three subresources) bound per namespace, or one rule
with `resourceNames`.

Positive verdicts are cached under the SHA-256 of the raw token for
`min(exp, 60 s)`; a denial is never cached; the key is never logged.
The `ServiceAccount` binds `system:auth-delegator` for the two
reviews, the grant Kubernetes already names for this.

A redirect carries no credentials. The client follows the 307 with
its own token, and `display-api` checks `displays/screen` for that
subject, so a captor of a `Player` also needs the grant on the
`Display` and the `Sink`. The composed route is the exception:
`media-api` calls the siblings under its own ServiceAccount, and the
caller needs `players/media` alone. The manual states beside the
grant examples that `players/media` on a `Player` is a grant on that
`Player`'s `Display` and `Sink`s, and that a role with
`resources: ["*"]` in `media.liken.sh`, and `cluster-admin`, gain
capture on the day this ships.

The `ClusterRole` `media-api`: `get` on `players`; `create` and
`patch` on `events`; `get` on `displays/screen` in `display.liken.sh`
and `sinks/audio` in `audio.liken.sh`, what the siblings check when
this API calls them. No `list` or `watch` on `players`: the API reads
one `Player` per request, so a moved `status.screen` moves the next
redirect. The `Role` in `liken-system`:

```yaml
rules:
  - apiGroups: [""]
    resources: [configmaps]
    resourceNames: [display-api-ca, audio-api-ca, media-api-ca]
    verbs: [get, watch]
  - apiGroups: [""]
    resources: [configmaps, secrets]
    verbs: [create]
  - apiGroups: [""]
    resources: [configmaps]
    resourceNames: [media-api-ca]
    verbs: [update]
  - apiGroups: [""]
    resources: [secrets]
    resourceNames: [media-api-tls]
    verbs: [get, update, watch]
```

Every request that produces bytes writes a Kubernetes `Event` on the
`Player`: `reason: Captured`, `type: Normal`, the subject and the
aspect in the message, so `kubectl describe player` answers who
looked and when. The log line is the detail record, and it never
carries the token or any hash of it.

### TLS

`media-api` serves HTTPS, because a Bearer token travels in every
request and the cluster network carries no encryption of its own.
One CA per domain. At first start the API mints a self-signed CA,
ten years, and a serving leaf for `media-api.liken-system.svc`, one
year. The key and cert go to the Secret `media-api-tls`; the CA
certificate alone goes to the ConfigMap `media-api-ca`, public data a
client with no Secret access reads. `display-api` and `audio-api`
publish theirs the same way, and `media-api` trusts them through
their ConfigMaps `display-api-ca` and `audio-api-ca`, which it
watches. It re-mints a leaf when under a third of its life remains
and publishes `media_api_certificate_expiry_seconds`. Rotation is two
steps: publish the new CA appended to `ca.crt` in the ConfigMap,
wait, then switch the leaf. An owner with a CA replaces both objects;
the API never overwrites a pair that exists, and a create that loses
the race reads the winner.

The sibling base URLs are configuration, `MEDIA_API_DISPLAY_URL` and
`MEDIA_API_AUDIO_URL`, defaulting to the `.svc` names. Every
`Location` is built from them, so an owner who exposes the APIs under
public names sets two variables.

### The Deployment

`deploy/api.yaml`, in the base beside `operator.yaml`: a Deployment
`media-api` with `replicas: 1` and `strategy: Recreate`, so two pods
never mint at once. It runs `args: [api]` under ServiceAccount
`media-api`, with env `MEDIA_API_ADDRESS=:8443`,
`MEDIA_METRICS_ADDRESS=:9200`, the two sibling URLs, and
`MEDIA_API_MAX_COMPOSITIONS=4`; ports `https` 8443 and `metrics`
9200; a readiness probe on `/readyz`; capabilities dropped; requests
`10m` and `32Mi`, limit `256Mi` for four ffmpeg processes. A Service
`media-api` answers on 443. `/readyz` is a latching startup check. It
answers 200 once the TLS pair is loaded and one `TokenReview` of the
API's own token succeeded, and never calls the API server per probe;
a later failure is a metric and a log line.

`api` is a sixth role of the one binary, so it shares `apiclient.go`
and the metrics base in `metrics.go`. The image is new,
`ghcr.io/liken-sh/media-operator-api`, from `Dockerfile.api` with the
base pinned the way `Dockerfile.player` pins mpv:
`ARG FFMPEG_BASE=ghcr.io/liken-sh/ffmpeg:<display release>`. The
operator's image is a static binary on scratch, and the mux needs
ffmpeg; plan 19 measured that layer at 218 MB, 88 MB compressed.
`release.yaml` gains the build step, a gate that runs `ffmpeg
-version` in the image, the push, and the roll line.
`deploy/monitoring/podmonitor-api.yaml` selects `app: media-api` on
`metrics` with the operator's node relabeling. The dashboard gains
one row: streams active, upstream failures by status, and API request
rate by status.

### Discovery

`GET /v1/media` answers the shared document shape, keyed by plural:

```json
{
  "resources": {
    "players": {
      "self": "/v1/media/namespaces/{namespace}/players/{name}",
      "aspects": {
        "screen": {"template": "/v1/media/namespaces/{namespace}/players/{name}/screen{.ext}{?t,xywh,width,height,framerate,quality}",
                   "mediaTypes": ["image/png", "image/jpeg", "video/mp4", "multipart/x-mixed-replace"],
                   "extensions": ["png", "jpg", "mp4", "mjpeg"], "redirects": true},
        "audio":  {"template": "/v1/media/namespaces/{namespace}/players/{name}/audio{.ext}{?t,bitrate}",
                   "mediaTypes": ["audio/wav", "audio/flac", "audio/ogg"],
                   "extensions": ["wav", "flac", "opus"], "redirects": true},
        "media":  {"template": "/v1/media/namespaces/{namespace}/players/{name}/media{.ext}{?t,xywh,width,height,framerate,quality,bitrate}",
                   "mediaTypes": ["video/mp4", "video/matroska"],
                   "extensions": ["mp4", "mkv"], "redirects": false, "leadIn": "1s"}
      }
    }
  },
  "openapi": "/v1/media/openapi.json"
}
```

The `screen` and `audio` lists are copied from the siblings'
documents at request time. The info route answers the `Display` name
and node, each `Sink` name, whether a `Play` runs, and the stream
count, with `related` links to every capture route, so a client
learns first whether `media.mp4` will compose or redirect.

The OpenAPI 3.1 document enumerates each extension path as its own
path item, with no `{.ext}`. The served copy injects `servers:
[{url: ...}]` from the configured public base, else the request's own
origin; the committed copy holds a placeholder, and the equality test
ignores `servers`. Its type, `application/openapi+json`, is
provisional: `draft-ietf-httpapi-rest-api-mediatypes` registers it
and IANA does not list it yet. `go generate` writes the document
from the router, it is committed, and the manual renders it at
`docs/content/docs/reference/api.md` with one page per problem
`type`. Document routes carry `ETag` set to the build version and
answer `If-None-Match` with 304.

### Metrics and logs

On `:9200`, milestone 65's port, with
`liken_build_info{component="media-api"}` from `newBaseMetrics`:

| Metric | Type | Labels |
| --- | --- | --- |
| `media_api_requests_total` | counter | `route`, `method`, `status` |
| `media_api_request_seconds` | histogram | `route` (header time) |
| `media_api_streams_active` | gauge | `aspect` |
| `media_api_upstream_requests_total` | counter | `upstream`, `status` |
| `media_api_compose_offset_seconds` | histogram | the measured header offset |
| `media_api_certificate_expiry_seconds` | gauge | none |

The capture counters (`_capture_bytes_total` and its kin) are emitted
by the sidecars only, so nothing double-counts, and this API has no
sidecar. `route` is the RFC 6570 template, never the concrete path,
so no namespace or name enters a label. One structured log line per
request: the request id, the template, method, the TokenReview
subject, status, bytes, header time, stream time, and on a
composition the upstream URLs, the offset, and any ffmpeg stderr
tail.

### The everyday use

```sh
TOKEN=$(kubectl create token media-api-client -n media --audience media-api --duration 10m)
kubectl get configmap media-api-ca -n liken-system -o jsonpath='{.data.ca\.crt}' > media-api-ca.crt
kubectl port-forward -n liken-system svc/media-api 8443:443 &
curl --cacert media-api-ca.crt -H "Authorization: Bearer $TOKEN" \
  --resolve media-api.liken-system.svc:8443:127.0.0.1 \
  "https://media-api.liken-system.svc:8443/v1/media/namespaces/media/players/studio/media.mp4?t=0,10" \
  -o studio.mp4
```

The composed route works through one forward, because `media-api`
fetches the upstreams itself. A redirect does not: its `Location`
names another Service, so the caller opens a second forward to that
Service and repeats the request with a token whose audience is that
API. `curl -L` drops `Authorization` on a host change and
`--location-trusted` keeps it; the manual states both.

### What the operator changes

`api.go` gains `PlayerSinkStatus{Request, Name}` and
`PlayerStatus.Sinks`, `operate.go` derives the list in
`reconcilePlayers` beside the screen memory, `main.go` gains the
`api` role, and the API is new files under the 500-line limit, one
per concern. `deploy/`, `Dockerfile.api`, `release.yaml`, and the
manual follow.

## What was set aside

**One gateway for three domains.** A gateway would hold display and
audio logic in a repository that owns neither. **A fourth container
in every playback pod.** An idle `Player` has no pod, and a
screenshot of an idle unit is a normal request. **Plain HTTP in the
cluster.** A Bearer token on a plaintext hop is readable by any pod
on the path. **A transcode to AAC for Safari.** An encode on the API
node; an open problem. **`Warning: 299` on the no-sink redirect.**
RFC 9111 obsoleted the header. **A `kubectl` plugin.** None in v1 in
any of the three repositories; the redirect-following client is an
open problem below.

## How it is proved

The unit tests run the router against an `httptest` server in place
of the siblings. They cover every route-table row and negotiation
example, a redirect's `Location` and `Link` byte for byte, and the
stream-count rule for zero to three streams with and without a
running `Play`. They check the upstream `detail` copied through, the
`-itsoffset` from two header instants, and the `SubjectAccessReview`
body with a group-bound subject and the path's namespace. One round
trip expands the published RFC 6570 template and calls the result.
The composition test runs a fixture ffmpeg over two short fixture
streams and checks with ffprobe: one video track, two audio tracks,
in order.

The drill runs on `liken-1` with a `Player` on `stick-1`, the Apollo
Lake node, so the sidecar cost lands there and the API cost on
`liken-1`:

1. Apply the release. Confirm `status.sinks` after one `Play` and
   that it stays after the `Play` retires.
2. The clapper. Generate a lab clip with ffmpeg's `lavfi` sources,
   `testsrc2` with a full-white frame every 2 s and `sine` gated to a
   50 ms burst at the same instants, 30 fps, 20 s. No library title
   is involved. Play it on the `Player`.
3. `GET media.mp4?t=0,10`. Read the log line's offset. Run ffprobe
   `-show_frames` for each white frame's `pts_time` and
   `silencedetect` for each burst onset, and take the difference at
   the second mark and later; the first mark is the clamped packet.
   Repeat five times.
4. `GET media.mkv?t=0,10`; play both captures in mpv and Firefox, and
   run `MediaSource.isTypeSupported` in Firefox and Chrome for
   `codecs="avc1.64001f,Opus"` and `codecs="avc1.64001f,opus"`.
5. `GET screen.png` and `GET audio.wav`: one `curl -v` for the 307,
   then a second `curl` to the `Location` with a token for that
   API's audience. Check each `Location` and `Link`.
6. The parser: `t=10%2C20`, `t=%6ept:10`, `t=npt%3a10` each answer as
   `t=10,20` and `t=10` do; `t=10,10`, `t=5,3`, `t=1&t=2`, and
   `t=61,70` each answer 400.
7. `media.mp4` on a `Player` with no sinks, and on one with sinks but
   no running `Play`: a 307 to the screen. `audio.wav` on the second:
   a 409 `not-playing`. Two composed requests at once: a 503 in the
   display sidecar's words. `kubectl describe player` shows a
   `Captured` event per capture.

| Measurement | Where it comes from |
| --- | --- |
| measured header offset, five runs | `media_api_compose_offset_seconds`, the log line |
| residual audio/video offset at marks two and later, five runs | ffprobe, white frame minus burst onset |
| time to first byte of `media.mp4` | `curl -w %{time_starttransfer}`, expected near 1 s plus the upstream's own |
| time to first byte of `screen.png` through the two calls | `curl -w` on each |
| CPU of the ffmpeg mux at 1080p30 | `kubectl top pod`, `ps` in the container |
| RSS of `media-api` idle and during one composition, and of ffmpeg | `kubectl top pod`, `ps` |
| `isTypeSupported` per spelling per browser | the browser console |
| whether the capture stalls mpv on `stick-1` | `media_dropped_frames_total` on the playback pod |

Pass: residual under 33 ms on every run, mux under 5% of one core,
no dropped frames the capture alone explains.

## Open problems

* **The aggregated `APIService`.** It needs its own group, because
  `media.liken.sh` is served by the CRDs and an `APIService` for it
  would take the group over. It moves the CA into `spec.caBundle`
  rather than removing it, it needs the
  `extension-apiserver-authentication-reader` `Role` in `kube-system`,
  and its paths under `/apis/` are a second vocabulary, not a move of
  this one.
* **A one-line client.** A `kubectl` plugin, or a `liken` CLI verb,
  that opens the forward, reads the CA, mints the token, and follows a
  307 into a sibling Service with the caller's token, so a `Player`
  capture is one command.
* **CORS.** A browser needs a preflight `OPTIONS` answered without
  credentials, `Access-Control-Allow-Origin` from configuration, and
  `Access-Control-Expose-Headers` for `Content-Location`, `Link`, and
  `Content-Disposition`.
* **AAC for Safari.** An Opus decode and AAC encode on the API node,
  about a tenth of a core per stream, as a second form or an `Accept`
  with `codecs="avc1,mp4a.40.2"`.
* **`ClusterTrustBundle`.** The k8s-native home for the three CA
  anchors, stable in Kubernetes 1.37; `liken` pins k3s 1.36, so it
  is a next-version item that removes the ConfigMaps and the `get`.
* **Two replicas.** The CA mint needs a `Lease`, and a composition is
  bound to one process.
* **Redirects outside the cluster.** A `.svc` `Location` reaches
  in-cluster clients; public names need the two URL variables. A
  proxying redirect was set aside because it hides the sibling's
  identity check.
* **The offset correction depends on the siblings.** Plans 22 and 09
  must send headers at the accept instant and discard until `begin`
  with a running pipeline; the drill's residual is the check.
