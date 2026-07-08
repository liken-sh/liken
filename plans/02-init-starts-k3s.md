# Init starts k3s and nothing else

Milestone 2 — Done

Init starts k3s and nothing else, and discover every host
dependency (cgroups, kernel modules, time, entropy) by running
into each one directly rather than reading about it.
1. [x] Boot to network from a Machine manifest (`kind: Machine`,
   `apiVersion: liken.sh/v1alpha1`, file-delivered at boot): bring
   up the interface, run DHCP (the whole DORA exchange prints to
   the console), apply the lease via netlink, and prove it with a
   DNS lookup against an outside nameserver. Entropy was the
   dependency we predicted would surface here: without RDRAND the
   kernel RNG never initializes, so getrandom() blocks forever in
   the DHCP client.
2. [x] Boot to a Ready node: init sets up everything k3s assumes
   exists (cgroup2, identity files, mount propagation, module
   preload, and iptables on a system with no shell), supervises
   k3s with backoff, and prints node and pod state to the
   console. `make run` ends at a settled single-node cluster;
   `make run-once` (`liken.oneshot`) powers the machine off
   whenever k3s exits, so a harness can measure the boot.
3. [x] Machine identity is an input to the build, not something
   extracted from a running machine: `make` mints a CA bundle
   (gitignored, identity/) and pre-seeds k3s's TLS directory in
   the image, so an operator's kubeconfig is computed offline
   and never copied off the machine. `make kubeconfig` plus a
   loopback-only QEMU port-forward gets `kubectl get nodes` from
   the host; no `--tls-san` needed, since k3s's serving cert
   covers 127.0.0.1 by default. One discovery came out of this:
   kube-apiserver reads the ServiceAccount key with a parser
   that accepts SEC1 "EC PRIVATE KEY" but not PKCS#8.
4. [x] The Kubernetes API is the machine API: the Machine manifest is
   now a real CRD (`kubectl get machines` works), reconciled by
   a liken operator. The operator ships inside the initramfs as
   a hand-assembled OCI tarball (operator/image.sh) and deploys
   through k3s's auto-manifests directory, so there are no
   registry pulls and no kubectl steps. Init never talks to k3s:
   it applies spec.sysctls at boot and writes facts to
   `/run/liken/`. The operator seeds the Machine from the
   manifest, publishes facts and observed sysctls into status,
   and reconciles sysctl edits live. It uses plain net/http
   against the API server rather than client-go, because writing
   the watch loop by hand is the lesson. The shared Go types
   live in the machine/ domain, used by both programs. A
   leftover mystery got mostly solved along the way: "The
   manifest file is empty, ignoring." fires once per embedded
   control-plane component as it parses its options, and has
   nothing to do with k3s's manifests directory.
