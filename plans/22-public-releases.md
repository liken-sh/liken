# Public releases

Milestone 22 — Not started

This one is a problem statement, not a design yet.

liken has a releases domain, but it publishes the wrong thing for the
public: versioned builds of one deployment's OS. That is correct for
what it serves today — the fleet-upgrade machinery verifies the exact
bytes a machine boots, and those bytes bake the deployment's identity
and manifests — but it means nothing in releases/dist is publishable.
A liken.cpio carries the cluster's CA private keys and join token;
handing one out hands out the cluster. publish.sh already documents
the symptom: no digest is stable enough to commit, because every
checkout's artifacts embed that checkout's identity.

What's missing is the layer above: releases of liken itself, with no
deployment baked in, so that someone who isn't this repo can run it.
Two halves to that:

- **Public artifacts.** Some form of the OS pieces — the init binary,
  the operator and logs images, the static configuration tree —
  versioned and digest-verified, with no identity and no manifests.
  The vendored inputs (kernel, k3s, xtables, the trust bundle) are
  already pinned by VERSION files and fetched from their own
  upstreams, so a public release may name those pins rather than
  rehost the bytes. What exactly is in the set, and how it is
  verified, is the design work.

- **Utilities for producing a cluster of your own.** The pieces exist
  but only work inside this checkout, driven by make: mint.sh and
  adopt.sh produce an identity, kubeconfig.sh computes a credential,
  and the image build takes IDENTITY, MANIFESTS, and DIST to assemble
  a deployment's bootable images. A person starting from a public
  release needs that workflow without the repo: mint or adopt an
  identity, write a Cluster document and Machine manifests, assemble
  images, and stand up their own release channel for their fleet.
  Whether that is a set of scripts, a small CLI, or a documented
  recipe is part of the design work too.

The two-layer shape this implies: public releases feed image
assembly; image assembly feeds a deployment's own release channel;
the fleet only ever consumes the deployment's channel, because the
digest chain must cover the deployment's actual images. Today's
releases/ domain is that deployment channel under a public-sounding
name, so this milestone likely also decides where the deployment
channel should live (probably with the deployment, next to its
identity and image) and lets releases/ come to mean what it says.

Open questions, deliberately unanswered here: signatures (deferred
with the rest of the hardening tier); whether public artifacts are
downloads or a source tag plus reproducible builds; and how much of
"produce your own cluster" belongs in this repo versus in
documentation.
