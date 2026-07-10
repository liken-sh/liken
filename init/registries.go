package main

// Rendering k3s's registries.yaml: how this machine pulls container
// images.
//
// registries.yaml is k3s's file for containerd's registry
// arrangements: mirror endpoints to pull through, and the
// credentials to present. k3s reads it once, at process start, when
// it renders containerd's actual configuration — which is exactly
// why registry changes converge by restarting k3s (machine/changes.go)
// and never need a reboot.
//
// Init is the file's sole author, from two inputs. The mirrors and
// the embedded-registry choice come from the cluster document, like
// every fleet-wide fact. The credentials come from their own
// document (the machine package's registries.go), authored by the
// operator from the registry-credentials Secret and riding the same
// staged/proven lifecycle as everything else — in its own store,
// because credentials rotate on their own schedule and must not
// wait on, or trigger, a cluster document edit.
//
// The credentials lifecycle is simpler than the cluster document's
// in one deliberate way: init promotes staged credentials at
// actuation, no attempted marker, no downstream proof. The cluster
// document needs the operator's existence as its proof because its
// failure modes are downstream of the boot (a bad endpoint means
// the machine never joins). A credentials document has no such
// failure mode: k3s starts fine with a wrong password, the symptom
// (ImagePullBackOff) is visible in the cluster, and the fix is a
// Secret edit that flows through as a new document. The file write
// is the whole actuation, init observes it directly, and falling
// back to older credentials on the next boot would repair nothing
// while hiding the newest intent.

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"slices"
	"strings"

	"sigs.k8s.io/yaml"

	"github.com/liken-sh/liken/machine"
)

// registriesConfigPath is where k3s expects the file. A package
// variable so tests can render into a tempdir.
var registriesConfigPath = "/etc/rancher/k3s/registries.yaml"

// The registries.yaml shape, reduced to the keys liken writes.
// Rendered through sigs.k8s.io/yaml (JSON-path marshaling, sorted
// keys) so the same inputs always produce the same bytes.
type registriesFile struct {
	Mirrors map[string]registryMirror `json:"mirrors,omitempty"`
	Configs map[string]registryConfig `json:"configs,omitempty"`
}

type registryMirror struct {
	// Endpoint is k3s's key name; order is preference order.
	Endpoint []string `json:"endpoint,omitempty"`
}

type registryConfig struct {
	Auth registryAuth `json:"auth"`
}

type registryAuth struct {
	Username string `json:"username"`
	Password string `json:"password,omitempty"`
}

// chooseRegistryCredentials selects the credentials document this
// rendering uses: staged (vetted) over proven, recorded in the boot
// record. There is no seed and no image fallback — the operator is
// the document's only author, so a machine that has never had
// credentials staged simply has none, which is the ordinary
// anonymous-pulls state, never an error. A staged document that
// won't parse is rejected on the spot and the rendering falls back
// to proven; unlike the cluster document there is no attempted
// marker (the file comment explains why promotion happens at
// actuation instead).
func chooseRegistryCredentials(stateRoot string, durable bool, boot *machine.BootStatus) *machine.RegistryCredentials {
	if !durable {
		return nil
	}
	store := machine.RegistryCredentialsStore(stateRoot)

	// The standing rejection is republished into the boot record
	// every boot (rejectStagedDocument explains why).
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
			// A proven document that won't parse is a corrupted
			// last-known-good: report it and pull anonymously rather
			// than dying over credentials.
			fmt.Fprintf(os.Stderr, "liken: registries: the proven credentials are unreadable: %v\n", perr)
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
// rendered for the facts. With nothing declared anywhere it removes
// the file instead (a live retraction must take the old rendering
// with it), so k3s's default behavior — every registry reached
// directly, anonymously — stays the default.
//
// When the embedded registry is on, the spec's mirrors render
// verbatim and a bare "*" entry joins them (unless the spec already
// declares one): with Spegel, a registry participates in
// peer-to-peer sharing only if registries.yaml lists it as a mirror,
// and the wildcard is k3s's own way to say "all of them". Turning
// embedded on means "share pulled images across the fleet", and a
// version of that which silently excluded every registry not also
// being mirrored would surprise exactly the deployments that want
// it most.
//
// On success, staged credentials are promoted: the write was the
// whole actuation (the file comment argues why no downstream proof
// exists to wait for). A write failure leaves staged in place for
// the next boot to retry.
func writeRegistriesConfig(cluster *machine.Cluster, creds *machine.RegistryCredentials,
	store machine.ManifestStore, source machine.ManifestSource) machine.RegistriesStatus {
	status := machine.RegistriesStatus{}

	// Both maps are built unconditionally and marshal away when empty
	// (omitempty); the nothing-declared gate below reads their sizes.
	file := registriesFile{
		Mirrors: map[string]registryMirror{},
		Configs: map[string]registryConfig{},
	}
	if cluster != nil {
		for host, endpoints := range cluster.Spec.Registries.Mirrors {
			file.Mirrors[host] = registryMirror{Endpoint: endpoints}
		}
		if cluster.Spec.Registries.Embedded {
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
			fmt.Fprintf(os.Stderr, "liken: registries: removing %s: %v\n", registriesConfigPath, err)
		}
		return status
	}

	raw, err := yaml.Marshal(&file)
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: registries: rendering %s: %v\n", registriesConfigPath, err)
		return status
	}
	// 0600, the join token's posture: this file embeds passwords.
	if err := os.WriteFile(registriesConfigPath, raw, 0o600); err != nil {
		fmt.Fprintf(os.Stderr, "liken: registries: writing %s: %v\n", registriesConfigPath, err)
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
