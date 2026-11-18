package main

// Init writes k3s's registries.yaml, which sets the mirrors that
// containerd pulls images through and the credentials it sends. k3s
// reads the file only at process start, so a registry change takes
// effect at a k3s restart (see cluster/changes.go) with no reboot.
//
// The mirrors and the embedded-registry setting come from the cluster
// document. The credentials come from their own document (see
// registries.go in the machine package), which the machine operator
// writes from the registry-credentials Secret. That document has its
// own staged and proven store, so a credentials rotation does not wait
// for a cluster document edit and does not cause one.
//
// Init promotes the staged credentials to proven as soon as it writes
// the file, with no attempted marker. The cluster document needs a
// trial because a bad one stops the machine from joining, and only the
// machine operator, running as a pod, can prove that the join worked.
// Bad credentials do not stop the boot: k3s starts, pods fail with
// ImagePullBackOff, and the fix is a Secret edit that arrives as a new
// document. Falling back to older credentials would not fix the pulls
// and would hide the newest credentials.

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"slices"
	"strings"

	"sigs.k8s.io/yaml"

	"github.com/liken-sh/liken/liken/cluster"
	"github.com/liken-sh/liken/liken/machine"
)

// registriesConfigPath is where k3s reads the file. Tests set it to a
// temporary directory.
var registriesConfigPath = "/etc/rancher/k3s/registries.yaml"

// registriesFile is registries.yaml, reduced to the keys that liken
// writes. sigs.k8s.io/yaml marshals through JSON tags and sorts map
// keys, so the same inputs always produce the same bytes.
type registriesFile struct {
	Mirrors map[string]registryMirror `json:"mirrors,omitempty"`
	Configs map[string]registryConfig `json:"configs,omitempty"`
}

type registryMirror struct {
	// k3s spells the key in the singular, and tries the endpoints in
	// order.
	Endpoint []string `json:"endpoint,omitempty"`
}

type registryConfig struct {
	Auth registryAuth `json:"auth"`
}

type registryAuth struct {
	Username string `json:"username"`
	Password string `json:"password,omitempty"`
}

// chooseRegistryCredentials returns the credentials to render and
// records which document they came from in the boot record. It uses
// the staged document when there is one that parses, and otherwise the
// proven one. A staged document that does not parse is rejected at
// once, and the proven document is used instead. When neither exists
// it returns nil and the machine pulls images anonymously, which is
// normal for a machine that has never had credentials. The image has
// no default copy, because the operator is the only source of the
// document.
func chooseRegistryCredentials(stateRoot string, durable bool, boot *machine.BootStatus) *machine.RegistryCredentials {
	if !durable {
		return nil
	}
	store := machine.RegistryCredentialsStore(stateRoot)

	// The boot record starts empty on every boot, so copy in the
	// rejection that the store kept from an earlier boot.
	boot.CredentialsRejection, _ = store.LoadRejection()

	if raw, err := store.LoadStaged(); err != nil {
		fmt.Fprintf(os.Stderr, "liken: registries: the staged credentials are unreadable: %v\n", err)
	} else if raw != nil {
		c, perr := machine.ParseRegistryCredentials(raw)
		if perr != nil {
			boot.CredentialsRejection = rejectStagedDocument("registries", "credentials", store.Reject,
				raw, fmt.Sprintf("the staged credentials document does not parse: %v", perr))
		} else {
			boot.CredentialsSource = machine.ManifestSourceStaged
			boot.CredentialsHash = machine.ManifestHash(raw)
			return c
		}
	}

	if raw, err := store.LoadProven(); err != nil {
		fmt.Fprintf(os.Stderr, "liken: registries: the proven credentials are unreadable: %v\n", err)
	} else if raw != nil {
		c, perr := machine.ParseRegistryCredentials(raw)
		if perr != nil {
			// A proven document that does not parse is a corrupted
			// copy. Log it and pull anonymously, because a credentials
			// problem must not stop the boot.
			fmt.Fprintf(os.Stderr, "liken: registries: the proven credentials document does not parse: %v\n", perr)
		} else {
			boot.CredentialsSource = machine.ManifestSourceProven
			boot.CredentialsHash = machine.ManifestHash(raw)
			return c
		}
	}
	return nil
}

// writeRegistriesConfig renders registries.yaml from the cluster
// document's mirrors and the chosen credentials, and returns what it
// rendered for the facts. When nothing is declared, it removes the
// file, so that removing the last mirror or credential also removes
// the old rendering. k3s then pulls from every registry directly and
// anonymously.
//
// When the embedded registry is on, the spec's mirrors render as
// declared and a "*" entry is added unless the spec already has one.
// Spegel shares images between nodes only for registries that
// registries.yaml lists as mirrors, and "*" is k3s's wildcard for all
// registries. Without it, turning the embedded registry on would share
// only the registries that were already mirrored.
//
// After a successful write, it promotes the staged credentials (see
// the comment at the top of the file). When the write fails, the
// credentials stay staged, so the next boot retries.
func writeRegistriesConfig(clusterDoc *cluster.Cluster, creds *machine.RegistryCredentials,
	store machine.ManifestStore, source machine.ManifestSource) machine.RegistriesStatus {
	status := machine.RegistriesStatus{}

	// Both maps always exist. omitempty drops an empty map from the
	// output, and the nothing-declared check below tests their sizes.
	file := registriesFile{
		Mirrors: map[string]registryMirror{},
		Configs: map[string]registryConfig{},
	}
	if clusterDoc != nil {
		for host, endpoints := range clusterDoc.Spec.Registries.Mirrors {
			file.Mirrors[host] = registryMirror{Endpoint: endpoints}
		}
		if clusterDoc.Spec.Registries.Embedded {
			status.Embedded = true
			if _, declared := file.Mirrors["*"]; !declared {
				file.Mirrors["*"] = registryMirror{}
			}
		}
	}
	if creds != nil {
		for _, h := range creds.Hosts {
			file.Configs[h.Host] = registryConfig{Auth: registryAuth{
				Username: h.Username,
				Password: h.Password,
			}}
		}
	}

	if len(file.Mirrors) == 0 && len(file.Configs) == 0 {
		if err := os.Remove(registriesConfigPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
			fmt.Fprintf(os.Stderr, "liken: registries: removing the old rendering: %v\n", err)
		}
		return status
	}

	raw, err := yaml.Marshal(&file)
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: registries: rendering registries.yaml: %v\n", err)
		return status
	}
	// 0600 as for the join token, because the file contains passwords.
	if err := os.WriteFile(registriesConfigPath, raw, 0o600); err != nil {
		fmt.Fprintf(os.Stderr, "liken: registries: writing the rendering: %v\n", err)
		return status
	}

	status.Mirrors = slices.Sorted(maps.Keys(file.Mirrors))
	status.CredentialedHosts = slices.Sorted(maps.Keys(file.Configs))
	for _, host := range status.Mirrors {
		if host == "*" {
			fmt.Println("liken: registries: the embedded registry shares every registry's images across the fleet")
			continue
		}
		fmt.Printf("liken: registries: mirroring %s via %s\n",
			host, strings.Join(file.Mirrors[host].Endpoint, ", "))
	}
	if len(status.CredentialedHosts) > 0 {
		fmt.Printf("liken: registries: credentials for %s\n", strings.Join(status.CredentialedHosts, ", "))
	}

	if source == machine.ManifestSourceStaged {
		if err := store.Promote(); err != nil {
			fmt.Fprintf(os.Stderr, "liken: registries: promoting the staged credentials: %v\n", err)
		} else {
			fmt.Println("liken: registries: the staged credentials are now proven")
		}
	}
	return status
}
