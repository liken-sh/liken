---
name: layout
description: "Put programs from several namespaces on one monitor, each in its own rectangle, with a Layout. Use when two or more pods must share one screen, or when a window should show for a while and then leave."
---

This skill is the guide at https://liken.sh/display/docs/guides/layout/, emitted for agents. Before the first command, run `kubectl config current-context` and confirm that it names the cluster the person means.

# Put regions on a screen

A `Layout` lets pods from different namespaces share one monitor, each
drawing in its own rectangle. This guide puts a notice board on the
left 70% of a lobby screen and a parking-lot camera in the upper
right. You need the operator [installed](https://liken.sh/display/docs/guides/install/) and
you should have read the [claim guide](https://liken.sh/display/docs/guides/claim/), because
each program's claim has the shape that guide shows.

A screen with no `Layout` shows every window fullscreen, with the
newest on top. A `Layout` divides the screen into regions. Each region
has a label selector and shows the window of a pod whose labels match
it. Any number of screens can name the same `Layout`.

## 1. Write the `Layout`

    apiVersion: display.liken.sh/v1alpha1
    kind: Layout
    metadata:
      name: front-desk
    spec:
      regions:
        - name: notices
          rect: {left: 0, top: 0, width: 0.7, height: 1}
          selector:
            matchLabels: {panel: notices}
        - name: lot
          rect: {left: 0.7, top: 0, width: 0.3, height: 0.6}
          selector:
            matchLabels: {panel: parking-lot}
          transition:
            enter: {kind: fade, milliseconds: 300}
            exit: {kind: fade, milliseconds: 300}

A `Layout` is cluster-scoped, like a `DeviceClass`, and it names no
namespace and no monitor. Each rectangle is four fractions of the
screen, so the same `Layout` fits a 1080p panel and a 4K one. The
order of `regions` is the stacking order. Where two regions overlap,
the one written later is drawn on top, which is how you put a small
picture in the corner of a large one.

The selector works like a `Service` selector, and it can match on any
label. It matches only pods that hold a claim on this screen, so a pod
elsewhere in the cluster with a `panel` label matches nothing here. A
region shows the windows of one claim, the earliest claim on the
screen whose pod matches. All of that claim's windows are drawn in the
region, newest on top, so a player's overlay stays above its video.
When a second claim also matches, its windows are not drawn until the
first claim's windows are gone. A region that no pod matches stays
empty. Section 4 shows how to find both cases in the status.

A `transition` has an `enter` half and an `exit` half. Each is a
`fade` of the given `milliseconds`, or `none`. The `enter` half runs
when a window arrives in its region, and a fade takes the window from
transparent to opaque. The `exit` half runs when a window that is
still drawing stops matching the region, for example because a
controller removed its label. A fade then takes the window to
transparent while the program keeps drawing. When a program exits, its
window disappears at once, with no fade.

## 2. Name it on the `Display`

    kubectl patch display boe-1080-display --type merge \
      -p '{"spec": {"layout": "front-desk"}}'

`spec.layout` holds the name of the `Layout`. Change the name to change
the arrangement, and delete the field to return to fullscreen windows.
When the name matches no `Layout`, the screen shows fullscreen windows
and the `LayoutResolved` condition is `False` with the reason
`LayoutNotFound`.

## 3. Label the pods

Each program claims the screen's draw device from its own namespace.
The claim has the shape that the claim guide shows, and the pod adds
the label that the region's selector reads. This is the camera, in the
`facilities` namespace:

    apiVersion: apps/v1
    kind: Deployment
    metadata:
      name: parking-lot-view
      namespace: facilities
    spec:
      replicas: 1
      strategy:
        type: Recreate
      selector:
        matchLabels:
          app: parking-lot-view
      template:
        metadata:
          labels:
            app: parking-lot-view
            panel: parking-lot
        spec:
          resourceClaims:
            - name: screen
              resourceClaimName: front-desk-screen
          containers:
            - name: player
              image: <your mpv image>
              args: ["rtsp://cameras/parking-lot"]
              resources:
                claims:
                  - name: screen

The notice board, in the `frontdesk` namespace, has the same shape,
with the label `panel: notices`. Each program claims the connector's
draw device, not its output device. The output device accepts one
claim, because that claim can set the panel's mode, so a second claim
through `display-output` stays `Pending`. The draw device has the
name of the output with a `-draw` suffix, and many claims can hold it
at the same time. Create a class for it, as you created
`display-output`:

    apiVersion: resource.k8s.io/v1
    kind: DeviceClass
    metadata:
      name: display-draw
    spec:
      selectors:
        - cel:
            expression: |
              device.driver == "display.liken.sh" &&
              has(device.attributes["display.liken.sh"].draw)

Name `display-draw` in each claim, and keep the `connector` selector
and the `display.liken.sh/disconnected` toleration from the claim
guide. The draw device has the same taints as the output device. A
draw claim sets neither the panel's mode nor its power, because a
write from one claim would change the screen for the others.

When a window is placed in a region, the compositor tells the program
the region's size, and the program redraws at that size, as it does
when you resize a window by its edge. A web page reflows, and a video
player letterboxes the picture inside the rectangle. `mpv` letterboxes
only with `--keepaspect-window=no`. By default it keeps the window at
the video's own aspect ratio, and the compositor scales that smaller
window up to fit the region. The compositor also scales the window of
any program that does not redraw at the new size.

## 4. Read what the screen shows

    kubectl get display boe-1080-display -o yaml

    status:
      layout:
        name: front-desk
        regions:
          - name: notices
            surface: 7c1e-1
          - name: lot
            surface: a940-1
      surfaces:
        - id: 7c1e-1
          claim: frontdesk/front-desk-screen
          pods: [frontdesk/notices-6b9d7-q2xw]
          labels: {app: notices, panel: notices}
          size: {width: 1344, height: 1080}
          region: notices
        - id: a940-1
          claim: facilities/front-desk-screen
          pods: [facilities/parking-lot-view-7d9f-x2k1]
          labels: {app: parking-lot-view, panel: parking-lot}
          size: {width: 576, height: 648}
          region: lot

`surfaces` lists every window the compositor has. Each entry gives the
claim the window arrived through, the pods that hold that claim, their
labels, the window's current size, and the region it is in. When the
program sets an xdg `app_id`, the entry shows it as `appID`. A window
that matches no region, including a window of a second matching claim,
has no `region` field. Look for that first when a program is running
and does not appear on the screen. A region with no window shows
`surface: empty`.

## A window for a while

To show a camera for 15 seconds, run a `Job` whose image streams for 15
seconds, with the claim and the label above. Its window appears in its
region when the pod starts and goes away when the pod ends, and the
region shows `empty` again. A `Layout` has no timer, so the window
shows for as long as the program runs.

## What a `Layout` does not do

A `Layout` does not start any program. A `Deployment`, a `Job`, or an
operator that creates pods supplies the programs, and the `Layout`
only decides where they draw.

A program cannot move its region or learn where it is on the screen.
To move a region, edit the `Layout`, and every screen that names it
follows.

A `Layout` does not arrange the windows inside a region. One client of
a claim can do that through the `liken_arrangement_v1` Wayland
protocol. That client is the claim's arranger, and it places the
claim's windows inside the region, for example a film that shrinks
into a frame while a panel slides in beside it. The arranger cannot
move a window outside the region. The
[arrangement reference](https://liken.sh/display/docs/reference/arrangement/) describes the
protocol, and `status.arrangers` on the `Display` lists the claims
that have an arranger.
