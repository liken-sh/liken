---
name: screenshot
description: "Capture a Display with the kubectl liken display capture command, or take a screenshot, a clip, or a live stream over HTTP with kubectl and curl. Use when someone asks what a screen shows, or to record it."
---

This skill is the guide at https://liken.sh/display/docs/guides/screenshot/, emitted for agents. Before the first command that reaches a cluster, run `kubectl config current-context` and confirm that it names the cluster the person means. A `liken` command that takes a deployment directory reaches that directory's cluster, whatever the context is.

# Take a picture of a screen

This guide shows you how to capture what a monitor shows: one frame
as PNG or JPEG, a clip as MP4, a live MJPEG stream, or a rectangle of
any of these. You need the operator
[installed](https://liken.sh/display/docs/guides/install/) on your
[`liken`](https://liken.sh/docs/) cluster and a connected
[`Display`](https://liken.sh/display/docs/reference/displays/).

`display-api`, a `Deployment` in `liken-system`, serves the captures.
For each request, the capture container in the `display-operator` pod
on the screen's node reads frames from the compositor, and the API
streams the result while it encodes it. Nothing is stored. The
[API reference](https://liken.sh/display/docs/reference/api/) has every route and parameter.

## The `kubectl liken display capture` command

The CLI is the easiest way to capture. `liken plugins sync` installs
the `kubectl liken display` plugin, as
[Install the plugins](https://liken.sh/docs/reference/cli/#install-the-plugins)
describes. `kubectl liken display capture` writes the screen of an
output to stdout as MP4, so you can save it to a file or pipe it to a
player with one command:

    kubectl liken display capture lg-hdr-wqhd-display | mpv -
    kubectl liken display capture lg-hdr-wqhd-display --format png > screen.png

`--format png` writes one frame instead of a clip. A `Display` is
cluster-scoped, so the command takes no namespace.

The CLI compares its version with the operator's version. When they
differ, it prints a warning on stderr that tells you to run
`kubectl liken plugins sync`, and the capture still runs. `--force`
turns the warning off. `--version` prints the CLI's version and exits.

The CLI authenticates with the client certificate in your kubeconfig,
the same identity that `kubectl` uses, so it needs only the permission
in step 1 below. It opens its own port-forward to `display-api`, so it
works from a laptop with no route into the cluster.

`kubectl liken display capture <TAB>` lists the `Display` names in the
cluster, because `kubectl` runs the plugin's completion script. If you call
`kubectl-liken-display` directly, load the completion script with
`source <(kubectl liken display completion bash)`.

The numbered steps below make the HTTP requests yourself. Use them
from an application in the cluster, or to capture JPEG or MJPEG, which
the CLI does not produce.

## 1. Who may capture

You can authenticate with a client certificate or with a Bearer
token. The API checks for a certificate first and then for a token,
the same order as the Kubernetes API server.

When you present a client certificate that the cluster's certificate
authority signed, your user name is the certificate's common name and
your groups are its organization values, the same as for `kubectl`.
The TLS handshake fails for a certificate from any other authority.

Without a certificate, send a Bearer token. The API sends a
`TokenReview` for the audience `display-api`, and refuses the token
unless the review's `status.audiences` includes that audience. A pod's
default service account token has the API server as its audience, so
it does not work here.

The API then sends a `SubjectAccessReview` for the verb `get` on
`displays/screen` in the group `display.liken.sh`. Every route checks
authorization before it reads anything, so a 403 does not reveal
whether a `Display` exists.

The operator ships a `ClusterRole` named `display-capture-viewer`
and does not bind it to anyone. It grants `get` on `displays/screen`
for the capture routes and `get` on `displays` for the info route. Read
your own subject from your kubeconfig:

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

An application gets the permission through its `ServiceAccount`:

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
includes `displays/screen`, and so does `cluster-admin`, so every
subject with one of those roles can see every screen. So can anyone
with `pods/exec` in `liken-system`, because the containers of the
`display-operator` pod share one process namespace, and a shell in
that pod can reach the capture socket.

Every request that returns bytes writes a `Captured` `Event` on the
`Display`, with the message `<subject> captured the screen of <display> as <format>`.

## 2. Reach the API

`display-api` is a `ClusterIP` `Service` at
`https://display-api.liken-system.svc`. Its HTTPS certificate comes
from its own certificate authority. The authority's certificate is in
the `ConfigMap` `display-api-ca` in `liken-system`, under the key
`ca.crt`.

A port-forward works for a still but is too slow for an MJPEG stream,
because it is one TCP connection through the API server. Read a stream
from a pod on the cluster network.

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
          configMap:
            name: display-api-ca
        - name: client
          secret:
            secretName: display-client

The pod is in `liken-system` because a pod can mount a `ConfigMap` or
a `Secret` only from its own namespace. It exits after an hour, so a
pod that you forget to delete does not run forever.

    kubectl apply -f capture-pod.yaml
    kubectl -n liken-system wait --for=condition=Ready pod/capture --timeout 60s

An application needs no `Secret`. It runs as the `ServiceAccount`
that you bound in step 1, mounts a token for the audience
`display-api`, and sends it in an `Authorization: Bearer` header:

    volumes:
      - name: token
        projected:
          sources:
            - serviceAccountToken:
                audience: display-api
                expirationSeconds: 3600
                path: token

For one still, you do not need a pod. The TLS handshake runs through
the port-forward between `curl` and the API, so the API receives your
client certificate:

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
in that pod. `--fail-with-body` makes `curl` exit with a non-zero
status on an error and still write the problem document, which step 4
reads.

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
| `t=` | A W3C Media Fragments time range in seconds. Zero is the moment the capture container accepts the request. `t=,10` records ten seconds from now. `t=5,7` skips five seconds and then records two. `t=5` on a still waits five seconds and then takes one frame. A start time over 60 seconds returns 400, and so does an end time on a still. Without an end time, a clip or a stream runs until you close the connection. |
| `xywh=` | A rectangle of the frame as `x,y,width,height`, in the frame's own physical pixels. `xywh=percent:0,0,50,50` is the same rectangle in percent. A region that extends past an edge is cut off at the edge of the screen. An origin at or past an edge returns 400. |
| `width=`, `height=` | Scale the region down after the crop, and keep the aspect ratio. Setting both, or setting a value larger than the source, returns 400. |
| `framerate=` | Frames per second of a clip or an MJPEG stream. The default is 15, and the maximum is the output's refresh rate. Returns 400 on a still. |
| `quality=` | JPEG quality from 1 to 100. The default is 85. Returns 400 for PNG and MP4. |

The extensions are `.png`, `.jpg`, `.mp4`, and `.mjpeg`. A path
with no extension uses the `Accept` header to choose the format, and
returns `image/png` when you send no `Accept` header.

## 4. Check what you got

Copy a file out of the pod and read it with `ffprobe`:

    kubectl -n liken-system cp capture:/tmp/clip.mp4 clip.mp4
    ffprobe clip.mp4

A clip is H.264 in `mov,mp4,m4a,3gp,3g2,mj2`, at the size of the
screen. A still is one `png` or `mjpeg` frame. A capture with
`xywh=` has the size of the region.

Each capture's `Captured` `Event` is in the `default` namespace,
because a `Display` is cluster-scoped, the same as a `Node`:

    kubectl get events --field-selector reason=Captured

`kubectl describe display` also shows who captured a screen and when.

### When a capture is refused

Every error is an `application/problem+json` document with `type`,
`title`, `status`, `detail`, and `instance`. `curl` wrote it to the
output file, so read that file:

    kubectl -n liken-system exec capture -- cat /tmp/screen.png

| Status | What it means | What to do |
| --- | --- | --- |
| 401 | The request had no client certificate and no token, or the `TokenReview` refused the token | Check that the `Secret` has the certificate and key from the kubeconfig that you use, or create a token for the audience `display-api` |
| 403 | The `SubjectAccessReview` denied the request | Bind `display-capture-viewer` to your subject, as step 1 shows. The `WWW-Authenticate` header names the scope that you need |
| 503 | The `Display` has no node (`no-node`), its compositor is not serving (`compositor-down`), another capture already uses the output (`capture-busy`), or the capture container is missing or not ready (`upstream-failed`) | The response has `Retry-After: 5`. Wait five seconds and try again. `detail` quotes the error from its source |

The info route works even when the screen is down. It returns the
name, the node, and the size and refresh rate that the `Display`
reports. When the screen is down, it also returns `panel:
disconnected`, `compositor: down`, or `sidecar: unreachable`, with the
reason in `detail`:

    kubectl -n liken-system exec capture -- curl -sS --fail-with-body \
      --cacert /ca/ca.crt --cert /client/tls.crt --key /client/tls.key \
      https://display-api.liken-system.svc/v1/display/displays/lg-hdr-wqhd-display

## 5. Clean up

    kubectl -n liken-system delete pod capture
    kubectl -n liken-system delete secret display-client
    rm client.crt client.key

The `ClusterRoleBinding` from step 1 keeps granting access until you
delete it. If you captured only once, delete it too:

    kubectl delete clusterrolebinding display-viewer

## What a capture costs the screen

While a capture runs, the compositor turns off its hardware planes,
so the GL renderer draws a film that a plane would normally show. On a
node with no render node, or whose driver has no VA-API
post-processing, the capture's color conversion runs on the CPU
instead of the GPU. The
`conversion` field of the info route names the pipeline that the node
uses.
