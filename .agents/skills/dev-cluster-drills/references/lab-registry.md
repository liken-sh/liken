# Run an image you built on the laptop

Run every command here from `liken/`, as in `SKILL.md`.

The lab registry lets the guests pull an operator image that you built
on the laptop, with no push to ghcr.io. It is a `registry:3.0.0`
container named `liken-lab-registry`, on the laptop's
`127.0.0.1:5000`. `dev-cluster/cluster.yaml` declares it as the
`spec.registries` mirror for `ghcr.io`, at `http://10.0.2.2:5000`. So
a manifest keeps its `ghcr.io/liken-sh/...` image reference:

```bash
make registry
dev-cluster/registry/push.sh observatory-operator
dev-cluster/registry/push.sh ghcr.io/liken-sh/indi-simulators:20261005-5
```

`push.sh` accepts a target of the root `docker-bake.hcl`, or the
reference of an image that the local docker daemon has. It builds a
target as `ghcr.io/liken-sh/<name>:dev` unless you set `VERSION`.
ghcr.io has no `:dev` tag, so a pod that uses one pulls only from the
lab registry. For every other `ghcr.io` image, the pull falls back to
ghcr.io when the lab registry does not have the image or is stopped.
`docker logs liken-lab-registry` shows each pull, with the user agent
`containerd`. The images are stored in `dev-cluster/registry/storage/`.
Neither `make registry-stop` nor `make -C dev-cluster clean` deletes
them.

When the build cannot resolve names, set
`BAKE_FLAGS="--set *.network=host --allow network.host"`. This
happens on a VPN whose DNS server responds to the laptop but not to
its containers.

When the drill ends, run `docker stop liken-lab-registry`, as the
"Clean up" section of `SKILL.md` says.
