# The weston image plus the wayland backend.
#
# The weston image carries the drm and headless backends alone, because
# a liken node's compositor owns a card. A nested run on a workstation
# needs the wayland backend, which is the same weston build from the
# same Debian snapshot, with two libraries the image's closure does not
# carry. The bake target in docker-bake.hcl beside this file gives the
# build the weston image of this checkout as the context weston, the
# snapshot scripts as the context builder, and the snapshot date as
# VERSION.
FROM debian:sid-slim@sha256:fb75970a5ba2a81ca409ed0cabcf05d53e058086fd397f7a63943ded5bd15f84 AS backend
ARG VERSION
COPY --from=builder snapshot.sh snapshot.sha256 /
RUN sh /snapshot.sh "$VERSION" \
    && apt-get upgrade -y \
    && apt-get install -y --no-install-recommends weston \
    && rm -rf /var/lib/apt/lists/*

FROM weston
COPY --from=backend /usr/lib/x86_64-linux-gnu/libweston-16/wayland-backend.so \
     /usr/lib/x86_64-linux-gnu/libweston-16/wayland-backend.so
COPY --from=backend /usr/lib/x86_64-linux-gnu/libwayland-cursor.so.0 \
     /usr/lib/x86_64-linux-gnu/libwayland-egl.so.1 \
     /usr/lib/x86_64-linux-gnu/
# --debug advertises weston_screenshooter, which is how a frame of the
# nested screen is captured.
COPY --from=backend /usr/bin/weston-screenshooter /usr/bin/weston-screenshooter
ENTRYPOINT ["/usr/bin/weston"]
