---
title: Record what a player is playing
weight: 40
description: "Record a Player's screen with its sound as one MP4 or MKV over HTTP with kubectl and curl. Use to capture what a media unit is playing right now."
---

# Record what a player is playing

This guide records a `Player`: the screen and the sound of one unit
of equipment, muxed into one MP4 or MKV file. It also shows the
plain screen and audio routes, which redirect to the operator that
owns the hardware. You need the operator
[installed](/docs/guides/install/) on your
[`liken`](https://liken.sh/docs/) cluster and a
[`Player`](/docs/reference/players/) that resolves a screen.

`media-api` answers for a `Player`. It is one `Deployment` per
cluster, in `liken-system`, beside the operator. Its screen routes
redirect to the display operator's `display-api` and its audio
routes redirect to the audio operator's `audio-api`, because each of
those owns its hardware. Its media routes compose the two into one
muxed stream, which no other API answers. The
[API reference](/docs/reference/api/) states the whole contract;
this guide is the short path through it.

## 1. Who may record

A request names its caller two ways, and `media-api` reads them in
the order the API server reads them.

A connection that carries a client certificate the cluster's own
authority signed names that certificate's subject. The user is the
subject's common name, and the groups are its organization values.
The credentials in your kubeconfig therefore name the same subject
here that they name to `kubectl`. A certificate from any other
authority ends the handshake.

A caller that offers no certificate carries a Bearer token.
`media-api` validates it with a `TokenReview` that names the
audience `media-api` and checks `status.audiences`, so a pod's
default API-server token does not open it.

Either credential then authorizes with a `SubjectAccessReview` for
the verb `get` on `players/screen`, `players/audio`, or
`players/media` in `media.liken.sh`, with the `Player`'s namespace
from the path.

The operator ships the `ClusterRole` `media-capture-viewer`, with
`get` on those three subresources and on `players`. It carries
`players` as well as the three aspects, so one binding covers the
information route and the captures together. A `Player` is
namespaced, so bind the role per namespace. Read your own subject
out of your kubeconfig:

    kubectl config view --raw --minify \
      -o jsonpath='{.users[0].user.client-certificate-data}' \
      | base64 -d | openssl x509 -noout -subject

Then bind the role to the common name that command printed, in the
namespace that holds the `Player`:

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

An organization value in the same certificate binds as `kind: Group`
with that value as the name.

An application holds the grant through its `ServiceAccount`:

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
`media-api` calls the siblings under its own `ServiceAccount`. A
redirect carries no credentials: the client follows the 307 with its
own, and `display-api` checks `displays/screen` for that subject. So
a caller of the plain screen and audio routes also needs the grant
on the `Display` and on each `Sink`, from the
[display](https://display.liken.sh/docs/guides/screenshot/) and
[audio](https://audio.liken.sh/docs/guides/listen/) guides.

`players/media` on a `Player` is therefore a grant on that
`Player`'s `Display` and `Sink`s. A role with `resources: ["*"]` in
`media.liken.sh`, and `cluster-admin`, already grant every capture.

Every request that produces bytes writes a `Captured` `Event` on the
`Player`, with the subject and the aspect in its message. A redirect
and a 503 produce no bytes, so they write none.

## 2. Reach the API

`media-api` is a `ClusterIP` `Service` at
`https://media-api.liken-system.svc`. It serves HTTPS under its own
authority, and that authority's certificate is in the `ConfigMap`
`media-api-ca` in `liken-system`, under the key `ca.crt`.
`display-api` and `audio-api` publish theirs the same way, in
`display-api-ca` and `audio-api-ca`.

A port-forward is a single TCP connection through the API server.
The composed route works through one forward, because `media-api`
fetches the upstreams itself. A redirect does not: its `Location`
names another `Service`, so the caller opens a second forward to
that `Service` and repeats the request. Record from a pod on the
cluster network instead, which is what the rest of this guide uses.

Put your client certificate and its key in a `Secret` that pod can
mount:

    kubectl config view --raw --minify \
      -o jsonpath='{.users[0].user.client-certificate-data}' | base64 -d > client.crt
    kubectl config view --raw --minify \
      -o jsonpath='{.users[0].user.client-key-data}' | base64 -d > client.key
    kubectl -n liken-system create secret tls media-client \
      --cert client.crt --key client.key

Write the pod to `record-pod.yaml`. It mounts all three authorities,
under one name each, so that one pod both records the composed
stream and follows a redirect to a sibling:

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

The pod is in `liken-system` because a volume reads a `ConfigMap`
and a `Secret` from the pod's own namespace. It sleeps for an hour
and then ends, so a forgotten pod does not run for a week.

    kubectl apply -f record-pod.yaml
    kubectl -n liken-system wait --for=condition=Ready pod/record --timeout 60s

`curl` verifies one server per `--cacert` file, and a redirect
crosses from one API to another. Join the three into one bundle
inside the pod, once:

    kubectl -n liken-system exec record -- sh -c 'cat /ca/*.crt > /tmp/ca.crt'

An application needs no `Secret`. It runs as the `ServiceAccount`
you bound in step 1, mounts a token for the API's own audience, and
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
drops `Authorization` there as well, so the sibling answers 401. The
credential itself is good at the sibling: your certificate names the
same subject at `display-api` that it names here, because the three
APIs read the same authority. A token names one audience only, so a
token caller mints a second token for the sibling's audience.

## 3. Take the recording

List the players and pick one:

    kubectl -n house get players

Ask what it resolves before you record it. The information route
answers the `Display` name and node, each `Sink` name, whether a
`Play` runs, and the stream count:

    kubectl -n liken-system exec record -- curl -sS --fail-with-body \
      --cacert /tmp/ca.crt --cert /client/tls.crt --key /client/tls.key \
      https://media-api.liken-system.svc/v1/media/namespaces/house/players/living-room

Every capture below runs in the pod from step 2 and writes its file
there. `--fail-with-body` makes `curl` exit non-zero on a refusal
and still write the problem document, which step 4 reads.

Ten seconds of the screen with its sound, as MP4:

    kubectl -n liken-system exec record -- curl -sS --fail-with-body \
      --cacert /tmp/ca.crt --cert /client/tls.crt --key /client/tls.key \
      -o /tmp/living-room.mp4 \
      'https://media-api.liken-system.svc/v1/media/namespaces/house/players/living-room/media.mp4?t=0,10'

The same span as Matroska, scaled to 960 wide:

    kubectl -n liken-system exec record -- curl -sS --fail-with-body \
      --cacert /tmp/ca.crt --cert /client/tls.crt --key /client/tls.key \
      -o /tmp/living-room.mkv \
      'https://media-api.liken-system.svc/v1/media/namespaces/house/players/living-room/media.mkv?t=0,10&width=960'

`media.mp4` is H.264 in fragmented MP4 with Opus audio, one video
track where the `Player` has a screen and one audio track per `Sink`
in `spec.sinks` order. `media.mkv` is the same streams through the
Matroska muxer.

Read `streams` on the information route before you run either
command. A `Player` that resolves only one stream answers 307 to
that stream instead of composing, and a `Player` that runs no `Play`
resolves its screen alone. A 307 is not an error, so
`--fail-with-body` does not catch it: the command above then exits 0
and leaves an empty file. Follow that redirect the way the next
section does.

### The routes that redirect

The plain screen and audio routes answer 307 to the sibling that
owns the hardware. Read the redirect first:

    kubectl -n liken-system exec record -- curl -sS -i \
      --cacert /tmp/ca.crt --cert /client/tls.crt --key /client/tls.key \
      https://media-api.liken-system.svc/v1/media/namespaces/house/players/living-room/screen.png

`Location` names the `display-api` route for this `Player`'s
`Display`, and `Link` names the composed `media.mp4` as `related`.
Read that `Location` into a variable, then ask for it:

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

An empty `url` means the route answered something other than a 307.
A `Player` with no running `Play` answers 409 on its audio routes,
so drop the second `curl` and read the first answer with `-i`.

### The query knobs

`t=` is a W3C Media Fragments time range in seconds, and its zero is
the instant the sidecar accepts the request. `t=0,10` records ten
seconds, and `t=5,7` discards five seconds and then records two. A
begin over 60 seconds is a 400, and an absent end records until the
client hangs up.

`xywh=` is a rectangle of the frame, `x,y,width,height` in the
frame's own physical pixels, with `percent:` on request. `width=` or
`height=` scales the frame down, and the two together are a 400.
`framerate=` is the frames per second of the video, and `quality=`
is the JPEG quality of a still. `bitrate=` is the Opus bitrate of
the sound. Each knob goes to the sibling that serves that half.

The extensions are `.mp4` and `.mkv` on `media`, the display
operator's four on `screen`, and the audio operator's three on
`audio`. A path with no extension negotiates on `Accept`, and
`media` answers `video/mp4` when the caller states none.

## 4. Check what you got

Copy a file out of the pod and read it with `ffprobe`:

    kubectl -n liken-system cp record:/tmp/living-room.mp4 living-room.mp4
    ffprobe living-room.mp4

A composed file reads as two streams: `h264` at the size of the
screen, and `opus` at the sink's rate. The duration is the `t=`
span.

The first byte arrives after a lead-in of one second plus the slower
sibling's first keyframe. The muxer writes nothing until it holds a
keyframe from the video and a packet from each sink, and a screen
capture at the default 15 frames per second has a keyframe every
second. The lab measured 4.09 to 4.34 s to the first byte over ten
runs of `media.mp4?t=0,10`, and 4.18 s for `media.mkv`.

The cluster's own record of the capture is an `Event` on the
`Player`, in the `Player`'s own namespace:

    kubectl -n house get events --field-selector reason=Captured

`kubectl -n house describe player` answers who looked, at which
unit, in which form, and when.

### When a recording is refused

Every error is an `application/problem+json` document with `type`,
`title`, `status`, `detail`, and `instance`. A problem relayed from
a sibling carries that sibling's own `detail` and an `upstream`
member with its URL. `curl` wrote the document where the recording
would have gone, so read that file:

    kubectl -n liken-system exec record -- cat /tmp/living-room.mp4

| Status | What it means | What to do |
| --- | --- | --- |
| 401 | The API read no client certificate and no token, or the `TokenReview` refused the token | Check that the `Secret` holds the certificate and key of the kubeconfig you use, or mint a token for the audience `media-api` |
| 403 | The `SubjectAccessReview` denied the subject | Bind `media-capture-viewer` in the `Player`'s namespace, as step 1 shows. On a followed redirect, bind the sibling's role as well |
| 409 | The `Player` carries no `status.screen`, or no `Play` runs for an audio aspect | `detail` names the action that clears it |
| 503 | An upstream is busy or refused the connection, or this API is at its composition limit | The answer carries `Retry-After: 5`, an upstream's value relayed. Wait and ask again |

## 5. Clean up

    kubectl -n liken-system delete pod record
    kubectl -n liken-system delete secret media-client
    rm client.crt client.key

The `RoleBinding` from step 1 is the standing grant. Delete it too
when the recording was a one-off:

    kubectl -n house delete rolebinding media-viewer
