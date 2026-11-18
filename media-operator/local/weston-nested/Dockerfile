# The weston image plus the wayland backend, for running weston nested
# in a window on a workstation.
#
# The weston image has only the drm and headless backends, because a
# liken node's compositor drives a graphics card directly. This stage
# installs weston from the Debian snapshot of the weston image, and the image copies
# the wayland backend from it, with libwayland-cursor and libwayland-egl,
# which the weston image lacks. The bake target in the docker-bake.hcl
# beside this file supplies the build contexts weston (the weston image
# of this checkout), builder (the snapshot scripts), and debian (the
# image from toolchains/images.toml), and VERSION (the snapshot date).
FROM debian AS backend
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
# With --debug, weston offers the weston_screenshooter interface, which
# captures a frame of the nested screen.
COPY --from=backend /usr/bin/weston-screenshooter /usr/bin/weston-screenshooter
ENTRYPOINT ["/usr/bin/weston"]
