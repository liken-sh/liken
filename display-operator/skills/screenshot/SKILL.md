---
name: screenshot
description: "Take a screenshot, a clip, or a live stream of a Display over HTTP with kubectl and curl. Use when someone asks what a screen shows, or to record it."
---

This skill is the guide at https://display.liken.sh/docs/guides/screenshot/, emitted for agents. Before the first command, run `kubectl config current-context` and confirm that it names the cluster the person means.

# Take a picture of a screen

This guide captures what a monitor shows: one frame as PNG or JPEG,
a clip as MP4, a live MJPEG stream, and a rectangle of any of them.
You need the operator [installed](https://display.liken.sh/docs/guides/install/) on your
[`liken`](https://liken.sh/docs/) cluster and a connected
[`Display`](https://display.liken.sh/docs/reference/displays/).

`display-api` answers the captures. It is a `Deployment` in
`liken-system`, and the capture container in the `display-operator`
pod on each node takes the frames from the compositor. Nothing is
stored. Every capture is taken when it is asked for and streamed to
the caller as it is made. The [API reference](https://display.liken.sh/docs/reference/api/)
states the whole contract; this guide is the short path through it.

## 1. Who may capture

A request names its caller two ways, and the API reads them in the
order the API server reads them.

A connection that carries a client certificate the cluster's own
authority signed names that certificate's subject. The user is the
subject's common name, and the groups are its organization values.
The credentials in your kubeconfig therefore name the same subject
here that they name to `kubectl`. A certificate from any other
authority ends the handshake.

A caller that offers no certificate carries a Bearer token. The API
sends the token in a `TokenReview` with the audience `display-api`,
and checks that the answer names that audience. A pod's ordinary
API-server token does not open this API.

The API then sends a `SubjectAccessReview` for the verb `get` on
`displays/screen` in the group `display.liken.sh`. Every route
authorizes before it reads, so a 403 never says whether a name
exists.

The base ships the `ClusterRole` `display-capture-viewer` and binds
it to nobody. It grants `get` on `displays/screen` for the capture
routes and `get` on `displays` for the info route beside them. Read
your own subject out of your kubeconfig:

    kubectl config view --raw --minify \
      -o jsonpath='{.users[0].user.client-certificate-data}' \
      | base64 -d | openssl x509 -noout -subject

Then bind the role to the common name that command printed:

    apiVersion: rbac.authorization.k8s.io/v1
    kind: ClusterRoleBinding
    metadata:
      name: display-viewer
    roleRef:
      apiGroup: rbac.authorization.k8s.io
      kind: ClusterRole
      name: display-capture-viewer
    subjects:
      - kind: User
        name: <the common name>

An organization value in the same certificate binds as `kind: Group`
with that value as the name.

An application holds the grant through its `ServiceAccount`:

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

A role with a wildcard `resources: ["*"]` on `display.liken.sh`, and
`cluster-admin`, already grant `displays/screen`, so every subject
that holds one may look at every screen. `pods/exec` in
`liken-system` reaches the capture socket through the
`display-operator` pod's shared process namespace, so that grant is
a capture grant too.

Every request that produced bytes writes a `Captured` `Event` on the
`Display`, with the subject and the aspect in its message.

## 2. Reach the API

`display-api` is a `ClusterIP` `Service` at
`https://display-api.liken-system.svc`. It serves HTTPS under its
own authority, and that authority's certificate is in the
`ConfigMap` `display-api-ca` in `liken-system`, under the key
`ca.crt`.

A port-forward carries a still well and a stream badly. It is a
single TCP connection through the API server, and in the lab it
moved about 2 Mbit/s: a 3 second MJPEG stream that the node captured
in 3.06 s took 25 s to read through the forward and 3.09 s from a
pod on the cluster network. A 1080p MJPEG stream needs about
15 Mbit/s, so a viewer on the forward sees it at about an eighth of
real time. Stills are not affected: `screen.png` cost 0.9 s to first
byte through the forward against 0.76 s from the cluster network.
Read a stream from a pod on the cluster network.

Put your client certificate and its key in a `Secret` that pod can
mount:

    kubectl config view --raw --minify \
      -o jsonpath='{.users[0].user.client-certificate-data}' | base64 -d > client.crt
    kubectl config view --raw --minify \
      -o jsonpath='{.users[0].user.client-key-data}' | base64 -d > client.key
    kubectl -n liken-system create secret tls display-client \
      --cert client.crt --key client.key

Write the pod to `capture-pod.yaml`:

    apiVersion: v1
    kind: Pod
    metadata:
      name: capture
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
            name: display-api-ca
        - name: client
          secret:
            secretName: display-client

The pod is in `liken-system` because a volume reads a `ConfigMap`
and a `Secret` from the pod's own namespace. It sleeps for an hour
and then ends, so a forgotten pod does not run for a week.

    kubectl apply -f capture-pod.yaml
    kubectl -n liken-system wait --for=condition=Ready pod/capture --timeout 60s

An application needs no `Secret`. It runs as the `ServiceAccount`
you bound in step 1, mounts a token for the API's own audience, and
sends it as `Authorization: Bearer`:

    volumes:
      - name: token
        projected:
          sources:
            - serviceAccountToken:
                audience: display-api
                expirationSeconds: 3600
                path: token

For one still and nothing more, a port-forward needs no pod. The
forward is a TCP tunnel, so the TLS handshake runs end to end and
carries the certificate to the API untouched:

    kubectl -n liken-system port-forward svc/display-api 8443:443 &
    kubectl -n liken-system get configmap display-api-ca \
      -o jsonpath='{.data.ca\.crt}' > display-api-ca.crt
    curl --cert client.crt --key client.key --cacert display-api-ca.crt \
      -o screen.png \
      https://localhost:8443/v1/display/displays/lg-hdr-wqhd-display/screen.png

## 3. Take the capture

List the screens and pick one:

    kubectl get displays

Every capture below runs in the pod from step 2 and writes its file
there. `--fail-with-body` makes `curl` exit non-zero on a refusal
and still write the problem document, which step 4 reads.

One frame as PNG:

    kubectl -n liken-system exec capture -- curl -sS --fail-with-body \
      --cacert /ca/ca.crt --cert /client/tls.crt --key /client/tls.key \
      -o /tmp/screen.png \
      https://display-api.liken-system.svc/v1/display/displays/lg-hdr-wqhd-display/screen.png

One frame as JPEG, at quality 95:

    kubectl -n liken-system exec capture -- curl -sS --fail-with-body \
      --cacert /ca/ca.crt --cert /client/tls.crt --key /client/tls.key \
      -o /tmp/screen.jpg \
      'https://display-api.liken-system.svc/v1/display/displays/lg-hdr-wqhd-display/screen.jpg?quality=95'

A ten second clip, as H.264 in fragmented MP4:

    kubectl -n liken-system exec capture -- curl -sS --fail-with-body \
      --cacert /ca/ca.crt --cert /client/tls.crt --key /client/tls.key \
      -o /tmp/clip.mp4 \
      'https://display-api.liken-system.svc/v1/display/displays/lg-hdr-wqhd-display/screen.mp4?t=,10'

Three seconds of MJPEG, one JPEG part per frame:

    kubectl -n liken-system exec capture -- curl -sS --fail-with-body \
      --cacert /ca/ca.crt --cert /client/tls.crt --key /client/tls.key \
      -o /tmp/stream.mjpeg \
      'https://display-api.liken-system.svc/v1/display/displays/lg-hdr-wqhd-display/screen.mjpeg?t=,3'

The top left quarter of a 1920x1080 screen, as PNG:

    kubectl -n liken-system exec capture -- curl -sS --fail-with-body \
      --cacert /ca/ca.crt --cert /client/tls.crt --key /client/tls.key \
      -o /tmp/corner.png \
      'https://display-api.liken-system.svc/v1/display/displays/lg-hdr-wqhd-display/screen.png?xywh=0,0,960,540'

### The query knobs

`t=` is a W3C Media Fragments time range in seconds, and its zero is
the instant the capture container accepts the request. `t=,10`
records ten seconds from now, `t=5,7` discards five seconds and then
records two, and `t=5` on a still waits five seconds and takes one
frame. A begin over 60 seconds is a 400, and a `t=` end on a still
is a 400. Without a `t=` end, a clip or a stream runs until the
client hangs up.

`xywh=` is a rectangle of the frame, `x,y,width,height` in the
frame's own physical pixels. `xywh=percent:0,0,50,50` states the
same rectangle in percent. A region that runs off an edge is clipped
to the screen, and an origin at or past an edge is a 400.

`width=` or `height=` scales the region down after the crop, with
the aspect kept. The two together, or a value above the source, is a
400.

`framerate=` is the frames per second of a clip or an MJPEG stream,
15 by default, at most the output's refresh, and a 400 on a still.

`quality=` is the JPEG quality, 1 to 100, 85 by default, and a 400
on PNG and MP4.

The extensions are `.png`, `.jpg`, `.mp4`, and `.mjpeg`. The path
with no extension negotiates on `Accept` and answers `image/png`
when the caller states none.

## 4. Check what you got

Copy a file out of the pod and read it with `ffprobe`:

    kubectl -n liken-system cp capture:/tmp/clip.mp4 clip.mp4
    ffprobe clip.mp4

A clip reads as H.264 in `mov,mp4,m4a,3gp,3g2,mj2`, at the size of
the screen. A still reads as one `png` or `mjpeg` frame. An
`xywh=` capture reads at the size of the region.

The cluster's own record of the capture is an `Event`. A `Display`
is cluster-scoped, so its `Event`s land in `default`, the convention
a `Node`'s own `Event`s follow:

    kubectl get events --field-selector reason=Captured

Each message names the subject, the aspect, and the media type, so
`kubectl describe display` answers who looked at a screen and when.

### When a capture is refused

Every error is an `application/problem+json` document with `type`,
`title`, `status`, `detail`, and `instance`. `curl` wrote it where
the picture would have gone, so read that file:

    kubectl -n liken-system exec capture -- cat /tmp/screen.png

| Status | What it means | What to do |
| --- | --- | --- |
| 401 | The API read no client certificate and no token, or the `TokenReview` refused the token | Check that the `Secret` holds the certificate and key of the kubeconfig you use, or mint a token for the audience `display-api` |
| 403 | The `SubjectAccessReview` said no | Bind `display-capture-viewer` to your subject, as step 1 shows. The `WWW-Authenticate` field names the scope you need |
| 503 | The screen has no node (`no-node`), its compositor is not serving (`compositor-down`), the output is already being captured (`capture-busy`), or the capture container is absent or not ready (`upstream-failed`) | The answer carries `Retry-After: 5`. Wait five seconds and ask again. `detail` carries the source's own words |

A `Display` answers what it is even when its screen is down. The
info route carries the name, the node, and the size and refresh the
`Display` reports, with `compositor: down` or
`sidecar: unreachable` and the condition's words in `detail`:

    kubectl -n liken-system exec capture -- curl -sS --fail-with-body \
      --cacert /ca/ca.crt --cert /client/tls.crt --key /client/tls.key \
      https://display-api.liken-system.svc/v1/display/displays/lg-hdr-wqhd-display

## 5. Clean up

    kubectl -n liken-system delete pod capture
    kubectl -n liken-system delete secret display-client
    rm client.crt client.key

The `ClusterRoleBinding` from step 1 is the standing grant. Delete
it too when the capture was a one-off:

    kubectl delete clusterrolebinding display-viewer

## What a capture costs the screen

A capture holds the compositor's hardware planes off for its whole
length, so a film that a plane would show is composited through the
GL renderer while a clip runs. On a node whose driver has no VA-API
post-processing the colour conversion runs on the CPU as well, which
costs about four times the cores at 1080p. The info route's
`conversion` member names the graph the node runs.
