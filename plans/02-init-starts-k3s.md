# Init starts k3s and nothing else

Milestone 2 — Done

Init starts k3s and nothing else. The team discovered every host
dependency (cgroups, kernel modules, time, entropy) by running
into each one directly, not by reading about it.
1. [x] Boot to network from a Machine manifest (`kind: Machine`,
   `apiVersion: liken.sh/v1alpha1`, delivered as a file at boot).
   Bring up the interface, run DHCP (the whole DORA exchange
   prints to the console), apply the lease through netlink, and
   prove the network works with a DNS lookup against an outside
   nameserver. We predicted that entropy would be the dependency
   that surfaced here. Without RDRAND the kernel RNG never
   initializes, so getrandom() blocks forever in the DHCP client.
2. [x] Boot to a Ready node. Init sets up everything k3s assumes
   exists: cgroup2, identity files, mount propagation, module
   preload, and iptables, on a system with no shell. Init
   supervises k3s with backoff, and prints node and pod state to
   the console. `make run` ends at a settled single-node cluster.
   `make run-once` (`liken.oneshot`) powers the machine off
   whenever k3s exits, so a harness can measure the boot.
3. [x] Machine identity is an input to the build, not something the
   build extracts from a running machine. `make` mints a CA
   bundle (gitignored, in identity/) and pre-seeds k3s's TLS
   directory in the image. Because of this, the build computes an
   operator's kubeconfig offline, and never copies it off the
   machine. `make kubeconfig`, together with a loopback-only QEMU
   port-forward, gets `kubectl get nodes` working from the host.
   This needs no `--tls-san` flag, because k3s's serving
   certificate covers 127.0.0.1 by default. This work led to one
   discovery: kube-apiserver reads the ServiceAccount key with a
   parser that accepts the SEC1 "EC PRIVATE KEY" format but not
   PKCS#8.
4. [x] The Kubernetes API is the machine API. The Machine manifest
   is now a real CRD (`kubectl get machines` works), and a liken
   operator reconciles it. The operator ships inside the
   initramfs as a hand-assembled OCI tarball (operator/image.sh)
   and deploys through k3s's auto-manifests directory. Because of
   this, the system needs no registry pulls and no kubectl steps.
   Init never talks to k3s. Init applies spec.sysctls at boot and
   writes facts to `/run/liken/`. The operator seeds the Machine
   resource from the manifest, publishes facts and observed
   sysctls into status, and reconciles sysctl edits while the
   machine runs. The operator uses plain net/http against the API
   server instead of client-go, because writing the watch loop by
   hand teaches the lesson. The shared Go types live in the
   machine/ domain, and both programs use them. This work mostly
   solved one leftover mystery: the message "The manifest file is
   empty, ignoring." fires once for each embedded control-plane
   component as it parses its options. The message has nothing to
   do with k3s's manifests directory.
