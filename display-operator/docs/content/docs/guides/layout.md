---
title: Put regions on a screen
weight: 30
---

# Put regions on a screen

This guide puts two programs from two namespaces on one monitor, each
in its own rectangle: a notice board on the left seven tenths of a
lobby screen and a parking-lot camera in the upper right. You need the
operator [installed](/docs/guides/install/) and the
[claim guide](/docs/guides/claim/) read, because each program gets to
the screen the way that guide shows.

A screen with no `Layout` shows every window fullscreen, with the
newest on top. A `Layout` divides the screen into regions, and each
region shows the window of a pod whose labels match the region's
selector. The pods never learn where they are drawn. The `Layout` is
the only place the arrangement lives, and one `Layout` serves any
number of screens.

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

A `Layout` is cluster-scoped, like a `DeviceClass`, and nothing in it
names a namespace or a monitor. Each rectangle is four fractions of
the screen, so the same `Layout` fits a 1080p panel and a 4K one. The
order of the list is the stacking order: a region written after
another draws over it where they overlap, which is how a small
picture sits in the corner of a large one.

The selector matches labels the way a `Service` does, and any label
counts. The candidates are only the pods that hold a claim on this
screen, so a `panel` label on a pod elsewhere in the cluster matches
nothing here. A region shows one window, the first to arrive from a
matching pod. A second matching window stays off the screen and is
reported, and a region with no matching pod is empty and is reported.

A `transition` has two halves, and each is `fade` over the stated
milliseconds or `none`. The `enter` half runs when a window enters
its region, and a fade brings the window from transparent to opaque.
The `exit` half runs when a window that is still drawing stops
matching the region, which is what a label a controller removes does,
and a fade brings the window back to transparent while the program
keeps drawing. A program that exits takes its window with it, and
nothing fades a window the compositor no longer holds.

## 2. Name it on the `Display`

    kubectl patch display boe-1080-display --type merge \
      -p '{"spec": {"layout": "front-desk"}}'

`spec.layout` is the one field a screen has for this. Change the name
to change the arrangement, and delete it to return to fullscreen
windows. A name that matches no `Layout` shows the fullscreen
arrangement and reports the name under a `LayoutResolved` condition.

## 3. Label the pods

Each program claims the screen from its own namespace, exactly as
the claim guide shows. The one addition is the label the region's
selector reads. The camera, in `facilities`:

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

The notice board, in `frontdesk`, is the same shape with its own
claim on the same connector and the label `panel: notices`. Two
namespaces each hold a claim on one screen, because the screen's draw
device allows many claims at once.

When a window lands in a region, the compositor tells the program the
region's size and the program redraws at that size, the way it would
if you dragged a window's edge. A page reflows, and a video player
letterboxes inside its rectangle. mpv needs `--keepaspect-window=no`
to do that, because its default keeps the window at the film's own
aspect and the compositor then scales that smaller buffer up to the
region. A program that ignores the request is scaled to fit.

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

Every window the compositor holds is in `surfaces`, with the claim it
arrived through, the pods that hold that claim, their labels, its
current size, and the region it is in. A window that matches no
region is listed with no `region`, which is the first thing to read
when a program is running and not on the screen. A region that shows
nothing reads `surface: empty`.

## A window for a while

A camera that should show for fifteen seconds is a `Job` whose image
streams for fifteen seconds, with the claim and the label above. Its
window appears in its region when the pod starts and leaves when the
pod ends, and the region reads `empty` again. Nothing in the `Layout`
reads a clock: how long a window stays is the program's own business.

## What a `Layout` does not do

It does not start anything. A region with a selector and no matching
pod stays empty until something creates a pod with that label and a
claim on the screen. A `Deployment`, a `Job`, or an operator that
makes pods is what fills a screen, and the `Layout` only says where.

It does not fill a region with two windows. A second window from a
matching pod stays off the screen until the first is gone.

It does not move a window a program is drawing. A program never
learns its rectangle, and it cannot ask for another one. Move the
region in the `Layout` and every screen that names it follows.
