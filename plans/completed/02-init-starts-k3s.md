# Init starts k3s and nothing else

Milestone 02 — Completed. Init prepares every host dependency that k3s
needs, then supervises k3s as its only child process.

The host dependencies are cgroups, kernel modules, time, and entropy.
Init supplies all of them on a system with no shell.
1. [x] Boot to network from a Machine manifest (`kind: Machine`,
   `apiVersion: liken.sh/v1alpha1`, delivered as a file at boot). Bring
   up the interface, run DHCP (the full DORA exchange prints to the
   console), apply the lease through netlink, and prove that the network
   works with a DNS lookup against an outside nameserver. Entropy is the
   dependency that appears here. Without RDRAND the kernel RNG does not
   initialize, so getrandom() blocks forever in the DHCP client.
2. [x] Boot to a Ready node. Init sets up everything that k3s needs:
   cgroup2, identity files, mount propagation, module preload, and
   iptables. Init supervises k3s with backoff, and prints the node and
   pod state to the console. `make run` ends at a settled single-node
   cluster. `make run-once` (`liken.oneshot`) powers the machine off
   when k3s exits, so that a harness can measure the boot.
3. [x] Machine identity is an input to the build, and not something that
   the build extracts from a running machine. `make` mints a CA bundle
   (gitignored, in identity/) and pre-seeds the k3s TLS directory in the
   image. Thus the build computes an operator kubeconfig offline, and
   never copies it off the machine. `make kubeconfig`, together with a
   loopback-only QEMU port-forward, makes `kubectl get nodes` work from
   the host. This needs no `--tls-san` flag, because the k3s serving
   certificate covers 127.0.0.1 by default. Note that kube-apiserver
   reads the ServiceAccount key with a parser that accepts the SEC1 "EC
   PRIVATE KEY" format, but not PKCS#8.
4. [x] The Kubernetes API is the machine API. The Machine manifest is
   now a real CRD (`kubectl get machines` works), and a liken operator
   reconciles it. The operator ships inside the initramfs as a
   hand-assembled OCI tarball (operator/image.sh) and deploys through
   the k3s auto-manifests directory. Thus the system needs no registry
   pulls and no kubectl steps. Init never talks to k3s. Init applies
   spec.sysctls at boot and writes facts to `/run/liken/`. The operator
   seeds the Machine resource from the manifest, publishes the facts and
   the observed sysctls into status, and reconciles sysctl edits while
   the machine runs. The operator uses plain net/http against the API
   server in place of client-go, because a hand-written watch loop shows
   the reader how the Kubernetes watch protocol works. The shared Go
   types live in the machine/ domain, and both programs use them. This
   work also explains an old console message. "The manifest file is
   empty, ignoring." comes once from each embedded control-plane
   component as it parses its options. The message is not about the k3s
   manifests directory.
