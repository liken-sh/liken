---
name: record
description: "Capture a Player's screen and sound as one MP4 or MKV with the kubectl liken media capture command, or over HTTP with kubectl and curl. Use to record what a media unit is playing right now."
---

This skill is the guide at https://media.liken.sh/docs/guides/record/, emitted for agents. Before the first command, run `kubectl config current-context` and confirm that it names the cluster the person means.

# Record what a player is playing

This guide shows you how to record a `Player`: the screen and the
sound of one unit, muxed into one MP4 or MKV file. It also shows the
plain screen and audio routes, which redirect to the operator that
owns the hardware. You need the operator
[installed](https://media.liken.sh/docs/guides/install/) on your
[`liken`](https://liken.sh/docs/) cluster and a
[`Player`](https://media.liken.sh/docs/reference/players/) that has a screen.

`media-api` serves the routes for a `Player`. It is one `Deployment`
per cluster, in `liken-system`, next to the operator. Its screen
routes redirect to `display-api` and its audio routes redirect to
`audio-api`, because those operators own the hardware. Its media
routes combine the two into one muxed stream, which no other API
does. The [API reference](https://media.liken.sh/docs/reference/api/) has the full
contract. This guide is the short path through it.

## The `kubectl liken media capture` command

The short path is the CLI. `kubectl liken media capture` streams a
`Player`'s composed video and sound to stdout as one MP4, so a file
or a pipe is a single command:

    kubectl liken media capture living-room -n house > living-room.mp4
    kubectl liken media capture living-room -n house | mpv -

`--format mkv` writes Matroska instead, and `-n` (or `--namespace`)
names the `Player`'s namespace.

The CLI authenticates with the client certificate in your
kubeconfig, the same subject `kubectl` uses, so the grant that step
1 describes is all it needs. It opens its own port-forward to
`media-api` and reads the composed stream through it, so it needs no
in-cluster routing and runs from a laptop.

The `Player` argument completes to the names the cluster reports.
`kubectl` runs the plugin's completion shim on its own, so
`kubectl liken media capture <TAB>` lists the `Player`s in the
namespace. For a direct call to `kubectl-liken-media`, load the
script with `source <(kubectl liken media completion bash)`.

The numbered steps below are the HTTP contract the CLI calls, for an
application in the cluster or a capture the CLI does not cover.

## 1. Who may record

You can identify yourself with a client certificate or with a
Bearer token. `media-api` checks for a certificate first, then for
a token, in the same order as the Kubernetes API server.

If your connection presents a client certificate signed by the
cluster's own certificate authority, you are that certificate's
subject. Your user name is the subject's common name, and your
groups are its organization values. The credentials in your
kubeconfig identify you here the same way they identify you to
`kubectl`. A certificate from any other authority ends the TLS
handshake.

If you present no certificate, send a Bearer token. `media-api`
verifies it with a `TokenReview` for the audience `media-api` and
checks `status.audiences`. A pod's default API server token does
not have that audience, so it does not work here.

After it identifies you, `media-api` sends a
`SubjectAccessReview` for the verb `get` on `players/screen`,
`players/audio`, or `players/media` in the group `media.liken.sh`,
with the `Player`'s namespace from the path.

The operator ships the `ClusterRole` `media-capture-viewer`, with
`get` on those three subresources and on `players`, so one binding
covers the info route and the captures together. A `Player` is
namespaced, so bind the role per namespace. Read your own subject
from your kubeconfig:

    kubectl config view --raw --minify \
      -o jsonpath='{.users[0].user.client-certificate-data}' \
      | base64 -d | openssl x509 -noout -subject

Then bind the role to the common name that command printed, in the
namespace of the `Player`:

    apiVersion: rbac.authorization.k8s.io/v1
    kind: RoleBinding
    metadata:
      name: media-viewer
      namespace: house
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
      namespace: house
    ---
    apiVersion: rbac.authorization.k8s.io/v1
    kind: RoleBinding
    metadata:
      name: media-viewer
      namespace: house
    roleRef:
      apiGroup: rbac.authorization.k8s.io
      kind: ClusterRole
      name: media-capture-viewer
    subjects:
      - kind: ServiceAccount
        name: media-viewer
        namespace: house

The composed route needs `players/media` and nothing else, because
`media-api` calls the sibling APIs under its own `ServiceAccount`. A
redirect includes no credentials. You must follow the 307 with your
own credentials. `display-api` then checks `displays/screen` for your
subject. To use the plain screen and audio routes, you also need a
grant on the `Display` and on each `Sink`. The
[display](https://display.liken.sh/docs/guides/screenshot/) and
[audio](https://audio.liken.sh/docs/guides/listen/) guides show
those grants.

`players/media` on a `Player` is in effect a grant on that
`Player`'s `Display` and `Sink`s. A role that grants
`resources: ["*"]` in `media.liken.sh` already includes every
capture, and so does `cluster-admin`.

Every request that returned bytes writes a `Captured` `Event` on the
`Player`, with the subject and the aspect in its message. A redirect
and a 503 return no bytes, so they write none.

## 2. Reach the API

`media-api` is a `ClusterIP` `Service` at
`https://media-api.liken-system.svc`. It serves HTTPS with its own
certificate authority. That authority's certificate is in the
`ConfigMap` `media-api-ca` in `liken-system`, under the key
`ca.crt`. `display-api` and `audio-api` publish theirs the same way,
in `display-api-ca` and `audio-api-ca`.

A port-forward is a single TCP connection through the API server.
The composed route works through one, because `media-api` fetches
the upstream streams itself. A redirect does not. Its `Location`
names another `Service`, so you would need a second port-forward to
that `Service`. Record from a pod on the cluster network instead,
which is what the rest of this guide does.

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
`ConfigMap` or a `Secret` from the pod's own namespace. The pod
sleeps for an hour and then exits, so a pod you forget does not run
forever.

    kubectl apply -f record-pod.yaml
    kubectl -n liken-system wait --for=condition=Ready pod/record --timeout 60s

`curl` takes one `--cacert` file, and a redirect crosses from one
API to another. Join the three certificates into one bundle inside
the pod, once:

    kubectl -n liken-system exec record -- sh -c 'cat /ca/*.crt > /tmp/ca.crt'

An application needs no `Secret`. It runs as the `ServiceAccount`
you bound in step 1, mounts a token for the API's audience, and
sends it as `Authorization: Bearer`:

    volumes:
      - name: token
        projected:
          sources:
            - serviceAccountToken:
                audience: media-api
                expirationSeconds: 3600
                path: token

Follow a redirect in two steps, never with `curl -L`. `curl` 8.22.0
sends no client certificate after a redirect to another host, and it
drops `Authorization` there too, so the sibling API returns 401. The
credential itself is valid at the sibling: your certificate
identifies the same subject at `display-api` as it does here,
because the three APIs read the same authority. A token is for one
audience only, so a token caller mints a second token for the
sibling's audience.

## 3. Take the recording

List the players and pick one:

    kubectl -n house get players

Before you record, ask what the `Player` has. The info route returns
the `Display` name and node, each `Sink` name, whether a `Play` is
running, and the number of streams:

    kubectl -n liken-system exec record -- curl -sS --fail-with-body \
      --cacert /tmp/ca.crt --cert /client/tls.crt --key /client/tls.key \
      https://media-api.liken-system.svc/v1/media/namespaces/house/players/living-room

Every command below runs in the pod from step 2 and writes its file
there. `--fail-with-body` makes `curl` exit non-zero on an error and
still write the problem document, which step 4 reads.

Ten seconds of the screen with its sound, as MP4:

    kubectl -n liken-system exec record -- curl -sS --fail-with-body \
      --cacert /tmp/ca.crt --cert /client/tls.crt --key /client/tls.key \
      -o /tmp/living-room.mp4 \
      'https://media-api.liken-system.svc/v1/media/namespaces/house/players/living-room/media.mp4?t=0,10'

The same span as Matroska, scaled to 960 pixels wide:

    kubectl -n liken-system exec record -- curl -sS --fail-with-body \
      --cacert /tmp/ca.crt --cert /client/tls.crt --key /client/tls.key \
      -o /tmp/living-room.mkv \
      'https://media-api.liken-system.svc/v1/media/namespaces/house/players/living-room/media.mkv?t=0,10&width=960'

`media.mp4` is H.264 in fragmented MP4 with Opus audio: one video
track if the `Player` has a screen, and one audio track per `Sink`
in `spec.sinks` order. `media.mkv` is the same streams through the
Matroska muxer.

Check `streams` on the info route before you run either command. A
`Player` with only one stream returns 307 to that stream instead of
composing, and a `Player` with no running `Play` has its screen
alone. A 307 is not an error, so `--fail-with-body` does not catch
it. The command then exits 0 and leaves an empty file. Follow the
redirect as the next section shows.

### The routes that redirect

The plain screen and audio routes return 307 to the sibling API that
owns the hardware. Read the redirect first:

    kubectl -n liken-system exec record -- curl -sS -i \
      --cacert /tmp/ca.crt --cert /client/tls.crt --key /client/tls.key \
      https://media-api.liken-system.svc/v1/media/namespaces/house/players/living-room/screen.png

`Location` is the `display-api` route for this `Player`'s `Display`,
and `Link` points to the composed `media.mp4` as `related`. Read
that `Location` into a variable, then request it:

    kubectl -n liken-system exec record -- sh -c '
      url=$(curl -sS -o /dev/null -w "%{redirect_url}" --cacert /tmp/ca.crt \
        --cert /client/tls.crt --key /client/tls.key \
        https://media-api.liken-system.svc/v1/media/namespaces/house/players/living-room/screen.png)
      curl -sS --fail-with-body --cacert /tmp/ca.crt \
        --cert /client/tls.crt --key /client/tls.key -o /tmp/screen.png "$url"'

Five seconds of the sound alone, which redirects to `audio-api`:

    kubectl -n liken-system exec record -- sh -c '
      url=$(curl -sS -o /dev/null -w "%{redirect_url}" --cacert /tmp/ca.crt \
        --cert /client/tls.crt --key /client/tls.key \
        "https://media-api.liken-system.svc/v1/media/namespaces/house/players/living-room/audio.wav?t=0,5")
      curl -sS --fail-with-body --cacert /tmp/ca.crt \
        --cert /client/tls.crt --key /client/tls.key -o /tmp/living-room.wav "$url"'

An empty `url` means the route returned something other than a 307.
A `Player` with no running `Play` returns 409 on its audio routes.
In that case, drop the second `curl` and read the first response
with `-i`.

### The query parameters

| Parameter | What it does |
| --- | --- |
| `t=` | A W3C Media Fragments time range in seconds. Zero is the instant the capture container accepts the request. `t=0,10` records ten seconds. `t=5,7` discards five seconds and then records two. A begin over 60 seconds is a 400. Without an end, the recording runs until you close the connection. |
| `xywh=` | A rectangle of the frame as `x,y,width,height`, in the frame's own physical pixels, or in percent with `percent:`. |
| `width=`, `height=` | Scale the frame down. Both together is a 400. |
| `framerate=` | Frames per second of the video. |
| `quality=` | JPEG quality of a still. |
| `bitrate=` | Opus bitrate of the sound. |

Each parameter goes to the sibling API that serves that half of the
recording. The extensions are `.mp4` and `.mkv` on `media`, the
display operator's four on `screen`, and the audio operator's three
on `audio`. A path with no extension negotiates on `Accept`, and
`media` returns `video/mp4` if you send none.

## 4. Check what you got

Copy a file out of the pod and read it with `ffprobe`:

    kubectl -n liken-system cp record:/tmp/living-room.mp4 living-room.mp4
    ffprobe living-room.mp4

A composed file has two streams: `h264` at the size of the screen,
and `opus` at the sink's sample rate. The duration is the `t=` span.

The first byte arrives after a lead-in of one second plus the slower
sibling's first keyframe. The muxer writes nothing until it has a
keyframe from the video and a packet from each sink, and a screen
capture at the default 15 frames per second has a keyframe every
second. In our tests, `media.mp4?t=0,10` took 4.09 to 4.34 s to
first byte over ten runs, and `media.mkv` took 4.18 s.

The cluster's own record of the recording is an `Event` on the
`Player`, in the `Player`'s namespace:

    kubectl -n house get events --field-selector reason=Captured

`kubectl -n house describe player` tells you who recorded which
unit, in which form, and when.

### When a recording is refused

Every error is an `application/problem+json` document with `type`,
`title`, `status`, `detail`, and `instance`. A problem relayed from a
sibling API has that sibling's own `detail` and an `upstream` member
with its URL. `curl` wrote the document to the output file, so read
that file:

    kubectl -n liken-system exec record -- cat /tmp/living-room.mp4

| Status | What it means | What to do |
| --- | --- | --- |
| 401 | No client certificate and no token, or the `TokenReview` refused the token | Check that the `Secret` has the certificate and key from the kubeconfig you use, or mint a token for the audience `media-api` |
| 403 | The `SubjectAccessReview` said no | Bind `media-capture-viewer` in the `Player`'s namespace, as step 1 shows. For a followed redirect, bind the sibling's role too |
| 409 | The `Player` has no `status.screen`, or no `Play` is running for an audio route | `detail` says what to do |
| 503 | An upstream is busy or refused the connection, or this API is at its composition limit | The response has `Retry-After: 5`, relayed from the upstream. Wait and try again |

## 5. Clean up

    kubectl -n liken-system delete pod record
    kubectl -n liken-system delete secret media-client
    rm client.crt client.key

The `RoleBinding` from step 1 is a standing grant. If the recording
was a one-off, delete it too:

    kubectl -n house delete rolebinding media-viewer
