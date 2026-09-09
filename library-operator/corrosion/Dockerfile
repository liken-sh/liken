# syntax=docker/dockerfile:1

# The Corrosion sidecar's image. A Corrosion agent runs beside every
# scanner and every media browser and replicates the catalog to them,
# and a second agent from the same image replicates the progress store.
#
# The binary comes from liken's fork of Corrosion,
# https://github.com/liken-sh/corrosion, which publishes upstream plus
# a short stack of upstream-bound changes as one image. This file
# pins that image by digest, so every agent in a cluster runs the
# same bytes, and a move to a newer Corrosion is a change to this
# line that a release carries. The fork's liken/README.md lists what
# its stack holds at each release.
FROM ghcr.io/liken-sh/corrosion:2026.09.09-002@sha256:a17aff43ee315f48a3e2f5a073dab4638e4fd592eea3231eb40b6a7f2602b9cb

# The configurations and the schemas arrive in the image because the
# pod has no shell to render a file and no `ConfigMap` for the sidecar.
# Each cluster has a pair: `config.toml` with `schema/` for the catalog,
# and `progress.toml` with `progress-schema/` for the progress store.
# So every agent this image starts loads the same schema as its peers.
COPY config.toml /etc/corrosion/config.toml
COPY schema/ /etc/corrosion/schema/
COPY progress.toml /etc/corrosion/progress.toml
COPY progress-schema/ /etc/corrosion/progress-schema/

# The entrypoint is the fork image's bare binary, so `corrosion cluster
# members` and the other commands still run from this image with their
# own arguments. The default command is the catalog agent, and a
# progress agent names `progress.toml` in its own arguments.
CMD ["agent", "--config", "/etc/corrosion/config.toml"]
