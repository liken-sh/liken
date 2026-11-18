---
title: Record what a player is playing
weight: 40
description: "Capture a Player's screen and sound as one MP4 or MKV with the kubectl liken media capture command, or over HTTP with kubectl and curl. Use to record what a Player is playing right now."
---

# Record what a player is playing

This guide shows you how to record a `Player`'s screen and sound
into one MP4 or MKV file. You need the operator
[installed](/docs/guides/install/) on your
[`liken`](https://liken.sh/docs/) cluster and a
[`Player`](/docs/reference/players/) that has a screen.

`media-api` serves the recording routes of every `Player`. It is a
`Deployment` in `liken-system`, next to the operator. Its `media`
routes mux the screen and the sound into one stream. Its `screen`
routes redirect to `display-api` and its `audio` routes redirect to
`audio-api`, because those operators own the hardware. The
[API reference](/docs/reference/api/) has the full contract.

## The `kubectl liken media capture` command

The quickest way to record is the CLI. `kubectl liken media capture`
writes a `Player`'s video and sound to stdout as one MP4:

    kubectl liken media capture living-room -n media > living-room.mp4
    kubectl liken media capture living-room -n media | mpv -

`--format mkv` writes Matroska instead. `-n` (or `--namespace`) sets
the `Player`'s namespace.

The CLI sends the bearer token or the client certificate of your
current kubeconfig context, so it records as the same user as
`kubectl`, and it needs only the grant in step 1. It opens its own
port-forward to `media-api`, so it works from a laptop outside the
cluster.

`kubectl liken media capture <TAB>` lists the `Player`s in the
namespace. To complete a direct call to `kubectl-liken-media`, load
the script with `source <(kubectl liken media completion bash)`.

The numbered steps below use the HTTP routes that the CLI calls. Use
them from an application in the cluster, or for a capture that the
CLI does not offer, such as a still or a cropped frame.

## 1. Who may record

You identify yourself with a client certificate or a bearer token.
`media-api` checks for a certificate first, as the Kubernetes API
server does.

A client certificate must be signed by the cluster's certificate
authority. A certificate from any other authority fails the TLS
handshake. Your user name is the certificate's common name, and your
groups are its organization values, as they are for `kubectl`.

Without a certificate, send a bearer token for the audience
`media-api`. `media-api` checks it with a `TokenReview` and refuses a
token whose `status.audiences` does not include `media-api`. A pod's
default service account token has the API server's audience, so it
does not work here.

`media-api` then sends a `SubjectAccessReview` for the verb `get` on
`players/screen`, `players/audio`, or `players/media` in the group
`media.liken.sh`, in the `Player`'s namespace.

The operator ships the `ClusterRole` `media-capture-viewer`. It grants
`get` on those three subresources and on `players`, so one binding
covers the info route and every capture. Bind it in each namespace
whose `Player`s you record. To find your user name, read the subject
of your kubeconfig's certificate:

    kubectl config view --raw --minify \
      -o jsonpath='{.users[0].user.client-certificate-data}' \
      | base64 -d | openssl x509 -noout -subject

Then bind the role to the common name that command printed, in the
namespace of the `Player`:

    apiVersion: rbac.authorization.k8s.io/v1
    kind: RoleBinding
    metadata:
      name: media-viewer
      namespace: media
    roleRef:
      apiGroup: rbac.authorization.k8s.io
      kind: ClusterRole
      name: media-capture-viewer
    subjects:
      - kind: User
        name: <the common name>

To bind a group instead, use `kind: Group` with one of the
certificate's organization values as the name.

An application gets the grant through its `ServiceAccount`:

    apiVersion: v1
    kind: ServiceAccount
    metadata:
      name: media-viewer
      namespace: media
    ---
    apiVersion: rbac.authorization.k8s.io/v1
    kind: RoleBinding
    metadata:
      name: media-viewer
      namespace: media
    roleRef:
      apiGroup: rbac.authorization.k8s.io
      kind: ClusterRole
      name: media-capture-viewer
    subjects:
      - kind: ServiceAccount
        name: media-viewer
        namespace: media

The `media` routes need only `players/media`, because `media-api`
reads the `Display` and the `Sink`s with its own `ServiceAccount`. So
a grant on `players/media` lets the subject record the `Player`'s
`Display` and `Sink`s even without a grant on them. The `screen` and
`audio` routes are different. They redirect, and you follow the
redirect with your own credentials, so `display-api` and `audio-api`
check your own grant on the `Display` and on each `Sink`. The
[display](https://liken.sh/display/docs/guides/screenshot/) and
[audio](https://liken.sh/audio/docs/guides/listen/) guides show those
grants.

A role that grants `resources: ["*"]` in `media.liken.sh` already
allows every capture, and so does `cluster-admin`.

Each request that returns bytes posts a `Captured` `Event` on the
`Player`, with the subject and the aspect in its message. A redirect
and a 503 return no bytes, so they post none. When the same subject
takes the same capture again within ten minutes, the count of the
existing `Event` goes up.

## 2. Reach the API

`media-api` is a `ClusterIP` `Service` at
`https://media-api.liken-system.svc`. It serves HTTPS with its own
certificate authority. That authority's certificate is in the
`ConfigMap` `media-api-ca` in `liken-system`, under the key
`ca.crt`. `display-api` and `audio-api` publish theirs the same way,
in `display-api-ca` and `audio-api-ca`.

The `media` routes work through a port-forward, because `media-api`
fetches the screen and the sound itself. A redirect does not work
through one, because its `Location` names another `Service`. So the
rest of this guide records from a pod in the cluster.

Put your client certificate and its key in a `Secret` that the pod
can mount:

    kubectl config view --raw --minify \
      -o jsonpath='{.users[0].user.client-certificate-data}' | base64 -d > client.crt
    kubectl config view --raw --minify \
      -o jsonpath='{.users[0].user.client-key-data}' | base64 -d > client.key
    kubectl -n liken-system create secret tls media-client \
      --cert client.crt --key client.key

Write the pod to `record-pod.yaml`. It mounts all three certificate
authorities, so one pod can record the composed stream and also
follow a redirect to a sibling API:

    apiVersion: v1
    kind: Pod
    metadata:
      name: record
      namespace: liken-system
    spec:
      restartPolicy: Never
      containers:
        - name: curl
          image: curlimages/curl:8.23.0
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
          projected:
            sources:
              - configMap:
                  name: media-api-ca
                  items: [{key: ca.crt, path: media.crt}]
              - configMap:
                  name: display-api-ca
                  items: [{key: ca.crt, path: display.crt}]
              - configMap:
                  name: audio-api-ca
                  items: [{key: ca.crt, path: audio.crt}]
        - name: client
          secret:
            secretName: media-client

The pod is in `liken-system` because a volume can only read a
`ConfigMap` or a `Secret` from the pod's own namespace. It exits after
an hour, so a pod you forget does not run forever.

    kubectl apply -f record-pod.yaml
    kubectl -n liken-system wait --for=condition=Ready pod/record --timeout 60s

`curl` takes one `--cacert` file, and a redirect goes from one API to
another, so join the three certificates into one file inside the pod:

    kubectl -n liken-system exec record -- sh -c 'cat /ca/*.crt > /tmp/ca.crt'

An application needs no `Secret`. It runs as the `ServiceAccount`
that you bound in step 1, mounts a token for the audience
`media-api`, and sends it as `Authorization: Bearer`:

    volumes:
      - name: token
        projected:
          sources:
            - serviceAccountToken:
                audience: media-api
                expirationSeconds: 3600
                path: token

Follow a redirect in two steps, not with `curl -L`. `curl` 8.22.0
sends no client certificate and no `Authorization` header after a
redirect to another host, so the other API returns 401. Your
certificate is valid there, because the three APIs trust the same
certificate authority. A token is valid for one audience only, so
mint a second token for the other API's audience.

## 3. Take the recording

List the players and pick one:

    kubectl -n media get players

Before you record, check what the `Player` has. The info route returns
the `Display` and its node, the `Sink`s, whether a `Play` is running,
and `streams`, the number of streams a recording would mux:

    kubectl -n liken-system exec record -- curl -sS --fail-with-body \
      --cacert /tmp/ca.crt --cert /client/tls.crt --key /client/tls.key \
      https://media-api.liken-system.svc/v1/media/namespaces/media/players/living-room

Each command below runs in the pod from step 2 and writes its file
there. `--fail-with-body` makes `curl` exit non-zero on an error and
still write the error document to the file, which step 4 reads.

Ten seconds of the screen with its sound, as MP4:

    kubectl -n liken-system exec record -- curl -sS --fail-with-body \
      --cacert /tmp/ca.crt --cert /client/tls.crt --key /client/tls.key \
      -o /tmp/living-room.mp4 \
      'https://media-api.liken-system.svc/v1/media/namespaces/media/players/living-room/media.mp4?t=0,10'

The same span as Matroska, scaled to 960 pixels wide:

    kubectl -n liken-system exec record -- curl -sS --fail-with-body \
      --cacert /tmp/ca.crt --cert /client/tls.crt --key /client/tls.key \
      -o /tmp/living-room.mkv \
      'https://media-api.liken-system.svc/v1/media/namespaces/media/players/living-room/media.mkv?t=0,10&width=960'

`media.mp4` is H.264 video in fragmented MP4 with Opus audio. It has
one video track if the `Player` has a screen, and one audio track for
each `Sink`, in the order of `spec.sinks`. `media.mkv` has the same
tracks in a Matroska container.

When `streams` is 0, both commands return 409. A `Player` with no
running `Play` has only its screen, so its file has a video track and
no audio track. The `media` routes never redirect, even for one
stream.

### The routes that redirect

The `screen` and `audio` routes return 307 to the API that owns the
hardware. To see the redirect:

    kubectl -n liken-system exec record -- curl -sS -i \
      --cacert /tmp/ca.crt --cert /client/tls.crt --key /client/tls.key \
      https://media-api.liken-system.svc/v1/media/namespaces/media/players/living-room/screen.png

`Location` is the `display-api` route for this `Player`'s `Display`,
and `Link` points to `media.mp4` as `related`. Read the `Location`
into a variable, then request it:

    kubectl -n liken-system exec record -- sh -c '
      url=$(curl -sS -o /dev/null -w "%{redirect_url}" --cacert /tmp/ca.crt \
        --cert /client/tls.crt --key /client/tls.key \
        https://media-api.liken-system.svc/v1/media/namespaces/media/players/living-room/screen.png)
      curl -sS --fail-with-body --cacert /tmp/ca.crt \
        --cert /client/tls.crt --key /client/tls.key -o /tmp/screen.png "$url"'

Five seconds of the sound alone, which redirects to `audio-api`:

    kubectl -n liken-system exec record -- sh -c '
      url=$(curl -sS -o /dev/null -w "%{redirect_url}" --cacert /tmp/ca.crt \
        --cert /client/tls.crt --key /client/tls.key \
        "https://media-api.liken-system.svc/v1/media/namespaces/media/players/living-room/audio.wav?t=0,5")
      curl -sS --fail-with-body --cacert /tmp/ca.crt \
        --cert /client/tls.crt --key /client/tls.key -o /tmp/living-room.wav "$url"'

An empty `url` means the route returned something other than a 307,
such as the 409 of an `audio` route when no `Play` is running. Run the
first `curl` alone with `-i` to read that response.

### The query parameters

| Parameter | What it does |
| --- | --- |
| `t=` | A W3C Media Fragments time range in seconds. Zero is the instant the capture container accepts the request. `t=0,10` records ten seconds. `t=5,7` discards five seconds and then records two. A begin over 60 seconds is a 400. Without an end, the recording runs until you close the connection. |
| `xywh=` | A rectangle of the frame as `x,y,width,height`, in the frame's own physical pixels, or in percent with `percent:`. |
| `width=`, `height=` | Scale the frame down. Both together is a 400. |
| `framerate=` | Frames per second of the video. |
| `quality=` | JPEG quality of a still. |
| `bitrate=` | Opus bitrate of the sound. |

`media-api` passes each parameter on to the API that serves the
screen or the sound. The `media` routes take `.mp4` and `.mkv`, the
`screen` routes take `.png`, `.jpg`, `.mp4`, and `.mjpeg`, and the
`audio` routes take `.wav`, `.flac`, and `.opus`. A path with no
extension chooses the format from `Accept`, and `media` returns
`video/mp4` when you send no `Accept`.

## 4. Check what you got

Copy a file out of the pod and read it with `ffprobe`:

    kubectl -n liken-system cp record:/tmp/living-room.mp4 living-room.mp4
    ffprobe living-room.mp4

A composed file from a `Player` with one `Sink` has two streams:
`h264` at the size of the screen, and `opus` at the sink's sample rate.
The duration is the `t=` span.

The first bytes arrive a few seconds after the request, because the
muxer writes nothing until it has a video keyframe and a packet from
each sink. The screen capture sends a keyframe every second.

Each recording also leaves a `Captured` `Event` on the `Player`:

    kubectl -n media get events --field-selector reason=Captured

`kubectl -n media describe player` shows who recorded the `Player`,
which part of it, in which format, and when.

### When a recording is refused

Each error is an `application/problem+json` document with `type`,
`title`, `status`, `detail`, and `instance`. An error that
`media-api` passes on from `display-api` or `audio-api` keeps that
API's `detail` and adds an `upstream` member with its URL. `curl`
wrote the document to the output file, so read that file:

    kubectl -n liken-system exec record -- cat /tmp/living-room.mp4

| Status | What it means | What to do |
| --- | --- | --- |
| 401 | No client certificate and no token, or the `TokenReview` refused the token | Check that the `Secret` has the certificate and key from the kubeconfig you use, or mint a token for the audience `media-api` |
| 403 | The `SubjectAccessReview` denied the request | Bind `media-capture-viewer` in the `Player`'s namespace, as step 1 shows. For a followed redirect, get a grant on the `Display` or the `Sink` too |
| 409 | A `screen` route on a `Player` with no `status.screen`, an `audio` route with no running `Play`, or a `media` route with neither | `detail` says what to do |
| 503 | `display-api` or `audio-api` is busy or refused the connection, or `media-api` is running as many recordings as it allows | Wait for the `Retry-After` time and try again |

## 5. Clean up

    kubectl -n liken-system delete pod record
    kubectl -n liken-system delete secret media-client
    rm client.crt client.key

The `RoleBinding` from step 1 stays until you delete it. If you do
not need it again, delete it too:

    kubectl -n media delete rolebinding media-viewer
