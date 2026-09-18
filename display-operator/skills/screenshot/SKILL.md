---
name: screenshot
description: "Capture a Display with the kubectl liken display capture command, or take a screenshot, a clip, or a live stream over HTTP with kubectl and curl. Use when someone asks what a screen shows, or to record it."
---

This skill is the guide at https://display.liken.sh/docs/guides/screenshot/, emitted for agents. Before the first command, run `kubectl config current-context` and confirm that it names the cluster the person means.

# Take a picture of a screen

This guide shows you how to capture what a monitor shows: one frame
as PNG or JPEG, a clip as MP4, a live MJPEG stream, or a rectangle
of any of those. You need the operator
[installed](https://display.liken.sh/docs/guides/install/) on your
[`liken`](https://liken.sh/docs/) cluster and a connected
[`Display`](https://display.liken.sh/docs/reference/displays/).

`display-api` serves the captures. It is a `Deployment` in
`liken-system`. The capture container in the `display-operator` pod
on each node takes the frames from the compositor. Nothing is
stored. Each capture is taken when you ask for it and streamed to
you while it is made. The [API reference](https://display.liken.sh/docs/reference/api/) has
the full contract. This guide is the short path through it.

## The `kubectl liken display capture` command

The short path is the CLI. `kubectl liken display capture` streams an
output's screen to stdout as MP4, so a file or a pipe is a single
command:

    kubectl liken display capture lg-hdr-wqhd-display | mpv -
    kubectl liken display capture lg-hdr-wqhd-display --format png > screen.png

`--format png` writes one frame in place of a clip. A `Display` is
cluster-scoped, so the command takes no namespace.

The CLI authenticates with the client certificate in your
kubeconfig, the same subject `kubectl` uses, so the grant that step
1 describes is all it needs. It opens its own port-forward to
`display-api` and reads the stream through it, so it needs no
in-cluster routing and runs from a laptop.

The output argument completes to the names the cluster reports.
`kubectl` runs the plugin's completion shim on its own, so
`kubectl liken display capture <TAB>` lists the `Display`s. For a
direct call to `kubectl-liken-display`, load the script with
`source <(kubectl liken display completion bash)`.

The numbered steps below are the HTTP contract the CLI calls, for an
application in the cluster, a still through a port-forward, or a JPEG
or MJPEG capture the CLI does not serve.

## 1. Who may capture

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
verifies it with a `TokenReview` for the audience `display-api`, and
checks that the answer names that audience. A pod's ordinary API
server token does not have that audience, so it does not work here.

After it knows who you are, the API sends a `SubjectAccessReview`
for the verb `get` on `displays/screen` in the group
`display.liken.sh`. Every route authorizes before it reads anything,
so a 403 never tells you whether a name exists.

The operator ships a `ClusterRole` named `display-capture-viewer`
and binds it to nobody. It grants `get` on `displays/screen` for the
capture routes and `get` on `displays` for the info route. Read your
own subject from your kubeconfig:

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

To bind a group instead, use `kind: Group` with one of the
certificate's organization values as the name.

An application gets the grant through its `ServiceAccount`:

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

A role that grants `resources: ["*"]` on `display.liken.sh` already
includes `displays/screen`, and so does `cluster-admin`. Every
subject with one of those may look at every screen. `pods/exec` in
`liken-system` is also a capture grant: the `display-operator` pod
shares its process namespace between its containers, so a shell in
that pod can reach the capture socket.

Every request that returned bytes writes a `Captured` `Event` on the
`Display`, with the subject and the aspect in its message.

## 2. Reach the API

`display-api` is a `ClusterIP` `Service` at
`https://display-api.liken-system.svc`. It serves HTTPS with its own
certificate authority. That authority's certificate is in the
`ConfigMap` `display-api-ca` in `liken-system`, under the key
`ca.crt`.

A port-forward is fine for a still and bad for a stream. It is a
single TCP connection through the API server. In our tests it moved
about 2 Mbit/s: a 3 second MJPEG stream that the node captured in
3.06 s took 25 s to read through the forward, and 3.09 s from a pod
on the cluster network. A 1080p MJPEG stream needs about 15 Mbit/s,
so through the forward you see it at about one eighth of real time.
Stills are not affected. `screen.png` took 0.9 s to first byte
through the forward and 0.76 s from the cluster network. Read a
stream from a pod on the cluster network.

Put your client certificate and its key in a `Secret` that the pod
can mount:

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

The pod is in `liken-system` because a volume can only read a
`ConfigMap` or a `Secret` from the pod's own namespace. The pod
sleeps for an hour and then exits, so a pod you forget does not run
forever.

    kubectl apply -f capture-pod.yaml
    kubectl -n liken-system wait --for=condition=Ready pod/capture --timeout 60s

An application needs no `Secret`. It runs as the `ServiceAccount`
you bound in step 1, mounts a token for the API's audience, and
sends it as `Authorization: Bearer`:

    volumes:
      - name: token
        projected:
          sources:
            - serviceAccountToken:
                audience: display-api
                expirationSeconds: 3600
                path: token

For one still and nothing more, you need no pod. A port-forward is a
TCP tunnel, so the TLS handshake runs end to end and the certificate
reaches the API unchanged:

    kubectl -n liken-system port-forward svc/display-api 8443:443 &
    kubectl -n liken-system get configmap display-api-ca \
      -o jsonpath='{.data.ca\.crt}' > display-api-ca.crt
    curl --cert client.crt --key client.key --cacert display-api-ca.crt \
      -o screen.png \
      https://localhost:8443/v1/display/displays/lg-hdr-wqhd-display/screen.png

## 3. Take the capture

List the screens and pick one:

    kubectl get displays

Every command below runs in the pod from step 2 and writes its file
there. `--fail-with-body` makes `curl` exit non-zero on an error and
still write the problem document, which step 4 reads.

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

### The query parameters

| Parameter | What it does |
| --- | --- |
| `t=` | A W3C Media Fragments time range in seconds. Zero is the instant the capture container accepts the request. `t=,10` records ten seconds from now. `t=5,7` discards five seconds and then records two. `t=5` on a still waits five seconds and takes one frame. A begin over 60 seconds is a 400, and a `t=` end on a still is a 400. Without an end, a clip or a stream runs until you close the connection. |
| `xywh=` | A rectangle of the frame as `x,y,width,height`, in the frame's own physical pixels. `xywh=percent:0,0,50,50` is the same rectangle in percent. A region that runs off an edge is clipped to the screen. An origin at or past an edge is a 400. |
| `width=`, `height=` | Scale the region down after the crop, with the aspect ratio kept. Both together, or a value larger than the source, is a 400. |
| `framerate=` | Frames per second of a clip or an MJPEG stream. 15 by default, at most the output's refresh rate. A 400 on a still. |
| `quality=` | JPEG quality from 1 to 100. 85 by default. A 400 on PNG and MP4. |

The extensions are `.png`, `.jpg`, `.mp4`, and `.mjpeg`. A path
with no extension negotiates on `Accept` and returns `image/png` if
you send none.

## 4. Check what you got

Copy a file out of the pod and read it with `ffprobe`:

    kubectl -n liken-system cp capture:/tmp/clip.mp4 clip.mp4
    ffprobe clip.mp4

A clip is H.264 in `mov,mp4,m4a,3gp,3g2,mj2`, at the size of the
screen. A still is one `png` or `mjpeg` frame. A capture with
`xywh=` has the size of the region.

The cluster's own record of the capture is an `Event`. A `Display`
is cluster-scoped, so its `Event`s are in the `default` namespace,
the same convention a `Node`'s `Event`s follow:

    kubectl get events --field-selector reason=Captured

Each message names the subject, the aspect, and the media type, so
`kubectl describe display` tells you who looked at a screen and
when.

### When a capture is refused

Every error is an `application/problem+json` document with `type`,
`title`, `status`, `detail`, and `instance`. `curl` wrote it to the
output file, so read that file:

    kubectl -n liken-system exec capture -- cat /tmp/screen.png

| Status | What it means | What to do |
| --- | --- | --- |
| 401 | No client certificate and no token, or the `TokenReview` refused the token | Check that the `Secret` has the certificate and key from the kubeconfig you use, or mint a token for the audience `display-api` |
| 403 | The `SubjectAccessReview` said no | Bind `display-capture-viewer` to your subject, as step 1 shows. The `WWW-Authenticate` header names the scope you need |
| 503 | The screen has no node (`no-node`), its compositor is not serving (`compositor-down`), the output is already being captured (`capture-busy`), or the capture container is absent or not ready (`upstream-failed`) | The response has `Retry-After: 5`. Wait five seconds and try again. `detail` quotes the source of the error |

You can ask what a `Display` is even when its screen is down. The
info route returns the name, the node, and the size and refresh rate
the `Display` reports, with `compositor: down` or
`sidecar: unreachable` and the condition's message in `detail`:

    kubectl -n liken-system exec capture -- curl -sS --fail-with-body \
      --cacert /ca/ca.crt --cert /client/tls.crt --key /client/tls.key \
      https://display-api.liken-system.svc/v1/display/displays/lg-hdr-wqhd-display

## 5. Clean up

    kubectl -n liken-system delete pod capture
    kubectl -n liken-system delete secret display-client
    rm client.crt client.key

The `ClusterRoleBinding` from step 1 is a standing grant. If the
capture was a one-off, delete it too:

    kubectl delete clusterrolebinding display-viewer

## What a capture costs the screen

A capture turns off the compositor's hardware planes for its whole
length. A film that a plane would normally show goes through the GL
renderer instead while a clip runs. On a node whose driver has no
VA-API post-processing, the color conversion runs on the CPU too,
which costs about four times the cores at 1080p. The info route's
`conversion` field names the pipeline the node uses.
