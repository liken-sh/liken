# 09, The sound over HTTP

Plan 09. Built on 2026-09-16, and drilled on liken-1 on 2026-09-16 and
2026-09-17.

An HTTP API that taps what a `Sink` plays and what a `Source` hears,
and streams it as WAV, FLAC, or Ogg Opus. It is the audio instance of
a design three operators share: a small `<domain>-api` `Deployment`
that finds the target's node and forwards the request, and a capture
container in the domain's `DaemonSet` pod that reads the data plane.
The display operator's plan 22 and the media operator's plan 34 are
the other two instances. The three share one route shape, one
vocabulary, and the same standards, and no code.

## The problem

Nothing in the cluster can answer "what is playing on the kitchen
speaker" with the sound itself under a grant a person can read. A
person who wants to hear a sink, a test that must assert a tone
reached the speakers, or a rule that must record a microphone for five
seconds has no door that RBAC governs. The claim-delivered socket is
an existing side door: `config/50-pipewire-access.conf` marks the
socket unrestricted and `config/51-access-rules.conf` lifts every
client to all permissions, so any pod that holds any audio claim on a
node can already tap that node's microphone, with no RBAC and no
record. This API adds an RBAC front door with an audit record. It does
not close the side door; closing it is an open problem below, and
until then a `Source` grant is a courtesy and the manual says so.

## The design

```
  kubectl / agent / media-api
        |  HTTPS, Bearer token
        v
  audio-api             Deployment, one per cluster, in liken-system
        |  reads Sink.status.node, finds the audio-operator pod on that node
        v
  capture               the fifth container in the audio-operator pod
        |  pw-record on the pod's PipeWire socket, an encoder, chunked HTTPS
        v
  PipeWire              the sink's monitor ports, or the source's output ports
```

`audio-api` finds the node and forwards the stream. It reads
`status.node` for the machine that holds the endpoint and
`status.nodeName` for the PipeWire node it asks the container for; the
CRD descriptions state which is which. The `capture` container holds
every fact about PipeWire and every encoder. Nothing is stored on
either side.

### The routes

The path grammar the three APIs share is
`/v1/{domain}/[namespaces/{ns}/]{plural}/{name}/{aspect}[.{ext}]`.
`domain` is the first label of the CRD's API group, so `audio.liken.sh`
gives `audio`; a namespaced kind puts `namespaces/{ns}` before its
plural, as Kubernetes does. The domain segment lets one ingress later
mount every domain's API under one host name with no path clash, and
lets a future `video` domain with sinks and sources of its own fit
beside `audio`. That ingress is not v1 work; in v1 each API is its
own `Service`. Format is chosen by the extension or by `Accept`, the
span by a W3C Media Fragments `t=` in the query, and a knob that only
changes the representation also goes in the query.

| Route | Answer |
| --- | --- |
| `GET /v1/audio` | the discovery document, `application/json` |
| `GET /v1/audio/openapi.json` | the OpenAPI 3.1 document |
| `GET /v1/audio/sinks/{name}` | the sink's format and the routes that tap it |
| `GET /v1/audio/sinks/{name}/audio` | what the speakers play now, negotiated by `Accept` |
| `GET /v1/audio/sinks/{name}/audio.wav` | PCM in a RIFF WAVE stream |
| `GET /v1/audio/sinks/{name}/audio.flac` | FLAC |
| `GET /v1/audio/sinks/{name}/audio.opus` | Ogg Opus |
| `GET /v1/audio/sinks/{name}/audio.wav?t=0,5` | five seconds, then the stream ends |
| `GET /v1/audio/sinks/{name}/audio.wav?t=5,7` | discard five seconds, then two seconds |
| `GET /v1/audio/sinks/{name}/audio.opus?bitrate=128` | Opus at 128 kbit/s |
| `GET /v1/audio/sources/{name}` | the source's format and the routes that tap it |
| `GET /v1/audio/sources/{name}/audio[.ext]` | what the microphone hears, the same forms |

`HEAD` answers with the `GET`'s headers, takes no sample, and makes
no call to the capture container (RFC 9110 section 9.3.2); a `HEAD`
on an error carries no body (section 15.5). `OPTIONS` answers `204`
with `Allow: GET, HEAD, OPTIONS` (sections 9.3.7 and 10.2.1). Any
other method is `405` with the same `Allow` (section 15.5.6). No route
accepts content, so `415` never occurs.

| Status | When | Headers | From |
| --- | --- | --- | --- |
| `200` document | `/v1/audio`, `openapi.json`, an info route | `Content-Type`, `ETag`, `Cache-Control: no-cache`, `Vary: Accept`, `Link` | RFC 9110 sections 8.8.3 and 12.5.5, RFC 9111 section 5.2.2.4 |
| `304` | `If-None-Match` matches a document's `ETag` | `ETag`, `Vary: Accept` | RFC 9110 sections 13.1.2 and 15.4.5 |
| `200` tap | a tap runs | `Content-Type`, `Vary: Accept`, `Cache-Control: no-store`, `Accept-Ranges: none`, `Content-Disposition`, `Link`, `Transfer-Encoding: chunked` on HTTP/1.1, and `Content-Location` on the negotiated route | RFC 9110, RFC 9111 section 5.2.2.5, RFC 9112 section 7.1, RFC 6266, RFC 8288 |
| `204` | `OPTIONS` | `Allow: GET, HEAD, OPTIONS` | RFC 9110 section 10.2.1 |
| `400` | a `t=` the grammar refuses, `t=a,b` with `a >= b`, a begin over `captureBeginMax`, a repeated dimension, an unknown query parameter, a knob the format does not take | `application/problem+json` | RFC 9457; a deliberate departure from Media Fragments, below |
| `401` | no token: `WWW-Authenticate: Bearer realm="audio-api"` alone; a token the `TokenReview` refuses: `error="invalid_token"`, `error_description` with the review's own words | `WWW-Authenticate` | RFC 9110 section 15.5.2, RFC 6750 section 3 |
| `403` | the `SubjectAccessReview` says no | `WWW-Authenticate: Bearer realm="audio-api", error="insufficient_scope", scope="sinks/audio"` | RFC 9110 section 15.5.4, RFC 6750 section 3.1 |
| `404` | no `Sink` or `Source` of that name, or PipeWire holds no node for it | `application/problem+json` | RFC 9110 section 15.5.5 |
| `405` | a method other than the three | `Allow` | RFC 9110 section 15.5.6 |
| `406` | `Accept` excludes every representation the route serves | `application/problem+json` with `acceptable` | RFC 9110 sections 12.5.1 and 15.5.7 |
| `409` | the object is away: no `status.node`; `detail` says to power the device on | `application/problem+json`, type `away` | RFC 9110 section 15.5.10 |
| `500` | the tap's link landed on a node other than the one asked for, or made no link within 3 s; the status line comes after the confirmation, so no `200` precedes it | `application/problem+json`, type `wrong-target` | RFC 9110 section 15.6.1 |
| `502` | the capture container answered something that is not HTTP or not a problem document | `application/problem+json` | RFC 9110 section 15.6.3 |
| `503` | the container is at its tap limit, refused the connection, is not ready, has no certificate yet, PipeWire refused `pw-record`, or PipeWire did not answer `pw-dump` at all | `Retry-After: 5`, `application/problem+json` | RFC 9110 sections 15.6.4 and 10.2.3 |
| `504` | the container sent no headers within the header timeout | `application/problem+json` | RFC 9110 section 15.6.5 |

`409` is used only where the caller can act, and the `detail` says
what clears it. Every route authorizes before it reads, so a `403`
never reveals that a name exists.

Every error is an RFC 9457 problem document. `type` is a URL: the
shared types `no-node`, `not-acceptable`, `capture-busy`,
`upstream-failed`, and `away` under `https://liken.sh/problems/`, and
this domain's `wrong-target` under `https://audio.liken.sh/problems/`.
With `about:blank`, `title` is the status phrase (section 4.2.1).
`instance` is the request path plus `#` plus the request id, and the
log line carries the same id. `detail` carries the source's own words
verbatim, by the rule in `AGENTS.md`: `pw-record`'s stderr, an
encoder's stderr, or the API server's `status.message`. A `406`
carries `acceptable`, a list of `{"type", "href"}`; on an extension
route it lists that route's own type and its siblings with their
URIs (section 15.5.7).

Documents carry `ETag` (the build version, since the router table is
compiled in) and `Cache-Control: no-cache`, and answer `If-None-Match`
with `304`. Taps carry `Cache-Control: no-store` and `Accept-Ranges:
none`, because a live capture has no byte identity: offset 44 of two
requests is two different moments (RFC 9110 section 14.3).
`Content-Disposition: inline; filename="<name>-<time>.<ext>"` names a
browser's save (RFC 6266), with the RFC 3339 UTC time's colons
replaced by dashes because a colon is not legal in a file name
everywhere a browser saves; this is the one place the time is not in
the standard form:
`usb-0573-1573-a34004801402-usb-audio-2026-09-16T21-02-16Z.wav`.

`Vary: Accept` is on every response, extension routes included, for
section 12.5.5's second purpose: it tells the recipient the response
was subject to negotiation, where `Accept` could turn a `200` into a
`406`. `Content-Location` is on the negotiated route only, as an
absolute path, under section 8.7's clause that it is "a more specific
identifier for the selected representation"; 8.7's identity
guarantee, that a `GET` on that URI would return the same
representation, does not hold for a live capture and is not claimed.

Every response carries `Link: </v1/audio/openapi.json>;
rel="service-desc"` and `Link: <https://audio.liken.sh/docs/reference/api/>;
rel="service-doc"` (RFC 8631), and `rel="describedby"` absolute to
`https://kubernetes.default.svc/apis/audio.liken.sh/v1alpha1/sinks/{name}`,
because a relative reference would resolve against the wrong server
(RFC 8288 section 3.1). The extensionless route adds `rel="alternate"`
for each fixed form, and an info document adds `rel="related"` (RFC
4287, registered) to its capture routes. `type` carries no media type
parameters (RFC 8288 section 3.4.1). Only registered relations are
used; an extension relation would go under `https://liken.sh/rel/`.

    Link: <https://kubernetes.default.svc/apis/audio.liken.sh/v1alpha1/sinks/kitchen>; rel="describedby",
          </v1/audio/sinks/kitchen/audio.flac>; rel="alternate"; type="audio/flac",
          </v1/audio/sinks/kitchen/audio.opus>; rel="alternate"; type="audio/ogg"

(wrapped for reading; one field line on the wire)

### The negotiation

An extension names one fixed representation. With no extension,
`Accept` chooses by RFC 9110 section 12.5.1 with q-values, ties
broken by the server's preference order (WAV, FLAC, Opus), and no
`Accept` means `audio/wav`.

- WAV is `audio/wav`. The IANA registry's only RIFF WAVE name is
  `audio/vnd.wave` from RFC 2361, informational, 1998 (checked
  2026-09-16), which no browser, ffmpeg, or mpv sends; the WHATWG MIME
  Sniffing Standard names the RIFF signature `audio/wave` (section
  6.2). `audio/wav` is what clients use, so it is the name sent, and
  `wav`, `wave`, `x-wav`, and `vnd.wave` match as one representation.
- FLAC is `audio/flac`, RFC 9639 section 12.1; `audio/x-flac` is its
  deprecated alias and matches too.
- Ogg Opus is `audio/ogg; codecs=opus`: RFC 5334 registers `audio/ogg`
  with the `codecs` parameter, and RFC 7845 section 9 adds `opus` and
  the `.opus` extension. A plain `audio/ogg` matches it.

On `GET /v1/audio/sinks/kitchen/audio`:

| `Accept` | Answer |
| --- | --- |
| absent, `*/*`, or `audio/*` | `200`, `audio/wav`, `Content-Location: /v1/audio/sinks/kitchen/audio.wav` |
| `audio/ogg; codecs=opus, audio/flac;q=0.5` | Opus, the higher q |
| `audio/flac;q=0.5, audio/ogg;q=0.5` | FLAC, the server's order at equal q |
| `audio/wav;q=0, */*` | FLAC: `q=0` excludes WAV, the wildcard admits the rest |
| `audio/mpeg` | `406`; `acceptable` lists the three with their `href`s |

On `audio.wav`, `Accept: audio/flac` is `406` listing `audio.wav`,
`audio.flac`, and `audio.opus`; `Accept: audio/vnd.wave` is WAV.

### Time

`t=` is the Media Fragments temporal dimension in NPT (section 4.2.1):
`t=begin,end`, `t=begin`, or `t=,end`; the interval is half-open,
`[begin, end)`. The query form is section 3.1, "a URI query produces a
new resource", and section 7.4, which says a query approach may
change the media type and gives a fragment retrieved as a JPEG through
`Accept` as its example. The parser follows section 5.1.1: split on
`&` and `=` first, percent-decode second, so `t=10%2C20`,
`t=%6ept:10`, and `t=npt%3a10` (section 6.1.1) parse.

Media Fragments fixes NPT's zero at the start of the source media
(section 6.1.1). A live tap has no start, so this API defines the
source media's zero as the instant the capture container accepts the
request. The `clock:` format that would say this directly is in the
advanced document, not in 1.0.

Media Fragments tells a user agent to ignore an invalid, unknown, or
non-existent dimension (sections 6.2, 6.2.1, 6.3.1). This API answers
`400` instead, because a query produces a new resource and a client
that asked for a span must not silently get something else. A repeated
dimension is `400`, and `t=a,b` with `a >= b` is `400`.

The origin is the instant the container accepts the request. It sends
the headers at once, starts `pw-record` at once, discards samples until
`begin` on its own clock, and stops the encoder at `end`, so sample
zero of the body is origin plus `begin` whenever the pipeline started
within `begin`. `t=5,7` discards five seconds, then records two.
`captureBeginMax` is 60 s; a larger `begin` is `400`. An absent `end`,
or an absent `t=`, means until the client closes. The API's header
deadline on the private leg is 10 s plus `begin`, and it bounds only
the wait for the container's headers: the timer stops when they
arrive, and the body runs under the idle timeout alone. That timeout
is 30 s, counted from the first body byte or from `begin`, whichever
is later, so it never fires during `begin`.

### The capture container

The fifth container, `capture`, is the operator binary in a fourth
mode, `audio-operator capture`, beside `declare` and
`endpoints-registered`. It is a regular container beside `operator`,
so it starts after the PipeWire and WirePlumber sidecars pass their
probes. It listens on 9201, named `capture`, and serves the tap
routes, `/healthz`, `/readyz`, and `/metrics` on that one listener;
the last three need no token. 9200 is the operator's, by `liken`'s
one-port-per-pod rule.

**How a tap reads PipeWire.** `stream.capture.sink` is PipeWire's own
key, "try to capture the sink output instead of source output"
(`pw_keys`, read 2026-09-16), and WirePlumber's
`src/scripts/lib/common-utils.lua` reads it to link the stream to the
sink's monitor ports. `pw-record` has no flag for it and no `--monitor`; `-P` puts it in
the stream properties, `--target` sets `target.object`, and `-` with
`--raw` writes raw samples to stdout (`pw-cat` source, GitHub mirror,
read 2026-09-16). A sink tap is

    pw-record -P '{ node.name = "audio-capture-<request id>", stream.capture.sink = true }' \
        --target <node> --raw --format s16 --rate <rate> --channels <channels> -

and a source tap is the same line with `node.name` alone in the
properties, on the pod's socket at `/var/run/audio.liken.sh/pipewire-0`
through the `runtime` volume. The `node.name` is the name the tap's
own node takes in the graph, built from the request id, so a person
reading `pw-dump` sees which request a stream belongs to and nothing
about what was captured.
`pw-record` never refuses a bad target: with the property set and an
unknown name it links to the default sink's monitor, and without the
property a sink name links to a microphone. So the container resolves
the node from `pw-dump` before the tap, the read `pipewire.go` already
makes, and answers `404` when the graph has no node of that name.
After the stream starts, and before it writes the status line, it
confirms from the graph that the link from the stream named
`audio-capture-<request id>` landed on the requested node. The stream
is found by that name because PipeWire sets no
`application.process.id` on a client outside the pod's PID namespace,
the same reason `config/51-access-rules.conf` marks every client of
this socket `flatpak`. Each look is one `pw-dump`; the first comes
20 ms after the start and the interval doubles up to 250 ms, for up to
3 s. A link elsewhere, or none within 3 s, is `500` `wrong-target`
with no `200` before it. A `pw-dump` that cannot reach the daemon is
`503` with its words, because PipeWire is down and that clears on its
own.

**The formats.** Every tap is s16le at the endpoint's own rate and
channel count. `deploy/crds.yaml` declares no format on a `Sink`, and
`nodes.go` leaves `audio.channels` unset so PipeWire takes the count
from the hardware, so the container reads both from the graph it
already dumped: the node's `Format` while it runs, else the channel
count the node's `EnumFormat` reports for the hardware and the graph's
`clock.rate` from the `settings` metadata. A suspended 5.1 sink is
never guessed as stereo. The values used go in the info document and
the log line. 16 bits is the width every player reads, and audioconvert
resamples nothing when the graph's rate matches, which is the running
case; a mid-tap graph rate change survives (measured: 1,519,304 of
1,536,000 bytes across a forced 48000 to 44100 to 48000 change, no
stderr).

- **WAV.** The Go code writes the 44-byte header with the RIFF and
  `data` sizes `0xFFFFFFFF`, then copies stdout. ffmpeg's WAV muxer
  writes the same `-1` placeholders and never returns to fix them on
  a pipe (`libavformat/wavenc.c`, `riffenc.c`, read 2026-09-16), so
  every player that reads an ffmpeg-piped WAV reads this one.
- **FLAC.** stdout piped into `flac --force-raw-format
  --endian=little --sign=signed --bps=16 --channels=<n>
  --sample-rate=<r> --stdout -`. The STREAMINFO total-samples field
  stays 0, which RFC 9639 defines as unknown. `flac` prints
  `WARNING, cannot write back MD5 sum when encoding to stdout` on
  every tap; that line is expected and is not a failure.
- **Ogg Opus.** stdout piped into `opusenc --raw --raw-bits 16
  --raw-rate <r> --raw-chan <n> - -`, plus `--bitrate` from the
  `bitrate=` knob (6 to 256 kbit/s per channel, `400` on WAV or FLAC).
  `opusenc(1)`: "The default for input with a sample rate of 44.1 kHz
  or higher is 64 kbit/s per mono stream and 96 kbit/s per coupled
  pair."

Both encoders buffer stdout through stdio, so a silent sink would
deliver nothing until the end. The closure carries coreutils'
`/usr/libexec/coreutils/libstdbuf.so`, and the container runs `flac`
and `opusenc` with `LD_PRELOAD` naming it and `_STDBUF_O=0`, which is
what `stdbuf -o0` does.

**The closure.** `audio-closure.sh` gains `/usr/bin/pw-cat` with its
`pw-record` link, `/usr/bin/flac`, `/usr/bin/opusenc`, and
`libstdbuf.so`; the Dockerfile installs `flac` and `opus-tools`. The
five seeds add 3,100,123 bytes to a closure of 25,626,562 bytes
without them, 3.1 MB and 12.1 percent, measured in the build; on the
node, `du` of `/usr` reads 3,584 KiB more, which is 3.67 MB. `libopus`
is already there, pulled by `libspa-codec-bluez5-opus.so`; the largest
part is `libsndfile`, which drags in `libvorbisenc`, `libvorbis`,
`libmpg123`, and `libmp3lame`, four codec libraries `--raw` never
opens. The comment on the seed list states the tap as the reason
`pw-cat` is in and counts that tree.

**The container's route** mirrors the public one with the extension
always present. It owns node resolution, `t=`, `bitrate=`, the
encoders, `400`, `404`, `500`, and `503` with `pw-record`'s words.
The API relays status, headers, and body unchanged and adds `Vary`,
`Content-Location`, `Link`, and `Content-Disposition`.

**Concurrency.** Taps on different endpoints, and several on one
endpoint, run at once: PipeWire links each stream to the monitor ports
itself, so audio taps do not share the display sidecar's
one-capture-per-output rule. The container caps its own load at
`CAPTURE_TAPS`, default four, and the fifth is `503` with
`Retry-After: 5`.

**A suspended sink plays silence.** A tap on a suspended node answers
`200` with silence at the endpoint's rate. The route is "what the
speakers play now", and silence is that answer; `503` would report a
failure the service does not have. The monitor link makes the node
run (observed: suspended, running, idle across a tap), so
`status.format` appears while the tap lasts, and on a Bluetooth
speaker the A2DP transport opens. A muted `Source` taps as silence:
its mute is in front of the ports a tap reads, so a closed microphone
stays closed to this door. A muted `Sink` taps at the level it was
sent, because a sink's monitor ports carry what the sink receives and
`spec.mute` is applied after them; the manual says so, and mute is not
a way to keep a sink's sound off this route.

**Authorizing the API.** The API sends its own `ServiceAccount` token,
projected with audience `audio-capture` and a ten-minute life. The
container runs a `TokenReview` with that audience, checks that
`status.audiences` contains it, and accepts when
`status.user.username` is `system:serviceaccount:liken-system:audio-api`.
The cache key is the SHA-256 of the raw token; a positive verdict is
kept for `min(exp, 60 s)`, a denial is never cached, and the key is
never logged. This is the Kubernetes mechanism for "who is calling
me", with no minted secret to mount and no rotation of its own.

**The private leg is HTTPS.** Sound from a room's microphone must not
cross the pod network in the clear. The container serves TLS with a
leaf the API signs from the domain's one CA into `Secret`
`audio-capture-server` in `liken-system`, SAN `audio-capture`; the API
dials the pod IP with that `ServerName` and the CA as trust anchor.
The leaf reaches the pod as a `Secret` volume with `optional: true`,
reloaded on file change, so a `Secret` that does not exist yet holds
nothing in `ContainerCreating` and the `DaemonSet`'s `ServiceAccount`
needs no `get` or `watch` on it. While the file is absent the
container mints one self-signed leaf and serves it, so the liveness
probe passes; no client trusts that leaf, so `audio_capture_ready` is
0 and the API reports `503` with the TLS failure as the reason. The
API checks for the `Secret` every minute and re-mints an absent leaf,
so an owner who deletes it gets it back within a minute and the
kubelet refreshes the optional volume after that.

**Envelope.** Idle, one Go process listening: 9 Mi by `kubectl top`
and no CPU (`/proc` reads about 22 MB `VmRSS`, of which the pages the
mapped binary shares with the pod's other containers are most; the
container's own cost is the `kubectl top` figure). Each tap adds one `pw-record` and, for FLAC or Opus, one encoder.
On cgroup v2 the kubelet's default sets `memory.oom.group`, so one
encoder over the limit kills the whole container: every tap on the
node ends, the container restarts, and nothing else in the pod is
touched. The pod's limits become 64 + 128 + 128 + 128 + 64 = 512Mi on
a machine that may have 1 GB. A pipeline that dies mid-tap ends the
body without its terminating chunk on both legs, the HTTP/1.1 signal
for an incomplete message (RFC 9112 section 8); over HTTP/2 the
stream is reset. The log line survives the abort and carries the exit
status of each process and the last line it wrote. The exit status
decides an encoder failure, not stderr: both encoders print a banner
and a progress bar there and exit 0 on every successful tap.

```yaml
        # A restart of this container ends every running capture on
        # the node and takes nothing else with it. No readiness probe:
        # a probe here would decide the pod's Ready condition, and a
        # late capture credential must not stall the hardware
        # DaemonSet's rollout. Capture readiness is the API's 503 and
        # the audio_capture_ready gauge.
        - name: capture
          image: ghcr.io/liken-sh/audio-operator:latest
          args: ["capture"]
          env:
            - name: PIPEWIRE_RUNTIME_DIR
              value: /var/run/audio.liken.sh
            - name: XDG_RUNTIME_DIR
              value: /var/run/audio.liken.sh
            - name: CAPTURE_ADDRESS
              value: ":9201"
            - name: CAPTURE_TAPS
              value: "4"
          ports:
            - name: capture
              containerPort: 9201
          livenessProbe:
            httpGet:
              path: /healthz
              port: capture
              scheme: HTTPS
            periodSeconds: 60
            failureThreshold: 3
          securityContext:
            capabilities:
              drop: ["ALL"]
            privileged: false
            allowPrivilegeEscalation: false
          resources:
            requests:
              cpu: 10m
              memory: 16Mi
            limits:
              memory: 64Mi
          volumeMounts:
            - name: runtime
              mountPath: /var/run/audio.liken.sh
            - name: capture-tls
              mountPath: /var/run/audio-capture-tls
              readOnly: true
      volumes:
        - name: capture-tls
          secret:
            secretName: audio-capture-server
            optional: true
```

The container names no claim; it needs only the socket, on a hostPath
the pod already holds.

**The access rules.** No new line. `config/51-access-rules.conf`
lifts every client whose access is `flatpak`, which is every client
of this socket, because the kernel cannot translate a peer's pid
across PID namespaces. `pw-record` here is one more such client.

### The Deployment

`audio-api` is a `Deployment` in `liken-system`, one replica,
`strategy: Recreate`, the same image in a fifth mode, `audio-operator
api`. Its one shared write is the CA below, and a create that loses
the race reads the winner.

A request is authenticated with a `TokenReview` that requires the
audience `audio-api` and checks `status.audiences`, with the same
cache rules as the container. It is authorized with a
`SubjectAccessReview` for verb `get` on `sinks/audio` or
`sources/audio` in group `audio.liken.sh`, with the resource's name,
an empty namespace because both kinds are cluster-scoped, and `user`,
`groups`, `uid`, and `extra` copied from the `TokenReview` status;
unit tests assert both. An info route needs `get` on the ordinary
resource; the discovery and OpenAPI documents need authentication and
no authorization. The operator ships one `ClusterRole` for an owner
to bind, `audio-capture-viewer`, which grants `get` on `sinks`,
`sources`, `sinks/audio`, and `sources/audio`: the two plain
resources for the info routes and the two subresources for the taps.
The subresource shape lets an ordinary RBAC rule grant a tap alone,
per sink if wanted:

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

The manual states beside that example that a role with `resources:
["*"]` in `audio.liken.sh`, and `cluster-admin`, gain capture on the
day this ships. Then the API reads the object: none is `404`, no
`status.node` is `409` `away`. One informer on pods in `liken-system`
with the label `app=audio-operator` answers which pod is on the node,
from memory, and "no Ready capture container" is an instant `503`.
Every request that produces bytes writes a Kubernetes `Event` on the
`Sink` or `Source`: `reason: Captured`, `type: Normal`, the subject
and the aspect in the message, so `kubectl describe sink` answers who
listened and when. The log line is the detail record and never
carries the token.

**RBAC.** `ServiceAccount` `audio-api`, bound to `system:auth-delegator`
for the two reviews, plus a `ClusterRole` and a `Role`:

```yaml
# ClusterRole audio-api
rules:
  - apiGroups: [audio.liken.sh]
    resources: [sinks, sources]
    verbs: [get]
  - apiGroups: [""]
    resources: [events]
    verbs: [create, patch]
---
# Role audio-api, in liken-system. RBAC cannot restrict a list to a
# label selector, so list and watch cover the namespace's pods. create
# cannot be limited by name, and the program creates only these names.
rules:
  - apiGroups: [""]
    resources: [pods]
    verbs: [list, watch]
  - apiGroups: [""]
    resources: [secrets]
    verbs: [get, update]
    resourceNames: [audio-api-tls, audio-capture-server]
  - apiGroups: [""]
    resources: [configmaps]
    verbs: [get, update]
    resourceNames: [audio-api-ca]
  - apiGroups: [""]
    resources: [secrets, configmaps]
    verbs: [create]
```

The `DaemonSet`'s `ClusterRole` in `deploy/rbac.yaml` gains one
hand-rolled rule, `create` on `tokenreviews`, because the container
reviews and never authorizes.

**`Service`.** `audio-api`, ClusterIP, `443` to the container's `8443`:
`https://audio-api.liken-system.svc`.

**TLS.** At first start the API looks for `Secret` `audio-api-tls`
and, when absent, mints one CA for the domain and a public leaf with
the `Service`'s DNS names, in the `kubernetes.io/tls` shape with
`ca.crt`. It writes the CA certificate alone into `ConfigMap`
`audio-api-ca`, so a client reads the trust anchor with an ordinary
`get` and never touches the `Secret`, and signs the container leaf
from the same CA. The CA lives ten years, a leaf one year, and the API
re-mints a leaf when under a third of its life remains, on an hourly
check of the lives it holds; the sidecar `Secret` is checked every
minute, above. `audio_api_certificate_expiry_seconds` reports the
nearest expiry.
Rotation of the CA is two steps: publish the new CA appended to
`ca.crt` in the `ConfigMap`, wait, then switch the leaves. A
cert-manager owner replaces `audio-api-tls` with a `Certificate` of
the same name. HTTPS because a Bearer token on a plain listener is
replayable by anything on the path, and because Go speaks HTTP/2 only
over TLS. Over HTTP/1.1 a stream is chunked with no `Content-Length`
(RFC 9112 sections 7.1 and 6.2); over HTTP/2 it is DATA frames (RFC
9113 section 8.1). The API server's `services/proxy` door strips the
caller's identity and is not supported. v1 has no CORS: a browser
reaches the API only on the same origin through a port-forward.

**`PodMonitor`.** `deploy/monitoring/podmonitor.yaml` gains the
`capture` endpoint, and a second `PodMonitor` selects `app: audio-api`
on its `metrics` port:

```yaml
  podMetricsEndpoints:
    - port: metrics
      relabelings:
        - sourceLabels: [__meta_kubernetes_pod_node_name]
          targetLabel: node
    # The capture listener serves metrics and taps on one TLS port,
    # with a leaf signed by a CA minted after this file is applied
    # and never held by Prometheus. The series carry no capture
    # content, so the scrape skips verification.
    - port: capture
      scheme: https
      tlsConfig:
        insecureSkipVerify: true
      relabelings:
        - sourceLabels: [__meta_kubernetes_pod_node_name]
          targetLabel: node
```

### Discovery and OpenAPI

`GET /v1/audio` answers the shape the three APIs share. `{name}` is
RFC 6570 simple expansion (section 3.2.2), `{.ext}` label expansion
(3.2.5), `{?t,bitrate}` form-style query expansion (3.2.8); form-style
expansion percent-encodes the comma in `t=5,7`, which section 5.1.1's
parsing order accepts.

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

`GET /v1/audio/sinks/{name}` answers `node`, `connectionType`, the
`rate` and `channels` a tap would use, `status.format` as reported,
the formats served, and `Link: rel="related"` to each capture route,
from one API server read and one graph read.

The OpenAPI 3.1 document is served as `application/openapi+json`, a
type `draft-ietf-httpapi-rest-api-mediatypes` registers and that is
provisional until that draft publishes. It is generated from the
router: one Go table holds each route's path, methods, media types,
query parameters, and problem types, and renders both documents.
OpenAPI has no `{.ext}`, so each extension path is its own path item
with a single-entry `content` map. The served copy injects `servers:
[{url: <the configured public base, else the request's own origin>}]`;
the committed copy at `docs/static/v1/audio/openapi.json`, served at
`/v1/audio/openapi.json`, holds a placeholder, and a test asserts the
two are equal apart from `servers`. The manual's
`docs/content/docs/reference/api.md` holds the route table and the
problem types, and `docs/manual_test.go` gains the JSON path as an
exception.

### Metrics and logs

Both processes serve `liken_build_info` under `audio-api` and
`audio-capture`, plus `/healthz`, `/readyz`, and `/metrics`: the API on
9200 in its own pod, the container on 9201. The `route` label and log
field is the RFC 6570 template, never the concrete path, so no name
enters Prometheus.

| Process | Metric | Type |
| --- | --- | --- |
| audio-api | `audio_api_requests_total{route,method,status}` | counter |
| audio-api | `audio_api_request_seconds{route}` | histogram, header time |
| audio-api | `audio_api_streams_active{aspect}` | gauge |
| audio-api | `audio_api_certificate_expiry_seconds` | gauge |
| audio-capture | `audio_capture_ready` | gauge |
| audio-capture | `audio_capture_bytes_total{aspect,format}` | counter |
| audio-capture | `audio_capture_seconds_total{aspect,format}` | counter, stream time |
| audio-capture | `audio_captures_active{aspect}` | gauge |
| audio-capture | `audio_capture_failures_total{reason}` | counter: `connect`, `target`, `wrong-target`, `encoder`, `limit`, `certificate` |

The capture counters are emitted by the container only, so nothing
double-counts. `deploy/monitoring/dashboards/audio-operator.json`
gains one row: captures active, failures by reason, API request rate
by status. One log line per request: the route template, the request
id, the caller's username, the resource, status, bytes, header time,
stream time. One per tap in the container: the target and its graph
node id, the stream name, the format, the rate and channels used, the
span, the bytes, the discarded bytes, the link verdict, the start
time, the duration, and the exit status of each process.

### The one-line use

No `kubectl` plugin in v1. The recipe, through a port-forward or the
owner's ingress:

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

The audience keeps every pod's ordinary API-server token out of this
API. The cost is real for OIDC: a kubeconfig's own OIDC token carries
the OIDC audience, so a person on OIDC mints a `ServiceAccount` token
too. `mpv` in place of `curl -o` listens live.

## What was considered and set aside

- **libpipewire from Go through cgo.** The Dockerfile builds with
  `CGO_ENABLED=0`, and `pipewire.go` states why the operator execs
  `pw-dump` and `pw-cli` in place of the protocol.
- **`pw-record` piped into ffmpeg.** ffmpeg has no PipeWire input (its
  device list on 2026-09-16 names `alsa`, `jack`, `pulse`, `oss`, and
  nothing named PipeWire), so `pw-record` is needed either way, and
  `ghcr.io/liken-sh/ffmpeg` (display plan 19) is a 218 MB layer for
  three encoders that cost 3.1 MB. A second image would also break the
  one-image pattern `main.go` states.
- **Encoding in the Go process**, the other cure for stdio buffering.
  No pure-Go Opus encoder exists, and `libstdbuf.so` is one file.
- **libsndfile's encoders through `pw-record`.** It picks the format
  from the file extension, and stdout has none.
- **`audio/vnd.wave`.** Registered and unrecognized. Accepted, never sent.
- **A shared secret, mTLS, or a by-name `Secret` watch on the private
  leg.** The `TokenReview` does the job with the API server's keys,
  and an optional volume delivers the leaf with no grant on the key.
- **Plain HTTP on either leg.** A replayable token on the public leg;
  a microphone in the clear on the private one.
- **`503` on a suspended sink.** Silence is the answer.
- **One tap per endpoint.** PipeWire fans out; the cap on the
  container's load is the limit that costs something.
- **A `levels` route**, a stream of the running level. An open
  problem below.
- **An aggregated `APIService`.** A spike on 2026-09-17 measured a
  throwaway extension server in front of this API through the API
  server. It streams: a 30 s WAV tap arrived whole, chunk for chunk,
  about 35 ms behind the direct read, with every header and problem
  body intact. It cuts every stream at the API server's 60 s
  `--request-timeout` with no terminating chunk; Kubernetes 1.36
  exempts only a hardcoded set of verbs and subresources (`watch`,
  `proxy`, `log`, `exec`, `attach`, `portforward`), and `?timeout=`
  can only shorten the deadline. It also refuses any 3xx with a
  `Location`. Staying under the limit would put a `proxy` or `watch`
  segment in every path, and lifting it would change the whole API
  server. Chris ruled both showstoppers, so this front door stays.

## How the work is proved

On `liken-1`, with the USB DAC `usb-0573-1573-a34004801402-usb-audio`
and its `Source`, `usb-0573-1573-a34004801402-usb-audio-capture`:

1. Play a tone through a `Player` that holds the DAC: a `Play` whose
   item `uri` is an `https://` URL of a sixty-second 440 Hz sine WAV.
2. Tap `audio.wav?t=0,5`, `audio.flac?t=0,5`, `audio.opus?t=0,5`.
   `ffprobe` reads each as the right container at the DAC's rate and
   channels, and a spectrum shows one peak at 440 Hz.
3. Tap `audio.wav?t=5,7`: two seconds, arriving seven after the request.
4. Parser rows: `t=10%2C20`, `t=%6ept:10`, `t=npt%3a10` parse;
   `t=2&t=10`, `t=7,5`, and `t=61` are `400`; a round-trip test expands
   the published template and calls the result.
5. Tap the `Source` open, then with `spec.mute: true`: speech, then
   silence.
6. Tap the DAC with nothing playing for five seconds in each format and
   record the time to the first body byte; `status.format` appears on
   the `Sink` during the tap and leaves after.
7. Five taps at once: the fifth is `503` with `Retry-After: 5`.
8. `Accept: audio/mpeg` is `406` with three `{type, href}` entries;
   `Accept: audio/ogg` on the extensionless route is Opus with
   `Content-Location`. `If-None-Match` on `/v1/audio` is `304`.
9. An unknown name is `404`; a Bluetooth speaker whose adapter is off
   is `409` `away`; a `Sink` whose node is renamed behind the API is
   `404` from the container, never another sink's sound.
10. The capture port called directly with an ordinary `ServiceAccount`
    token is `401`; a token minted without `--audience audio-api` is
    `401` at the API.
11. Delete `audio-capture-server`: the next tap is `503` with the
    reason, and `200` once the API has minted it again and the volume
    refreshed.
12. Kill the PipeWire container during a tap: the stream ends, the next
    request is `503` with `pw-record`'s words, then `200` after the
    kubelet restarts PipeWire. `kubectl describe sink` shows a
    `Captured` event for each tap above.

| Measurement | How | Expected |
| --- | --- | --- |
| idle RSS of `capture` | `kubectl top` and `/proc/<pid>/status` from a busybox pod on the node | under 10 MB |
| idle CPU of `capture` | `kubectl top` over a minute with no tap | 0 |
| CPU of one WAV tap | `pw-record` plus the container, over thirty seconds | about 1% of a core |
| CPU of one Opus tap | the same plus `opusenc` | a few percent of a core |
| time to first body byte, no `t=`, tone playing | `curl -w '%{time_starttransfer}'` | under 200 ms |
| time to first body byte, silent sink, each format | the same | under 200 ms |
| `pw-top -b -n 1` xrun count on the DAC's node | before, during, and after a tap | unchanged |
| the closure's size | `du` of `/out` in the closure stage, before and after | 3.1 MB more |
| a tap wakes a suspended sink | `status.format` on the `Sink` | appears and leaves |

The xrun count and the ear together answer whether a tap disturbs
playback. The drill section below holds each number.

## The drill

Run on `liken-1` in two passes, both on the USB DAC
`usb-0573-1573-a34004801402-usb-audio` (PipeWire node
`liken.audio.card1-pcm0`, 48000 Hz, 2 channels) and its `Source`
`usb-0573-1573-a34004801402-usb-audio-capture` (48000 Hz, 1 channel):

- Drill 1, 2026-09-17 01:45 to 02:12 UTC, against
  `2026.09.10-001-dev-005-3413547a`, the first build. No tap produced
  a byte through the public leg: the API read `status.nodeName` where
  it needed `status.node` and the other way round, and the link
  confirmation looked for `pw-record` by `application.process.id`, so
  every tap ended `500` `wrong-target` after 3 s. The CPU rows of that
  drill were measured on the same command lines from a pod on the same
  socket.
- Drill 2, 2026-09-17 02:41 to 03:08 UTC, against
  `2026.09.10-001-dev-007-ca00d9e8`, with those two defects fixed.
  The public leg produced sound in all three formats. Every tap ended
  after 10 s plus `begin` with a clean `200`, because the header
  deadline was on the whole request context; a FLAC or Opus tap
  counted as an encoder failure for its banner on stderr; and a muted
  `Sink` tapped at full level. The build after that drill (`f285eca`)
  answers the first two as the design above states and states the
  third as the rule; it has not been drilled.

| Measurement | Expected | Drill 1 | Drill 2 |
| --- | --- | --- | --- |
| idle RSS of `capture`, `kubectl top` | under 10 MB | 5 Mi | 9 Mi |
| idle RSS of `capture`, `/proc/<pid>/status` | under 10 MB | `VmRSS` 22,848 kB, `VmHWM` 25,660 kB, 18 threads | `VmRSS` 22,396 kB, `VmHWM` 23,520 kB, 14 threads; `smaps_rollup` `Pss` 13,222 kB, `Shared_Clean` 13,816 kB, `Private_Dirty` 8,516 kB |
| idle CPU of `capture` over 60 s | 0 | 2 ticks, 0.02 s, 0.033% of a core (`kubectl top` 1m) | the same |
| CPU of one WAV tap over 30 s | about 1% of a core | 12 ticks, 0.40% of a core, `pw-record` alone | 0.842% while it runs, plus 0.182 s of CPU at the start; 1.449% across 30 s, whole cgroup |
| CPU of one FLAC tap over 30 s | not stated | 23 ticks, 0.77% of a core | 0.926% while it runs; 1.534% across 30 s |
| CPU of one Opus tap over 30 s | a few percent of a core | 82 ticks, 2.73% of a core | 3.408% while it runs; 4.076% across 30 s |
| first body byte, no `t=`, tone playing | under 200 ms | no body byte; `500` at 3.029 s | 0.474 s, 0.495 s, 0.501 s over three runs |
| first body byte, silent sink, `audio.wav?t=0,5` | under 200 ms | no body byte; `500` at 3.030 s | 0.589 s |
| first body byte, silent sink, `audio.flac?t=0,5` | under 200 ms | no body byte; `500` at 3.054 s | 0.450 s, at the start and not at the end, so the `libstdbuf.so` preload works |
| first body byte, silent sink, `audio.opus?t=0,5` | under 200 ms | no body byte; `500` at 3.042 s | 0.517 s, the same |
| first body byte, silent sink, `audio.opus?t=0,3`, after the 20 ms first look (dev-012, 2026-09-17) | under 200 ms | | 0.189 s through a port-forward |
| `pw-top -b -n 1` xruns on the DAC's node, before / during / after | unchanged | `ERR` 0 / 0 / 0 | `ERR` 0 / 0 / 0 |
| the closure's size | 3.1 MB more | `du /usr` 46,992 KiB to 50,576 KiB, 3,584 KiB (3.67 MB) more; the compressed layer 12.28 MB to 13.52 MB | not re-measured; only the Go binary changed |
| a tap wakes a suspended sink | `status.format` appears and leaves | absent, then `{"channels":2,"positions":["FL","FR"],"rate":48000}` while a monitor tap ran, then absent | the same |
| the `Secret` re-minted after a delete | `503`, then `200` | not within 2.5 min; the check was hourly, and the API was restarted by hand | 43 s to the new `Secret`; `200` again 107 s after the delete, when the kubelet refreshed the optional volume |
| the tone in the tap | one peak at 440 Hz | 439.5 Hz (bin width 2.93 Hz), RMS -15.3 dBFS, through `pw-record` alone | 439.5 Hz in WAV, FLAC, and Opus through the API, RMS -13.5, -13.5, -13.4 dBFS |

The drill 2 CPU rows come from the `capture` container's own cgroup
(`cpu.stat` `usage_usec`), so they count `pw-record` and the encoder
with the Go process; a `t=0,1` tap and a `t=0,9` tap each ran three
times, and their difference gives the rate while a tap runs. The TLS
handshake through the port-forward costs 0.09 to 0.12 s of each
first-byte figure; the API's own log read `headers=0.354` to
`headers=0.378` on every tap, so the rest is the container's node
resolution, `pw-record` start, and link confirmation.

What ran in drill 2, against the list above:

* Step 1 was adapted: no `Player` held the DAC and no reachable
  `https://` tone source existed, so a 440 Hz stereo s16 tone was
  piped into `pw-cat --playback` on the node's socket, the same graph
  a `Player` feeds.
* Steps 2 and 3 passed. `ffprobe` read `pcm_s16le 48000 Hz 2
  channels, duration 5.000000` (960,044 bytes), `flac 48000 2` with
  `duration=N/A` (STREAMINFO total samples 0, as designed), and
  `opus/ogg 48000 2, duration 5.006500`. `t=5,7` returned 384,044
  bytes, `duration=2.000000`, in 7.199 s, and the container logged
  952,320 bytes discarded, 4.96 s of samples.
* Step 4 passed, the round trip included: the template from `GET
  /v1/audio` expanded with `uritemplate` 4.2.0 to `audio.opus?t=0%2C5&bitrate=64`
  and answered `200` with 53,129 bytes against 76,323 at the default
  bitrate.
* Step 5, the muted half: a muted `Source` tapped 288,044 bytes with a
  peak absolute sample of 0.
* Steps 7, 8, 10, and 12 passed as written. The fifth tap was `503`
  at 0.203 s with `Retry-After: 5`; every negotiation row of the table
  matched; a token without the audience was `401` and one without a
  grant was `403`; a tap cut by a PipeWire kill ended at 3.310 s, the
  next request was `503` with `pw-dump`'s own words, a tap answered
  `200` again 14 s after the kill, and 33 `Captured` events were on
  the `Sink` and both `Source`s. A `HEAD` answered `200` in 0.067 s
  with PipeWire dead.
* Step 9: an unknown name was `404` from the API, and a source node
  asked as a sink, or an absent node, was `404` from the container,
  never another sink's sound.
* Step 11 passed, on the timeline in the table.

Not run, in either drill:

* The `409` `away` row. Every endpoint on the cluster held a
  `status.node`, the Bluetooth speaker was connected, and turning its
  adapter off was out of scope.
* A live `Source`. The DAC has nothing patched into its input and the
  other `Source` on the cluster reports `Capture Switch` off, so no
  microphone on the cluster carries a signal; the open tap returned
  288,044 bytes with a peak of 0, the same as the muted one.

## Open problems

- **A one-line client.** A `kubectl` plugin, or a `liken` CLI verb,
  that opens the forward, reads the CA, mints the token, and follows a
  307 into a sibling `Service` with the caller's token.
- **CORS.** Preflight without credentials,
  `Access-Control-Allow-Origin` from configuration, and
  `Access-Control-Expose-Headers` for `Content-Location`, `Link`, and
  `Content-Disposition`.
- **`ClusterTrustBundle`.** The k8s-native home for the CA anchors,
  which removes the `ConfigMap` and its grant, once `liken`'s k3s
  reaches 1.37.
- **A `levels` route.** A stream of the running level, for a rule or
  a meter that never needs the samples.
