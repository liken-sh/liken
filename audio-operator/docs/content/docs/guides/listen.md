---
title: Listen to what a speaker plays
weight: 50
description: "Tap a Sink or a Source with the kubectl liken audio capture command, or over HTTP with kubectl and curl, and save the sound as WAV, FLAC, or Opus. Use to hear what a speaker plays or what a microphone records."
---

# Listen to what a speaker plays

This guide shows you how to tap an endpoint for a fixed span and
save it as a file: what a speaker plays, or what a microphone hears,
as WAV, FLAC, or Ogg Opus. You need the operator
[installed](/docs/guides/install/) on your
[`liken`](https://liken.sh/docs/) cluster and a connected
[`Sink`](/docs/reference/sinks/) or
[`Source`](/docs/reference/sources/).

`audio-api` serves the taps. It is a `Deployment` in `liken-system`
that finds the endpoint's node and forwards the stream. The
`capture` container in the `audio-operator` pod on that node reads
the sound from PipeWire. Nothing is stored on either side. The
[API reference](/docs/reference/api/) has the full contract. This
guide is the short path through it.

## The `kubectl liken audio capture` command

The short path is the CLI. `kubectl liken audio capture` streams a
`Sink`'s sound to stdout as WAV, so a file or a pipe is a single
command:

    kubectl liken audio capture kitchen-pci-0000-00-1f-3-hdmi-0 > kitchen.wav
    kubectl liken audio capture kitchen-pci-0000-00-1f-3-hdmi-0 | mpv -

`--format flac` and `--format opus` write those forms instead, and
`--source` taps a `Source`, a microphone, in place of a `Sink`. A
`Sink` and a `Source` are cluster-scoped, so the command takes no
namespace.

The CLI authenticates with the client certificate in your
kubeconfig, the same subject `kubectl` uses, so the grant that step
1 describes is all it needs. It opens its own port-forward to
`audio-api` and reads the stream through it, so it needs no
in-cluster routing and runs from a laptop.

The endpoint argument completes to the names the cluster reports.
`kubectl` runs the plugin's completion shim on its own, so
`kubectl liken audio capture <TAB>` lists the `Sink`s. For a direct
call to `kubectl-liken-audio`, load the script with
`source <(kubectl liken audio completion bash)`.

The numbered steps below are the HTTP contract the CLI calls, for an
application in the cluster or a tap you bound with `t=`.

## 1. Who may listen

You can identify yourself with a client certificate or with a
Bearer token. The API checks for a certificate first, then for a
token, in the same order as the Kubernetes API server.

If your connection presents a client certificate signed by the
cluster's own certificate authority, you are that certificate's
subject. Your user name is the subject's common name, and your
groups are its organization values. The credentials in your
kubeconfig identify you here the same way they identify you to
`kubectl`. A certificate from any other authority ends the TLS
handshake.

If you present no certificate, send a Bearer token. The API
verifies it with a `TokenReview` that requires the audience
`audio-api`. A pod's ordinary API server token does not have that
audience, so it does not work here.

After it identifies you, the API sends a `SubjectAccessReview`
for the verb `get` on `sinks/audio` or `sources/audio` in the group
`audio.liken.sh`, with the name of the object and an empty
namespace, because both kinds are cluster-scoped. Every route
authorizes before it reads anything, so a 403 never tells you
whether a name exists.

The operator ships one `ClusterRole` for a cluster owner to bind:
`audio-capture-viewer`. It grants `get` on `sinks`, `sources`,
`sinks/audio`, and `sources/audio`: the two plain resources for the
info routes and the two subresources for the taps. Read your own
subject from your kubeconfig:

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

To bind a group instead, use `kind: Group` with one of the
certificate's organization values as the name.

An application gets the grant through its `ServiceAccount`:

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

Because the taps are subresources, you can write a narrower rule. A
rule with `resources: [sinks/audio]` and a `resourceNames` list
grants the sound of one speaker and nothing else, not even the info
document next to it. A role that grants `resources: ["*"]` in
`audio.liken.sh` already includes every tap, and so does
`cluster-admin`.

Every request that returned bytes writes a `Captured` `Event` on the
`Sink` or the `Source`.

## 2. Reach the API

`audio-api` is a `ClusterIP` `Service` at
`https://audio-api.liken-system.svc`. It serves HTTPS with its own
certificate authority. That authority's certificate is in the
`ConfigMap` `audio-api-ca` in `liken-system`, under the key
`ca.crt`.

A port-forward is a single TCP connection through the API server. A
short tap works through one, and the API reference's own
[recipe](/docs/reference/api/#examples) does that. For a long tap,
read from a pod on the cluster network, which is what the rest of
this guide does.

Put your client certificate and its key in a `Secret` that the pod
can mount:

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

The pod is in `liken-system` because a volume can only read a
`ConfigMap` or a `Secret` from the pod's own namespace. The pod
sleeps for an hour and then exits, so a pod you forget does not run
forever.

    kubectl apply -f listen-pod.yaml
    kubectl -n liken-system wait --for=condition=Ready pod/listen --timeout 60s

An application needs no `Secret`. It runs as the `ServiceAccount`
you bound in step 1, mounts a token for the API's audience, and
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

Every command below runs in the pod from step 2 and writes its file
there. `--fail-with-body` makes `curl` exit non-zero on an error and
still write the problem document, which step 4 reads.

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

Two seconds, starting five seconds from now, which skips a fade-in:

    kubectl -n liken-system exec listen -- curl -sS --fail-with-body \
      --cacert /ca/ca.crt --cert /client/tls.crt --key /client/tls.key \
      -o /tmp/later.wav \
      'https://audio-api.liken-system.svc/v1/audio/sinks/kitchen-pci-0000-00-1f-3-hdmi-0/audio.wav?t=5,7'

### The query parameters

| Parameter | What it does |
| --- | --- |
| `t=` | A W3C Media Fragments time range in seconds, as `t=begin,end`, `t=begin`, or `t=,end`. The interval is half-open. Zero is the instant the capture container accepts the request. `t=5,7` discards five seconds and then records two. A begin over 60 seconds is a 400. Without an end, or without `t=` at all, the tap runs until you close the connection, so always give an end for a file. |
| `bitrate=` | The Opus bitrate in kbit/s per channel, 6 to 256. If absent, `opusenc` picks one from the sample rate. A bitrate on WAV or FLAC is a 400. |

The extensions are `.wav`, `.flac`, and `.opus`. A path with no
extension negotiates on `Accept` and returns `audio/wav` if you send
none.

## 4. Check what you got

Copy a file out of the pod and read it with `ffprobe`:

    kubectl -n liken-system cp listen:/tmp/speaker.flac speaker.flac
    ffprobe speaker.flac

The stream has the sample rate and channel count that the endpoint's
info route reports:

    kubectl -n liken-system exec listen -- curl -sS --fail-with-body \
      --cacert /ca/ca.crt --cert /client/tls.crt --key /client/tls.key \
      https://audio-api.liken-system.svc/v1/audio/sinks/kitchen-pci-0000-00-1f-3-hdmi-0

A WAV or Opus file reports the `t=` span as its duration. A FLAC
file reports no duration, because the encoder writes the header
before the live tap ends. Decode it to measure it:

    ffmpeg -i speaker.flac -f null -

The cluster's own record of the tap is an `Event`. A `Sink` and a
`Source` are cluster-scoped, so their `Event`s are in the `default`
namespace:

    kubectl get events --field-selector reason=Captured

`kubectl describe sink` tells you who listened and when.

A tap of a speaker that plays nothing is silence, and so is a tap of
a muted microphone. A muted speaker is not silent. A sink's monitor
ports carry what the sink receives, and `spec.mute` is applied after
them, so muting a speaker silences the room and changes nothing on
this route.

### When a tap is refused

Every error is an `application/problem+json` document with `type`,
`title`, `status`, `detail`, and `instance`. `detail` quotes the
source of the error, such as the stderr of `pw-record`. `curl` wrote
the document to the output file, so read that file:

    kubectl -n liken-system exec listen -- cat /tmp/speaker.wav

| Status | What it means | What to do |
| --- | --- | --- |
| 401 | No client certificate and no token, or the `TokenReview` refused the token | Check that the `Secret` has the certificate and key from the kubeconfig you use, or mint a token for the audience `audio-api` |
| 403 | The `SubjectAccessReview` said no | Bind `audio-capture-viewer` to your subject, as step 1 shows. The `WWW-Authenticate` header names the scope you need |
| 409 | The endpoint has no `status.node` | The device is away. `detail` tells you to power it on |
| 503 | The capture container is at its tap limit, refused the connection, is not ready, has no certificate this API trusts, or PipeWire refused `pw-record` | The response has `Retry-After: 5`. Wait five seconds and try again |

## 5. Clean up

    kubectl -n liken-system delete pod listen
    kubectl -n liken-system delete secret audio-client
    rm client.crt client.key

The `ClusterRoleBinding` from step 1 is a standing grant. If the tap
was a one-off, delete it too:

    kubectl delete clusterrolebinding audio-listener

## The side door this grant does not close

The PipeWire socket is delivered to pods through claims, so any pod
with any audio claim on a node can already tap that node's
microphones and sinks, with no RBAC and no record. Until that door
is closed, a `Source` grant controls only this API.
