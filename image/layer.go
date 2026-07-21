package image

// This file builds the deployment layer: everything that makes a
// generic liken image one particular deployment's image.
//
// The generic archive that build.sh produces carries the operating
// system and nothing else: no cluster identity, no manifests. This
// file packs the rest, the deployment's half, as a second cpio
// archive. Concatenation joins the two archives: the kernel's
// initramfs unpacker processes concatenated archives in order into
// one filesystem, with later entries overriding earlier ones (the
// same mechanism the install image uses to carry its payload). This
// split is what makes liken releasable: the generic archive's digest
// never changes with the deployment, and producing a bootable image
// from a release is composition, not compilation.
//
// The layer holds these contents, at the paths init and k3s read:
//
//	etc/liken/cluster.yaml       the deployment's cluster document
//	etc/liken/machines/*.yaml    one manifest per machine
//	etc/liken/token              the join token (0600; init hands k3s
//	                             the path, never the value)
//	var/lib/rancher/k3s/         the certificate authorities, exactly
//	  server/tls/**              where k3s looks before generating
//	                             its own
//
// Kernel modules are deliberately not here. The system image carries
// the kernel build's whole module tree, so a machine's declared
// modules (spec.modules) are a boot-time load from that tree, never
// a build-time selection. The layer stays what its name says: the
// deployment's declarations and identity, nothing of the OS.
//
// The identity's kubeconfig, and the admin keypair inside it, stay
// behind on purpose. The kubeconfig is the operator's credential, not
// the machine's. A machine image carrying it would hand cluster-admin
// access to anyone who reads the disk.

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/liken-sh/liken/identity"
)

// Layer packs a deployment's archive from its manifests and identity.
func Layer(manifests, identityDir, out string) error {
	f, err := os.Create(out)
	if err != nil {
		return err
	}
	defer f.Close()
	a := newArchive(f)
	dirs := map[string]bool{}

	// ensure writes the missing parents of a path, root first. This is
	// the order the kernel's unpacker needs to create them.
	ensure := func(path string) error {
		var parents []string
		for d := filepath.Dir(path); d != "."; d = filepath.Dir(d) {
			parents = append(parents, d)
		}
		slices.Reverse(parents)
		for _, d := range parents {
			if !dirs[d] {
				if err := a.dir(d, 0o755); err != nil {
					return err
				}
				dirs[d] = true
			}
		}
		return nil
	}
	ship := func(src, dst string, perm int) error {
		data, err := os.ReadFile(src)
		if err != nil {
			return err
		}
		if err := ensure(dst); err != nil {
			return err
		}
		return a.file(dst, data, perm)
	}

	// This stages the manifests at the paths init reads. It copies
	// them file by file rather than as a tree, so what the layer can
	// carry stays explicit: one cluster document, one manifest per
	// machine.
	if cluster := filepath.Join(manifests, "cluster.yaml"); fileExists(cluster) {
		if err := ship(cluster, "etc/liken/cluster.yaml", 0o644); err != nil {
			return err
		}
	}
	machines, err := filepath.Glob(filepath.Join(manifests, "machines", "*.yaml"))
	if err != nil {
		return err
	}
	for _, m := range machines {
		if err := ship(m, filepath.Join("etc/liken/machines", filepath.Base(m)), 0o644); err != nil {
			return err
		}
	}

	// This stages the identity: the CA tree where k3s looks for it,
	// and the token under /etc/liken, where no disk mount can hide it
	// underneath. The bundle list belongs to the identity package
	// itself, so the layer and the mint can never disagree about what
	// an identity is.
	for _, p := range identity.Bundle {
		src := filepath.Join(identityDir, p)
		dst := filepath.Join("var/lib/rancher/k3s/server", p)
		perm := 0o600
		if strings.HasSuffix(p, ".crt") {
			perm = 0o644
		}
		if p == "token" {
			dst = "etc/liken/token"
		}
		if err := ship(src, dst, perm); err != nil {
			return err
		}
	}

	return a.close()
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
