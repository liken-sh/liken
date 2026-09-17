---
title: Listen to what a speaker plays
weight: 50
description: "Tap a Sink or a Source over HTTP with kubectl and curl, and save a span as WAV, FLAC, or Opus. Use to hear what a speaker plays or what a microphone records."
---

# Listen to what a speaker plays

This guide taps an endpoint for a bounded span and saves it as a
file: what a speaker plays, or what a microphone hears, as WAV,
FLAC, or Ogg Opus. You need the operator
[installed](/docs/guides/install/) on your
[`liken`](https://liken.sh/docs/) cluster and a connected
[`Sink`](/docs/reference/sinks/) or
[`Source`](/docs/reference/sources/).

`audio-api` answers the taps. It is a `Deployment` in
`liken-system` that finds the endpoint's node and forwards the
stream, and the `capture` container in the `audio-operator` pod on
that node reads PipeWire. Nothing is stored on either side. The
[API reference](/docs/reference/api/) states the whole contract;
this guide is the short path through it.

## 1. Who may listen

A request names its caller two ways, and the API reads them in the
order the API server reads them.

A connection that carries a client certificate the cluster's own
authority signed names that certificate's subject. The user is the
subject's common name, and the groups are its organization values.
The credentials in your kubeconfig therefore name the same subject
here that they name to `kubectl`. A certificate from any other
authority ends the handshake.

A caller that offers no certificate sends a Bearer token, which the
API authenticates with a `TokenReview` that requires the audience
`audio-api`. A pod's ordinary API-server token does not open this
API.

Either credential is then authorized with a `SubjectAccessReview`
for the verb `get` on `sinks/audio` or `sources/audio` in the group
`audio.liken.sh`. The review names the resource and an empty
namespace, because both kinds are cluster-scoped. Every route
authorizes before it reads, so a 403 never reveals that a name
exists.

The operator ships one `ClusterRole` for an owner to bind,
`audio-capture-viewer`. It grants `get` on `sinks`, `sources`,
`sinks/audio`, and `sources/audio`: the two plain resources for the
information routes and the two subresources for the taps. Read your
own subject out of your kubeconfig:

    kubectl config view --raw --minify \
      -o jsonpath='{.users[0].user.client-certificate-data}' \
      | base64 -d | openssl x509 -noout -subject

Then bind the role to the common name that command printed:

    apiVersion: rbac.authorization.k8s.io/v1
    kind: ClusterRoleBinding
    metadata:
      name: audio-listener
    roleRef:
      apiGroup: rbac.authorization.k8s.io
      kind: ClusterRole
      name: audio-capture-viewer
    subjects:
      - kind: User
        name: <the common name>

An organization value in the same certificate binds as `kind: Group`
with that value as the name.

An application holds the grant through its `ServiceAccount`:

    apiVersion: v1
    kind: ServiceAccount
    metadata:
      name: audio-listener
      namespace: liken-system
    ---
    apiVersion: rbac.authorization.k8s.io/v1
    kind: ClusterRoleBinding
    metadata:
      name: audio-listener
    roleRef:
      apiGroup: rbac.authorization.k8s.io
      kind: ClusterRole
      name: audio-capture-viewer
    subjects:
      - kind: ServiceAccount
        name: audio-listener
        namespace: liken-system

The subresource shape lets an owner write a narrower rule instead. A
rule with `resources: [sinks/audio]` and a `resourceNames` list
grants the sound of one speaker and nothing else, not even the
information document beside it. A role with `resources: ["*"]` in
`audio.liken.sh`, and `cluster-admin`, already grant every tap.

Every request that produces bytes writes a `Captured` `Event` on the
`Sink` or the `Source`.

## 2. Reach the API

`audio-api` is a `ClusterIP` `Service` at
`https://audio-api.liken-system.svc`. It serves HTTPS under its own
authority, and that authority's certificate is in the `ConfigMap`
`audio-api-ca` in `liken-system`, under the key `ca.crt`.

A port-forward is a single TCP connection through the API server. A
short bounded tap reads well through one, and the API reference's
own [recipe](/docs/reference/api/#calling-it) does that. Read a long
tap from a pod on the cluster network, which is what the rest of
this guide uses.

Put your client certificate and its key in a `Secret` that pod can
mount:

    kubectl config view --raw --minify \
      -o jsonpath='{.users[0].user.client-certificate-data}' | base64 -d > client.crt
    kubectl config view --raw --minify \
      -o jsonpath='{.users[0].user.client-key-data}' | base64 -d > client.key
    kubectl -n liken-system create secret tls audio-client \
      --cert client.crt --key client.key

Write the pod to `listen-pod.yaml`:

    apiVersion: v1
    kind: Pod
    metadata:
      name: listen
      namespace: liken-system
    spec:
      restartPolicy: Never
      containers:
        - name: curl
          image: curlimages/curl:8.22.0
          command: [sleep, "3600"]
          volumeMounts:
            - name: ca
              mountPath: /ca
              readOnly: true
            - name: client
              mountPath: /client
              readOnly: true
      volumes:
        - name: ca
          configMap:
            name: audio-api-ca
        - name: client
          secret:
            secretName: audio-client

The pod is in `liken-system` because a volume reads a `ConfigMap`
and a `Secret` from the pod's own namespace. It sleeps for an hour
and then ends, so a forgotten pod does not run for a week.

    kubectl apply -f listen-pod.yaml
    kubectl -n liken-system wait --for=condition=Ready pod/listen --timeout 60s

An application needs no `Secret`. It runs as the `ServiceAccount`
you bound in step 1, mounts a token for the API's own audience, and
sends it as `Authorization: Bearer`:

    volumes:
      - name: token
        projected:
          sources:
            - serviceAccountToken:
                audience: audio-api
                expirationSeconds: 3600
                path: token

## 3. Take the tap

List the endpoints and pick one:

    kubectl get sinks
    kubectl get sources

Every tap below runs in the pod from step 2 and writes its file
there. `--fail-with-body` makes `curl` exit non-zero on a refusal
and still write the problem document, which step 4 reads.

Five seconds of a speaker, as PCM in a RIFF WAVE stream:

    kubectl -n liken-system exec listen -- curl -sS --fail-with-body \
      --cacert /ca/ca.crt --cert /client/tls.crt --key /client/tls.key \
      -o /tmp/speaker.wav \
      'https://audio-api.liken-system.svc/v1/audio/sinks/kitchen-pci-0000-00-1f-3-hdmi-0/audio.wav?t=0,5'

Thirty seconds of the same speaker, as FLAC:

    kubectl -n liken-system exec listen -- curl -sS --fail-with-body \
      --cacert /ca/ca.crt --cert /client/tls.crt --key /client/tls.key \
      -o /tmp/speaker.flac \
      'https://audio-api.liken-system.svc/v1/audio/sinks/kitchen-pci-0000-00-1f-3-hdmi-0/audio.flac?t=0,30'

The same span as Ogg Opus, at 96 kbit/s per channel:

    kubectl -n liken-system exec listen -- curl -sS --fail-with-body \
      --cacert /ca/ca.crt --cert /client/tls.crt --key /client/tls.key \
      -o /tmp/speaker.opus \
      'https://audio-api.liken-system.svc/v1/audio/sinks/kitchen-pci-0000-00-1f-3-hdmi-0/audio.opus?bitrate=96&t=0,30'

Ten seconds of a microphone, as WAV:

    kubectl -n liken-system exec listen -- curl -sS --fail-with-body \
      --cacert /ca/ca.crt --cert /client/tls.crt --key /client/tls.key \
      -o /tmp/microphone.wav \
      'https://audio-api.liken-system.svc/v1/audio/sources/usb-0573-1573-a34004801402-usb-audio-capture/audio.wav?t=0,10'

Two seconds, five seconds from now, which skips a fade-in:

    kubectl -n liken-system exec listen -- curl -sS --fail-with-body \
      --cacert /ca/ca.crt --cert /client/tls.crt --key /client/tls.key \
      -o /tmp/later.wav \
      'https://audio-api.liken-system.svc/v1/audio/sinks/kitchen-pci-0000-00-1f-3-hdmi-0/audio.wav?t=5,7'

### The query knobs

`t=` is the W3C Media Fragments temporal dimension in seconds, in
the forms `t=begin,end`, `t=begin`, and `t=,end`. The interval is
half-open, and its zero is the instant the capture container accepts
the request. `t=5,7` discards five seconds and then records two. A
begin over 60 seconds is a 400. An absent end, or an absent `t=`,
taps until the client closes, so a bounded tap always states an end.

`bitrate=` is the Opus bitrate in kbit/s per channel, 6 to 256.
`opusenc` chooses one from the sample rate when this is absent. A
bitrate on WAV or FLAC is a 400.

The extensions are `.wav`, `.flac`, and `.opus`. The path with no
extension negotiates on `Accept` and answers `audio/wav` when the
caller states none.

## 4. Check what you got

Copy a file out of the pod and read it with `ffprobe`:

    kubectl -n liken-system cp listen:/tmp/speaker.flac speaker.flac
    ffprobe speaker.flac

The stream reads at the rate and the channel count the endpoint's
information route reports:

    kubectl -n liken-system exec listen -- curl -sS --fail-with-body \
      --cacert /ca/ca.crt --cert /client/tls.crt --key /client/tls.key \
      https://audio-api.liken-system.svc/v1/audio/sinks/kitchen-pci-0000-00-1f-3-hdmi-0

A WAV and an Opus file report the `t=` span as their duration. A
FLAC file reports none, because the encoder writes the header before
it knows the length of a live tap. Decode it to measure it:

    ffmpeg -i speaker.flac -f null -

The cluster's own record of the tap is an `Event`. A `Sink` and a
`Source` are cluster-scoped, so their `Event`s land in `default`:

    kubectl get events --field-selector reason=Captured

`kubectl describe sink` answers who listened and when.

A tap of a speaker that plays nothing is silence, and so is a tap of
a muted microphone. A muted speaker is not: a sink's monitor ports
carry what the sink receives, and `spec.mute` is applied after them,
so muting a speaker silences the room and changes nothing on this
route.

### When a tap is refused

Every error is an `application/problem+json` document with `type`,
`title`, `status`, `detail`, and `instance`. `detail` carries the
source's own words, such as `pw-record`'s stderr. `curl` wrote the
document where the sound would have gone, so read that file:

    kubectl -n liken-system exec listen -- cat /tmp/speaker.wav

| Status | What it means | What to do |
| --- | --- | --- |
| 401 | The API read no client certificate and no token, or the `TokenReview` refused the token | Check that the `Secret` holds the certificate and key of the kubeconfig you use, or mint a token for the audience `audio-api` |
| 403 | The `SubjectAccessReview` said no | Bind `audio-capture-viewer` to your subject, as step 1 shows. The `WWW-Authenticate` field names the scope you need |
| 409 | The endpoint has no `status.node` | The endpoint is away. `detail` says to power the device on |
| 503 | The capture container is at its tap limit, refused the connection, is not ready, has no certificate this API trusts, or PipeWire refused `pw-record` | The answer carries `Retry-After: 5`. Wait five seconds and ask again |

## 5. Clean up

    kubectl -n liken-system delete pod listen
    kubectl -n liken-system delete secret audio-client
    rm client.crt client.key

The `ClusterRoleBinding` from step 1 is the standing grant. Delete
it too when the tap was a one-off:

    kubectl delete clusterrolebinding audio-listener

## The door this grant does not close

The claim-delivered PipeWire socket is an existing side door this
API does not close. Any pod that holds any audio claim on a node can
already tap that node's microphones and sinks, with no RBAC and no
record. A `Source` grant is a courtesy until that door closes.
