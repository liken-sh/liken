# The released weston image plus the wayland backend.
#
# The image display-operator ships carries the drm and headless backends
# alone, because a liken node's compositor owns a card. A nested run on a
# workstation needs the wayland backend, which is the same weston build from
# the same Debian suite, with two libraries the released closure does not
# carry.
FROM debian:trixie-slim AS backend
RUN apt-get update \
    && apt-get install -y --no-install-recommends weston \
    && rm -rf /var/lib/apt/lists/*

FROM ghcr.io/liken-sh/weston:2026.09.11-002
COPY --from=backend /usr/lib/x86_64-linux-gnu/libweston-14/wayland-backend.so \
     /usr/lib/x86_64-linux-gnu/libweston-14/wayland-backend.so
COPY --from=backend /usr/lib/x86_64-linux-gnu/libwayland-cursor.so.0 \
     /usr/lib/x86_64-linux-gnu/libwayland-egl.so.1 \
     /usr/lib/x86_64-linux-gnu/
# --debug advertises weston_screenshooter, which is how a frame of the
# nested screen is captured.
COPY --from=backend /usr/bin/weston-screenshooter /usr/bin/weston-screenshooter
ENTRYPOINT ["/usr/bin/weston"]
