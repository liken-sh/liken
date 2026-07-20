package main

// Rendering k3s's registries.yaml: how this machine pulls container
// images.
//
// registries.yaml is k3s's file for containerd's registry settings:
// mirror endpoints to pull through, and the credentials to present.
// k3s reads this file once, at process start, when it renders
// containerd's actual configuration. This is why registry changes
// take effect only when k3s restarts (see cluster/changes.go), and
// never need a reboot.
//
// Init is the sole author of this file, and it uses two inputs. The
// mirrors and the embedded-registry choice come from the cluster
// document, like every fleet-wide fact. The credentials come from
// their own document (see registries.go in the machine package).
// The operator authors this document from the registry-credentials
// Secret. The document follows the same staged/proven lifecycle as
// every other document, but it has its own store, because
// credentials rotate on their own schedule. A credentials change
// must not wait for a cluster document edit, and must not trigger
// one either.
//
// The credentials lifecycle is simpler than the cluster document's
// lifecycle in one deliberate way: init promotes staged credentials
// at actuation, with no attempted marker and no downstream proof.
// The cluster document needs the operator's existence as its proof,
// because its failure modes appear only after the boot completes (a
// bad endpoint means the machine never joins the cluster). A
// credentials document has no such failure mode. k3s starts fine
// even with a wrong password. The symptom (ImagePullBackOff) is
// visible in the cluster, and the fix is a Secret edit that flows
// through as a new document. Writing the file is the whole
// actuation, and init observes the result directly. Falling back to
// older credentials on the next boot would repair nothing, and it
// would also hide the newest intent.

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"slices"
	"strings"

	"sigs.k8s.io/yaml"

	"github.com/liken-sh/liken/cluster"
	"github.com/liken-sh/liken/machine"
)

// registriesConfigPath is the path where k3s expects the file. It is
// a package variable, so tests can render into a temporary
// directory.
var registriesConfigPath = "/etc/rancher/k3s/registries.yaml"

// registriesFile is the registries.yaml shape, reduced to the keys
// that liken writes. It renders through sigs.k8s.io/yaml, which uses
// JSON-path marshaling and sorts keys, so the same inputs always
// produce the same bytes.
type registriesFile struct {
	Mirrors map[string]registryMirror `json:"mirrors,omitempty"`
	Configs map[string]registryConfig `json:"configs,omitempty"`
}

type registryMirror struct {
	// Endpoint is k3s's key name. The order of endpoints is the order
	// of preference.
	Endpoint []string `json:"endpoint,omitempty"`
}

type registryConfig struct {
	Auth registryAuth `json:"auth"`
}

type registryAuth struct {
	Username string `json:"username"`
	Password string `json:"password,omitempty"`
}

// chooseRegistryCredentials selects the credentials document that
// this rendering uses. It prefers the staged (vetted) document over
// the proven document, and it records the choice in the boot record.
// There is no seed document and no image fallback, because the
// operator is the only author of this document. A machine that has
// never had credentials staged simply has none. This is the normal
// anonymous-pulls state, not an error. The function rejects a staged
// document that does not parse immediately, and the rendering falls
// back to the proven document. Unlike the cluster document, this
// document has no attempted marker. (The file comment above explains
// why promotion happens at actuation instead.)
func chooseRegistryCredentials(stateRoot string, durable bool, boot *machine.BootStatus) *machine.RegistryCredentials {
	if !durable {
		return nil
	}
	store := machine.RegistryCredentialsStore(stateRoot)

	// The function republishes the standing rejection into the boot
	// record on every boot. (rejectStagedDocument explains why.)
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
			// last-known-good copy. The function reports this and pulls
			// images anonymously, instead of stopping because of a
			// credentials problem.
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
// document's mirrors and the chosen credentials, and it returns what
// it rendered for the facts. When nothing is declared anywhere, the
// function removes the file instead. (A live retraction must remove
// the old rendering too.) This keeps k3s's default behavior in
// place: every registry is reached directly, and anonymously.
//
// When the embedded registry is on, the spec's mirrors render
// exactly as declared, and a bare "*" entry joins them, unless the
// spec already declares one. With Spegel, a registry participates in
// peer-to-peer sharing only if registries.yaml lists it as a mirror,
// and the wildcard is k3s's way to say "all registries". Turning the
// embedded registry on means "share pulled images across the
// fleet". If the function silently excluded every registry that was
// not also mirrored, it would surprise the deployments that want
// this feature most.
//
// On success, the function promotes the staged credentials, because
// the write was the whole actuation. (The file comment above
// explains why no downstream proof exists to wait for.) A write
// failure leaves the staged credentials in place, so the next boot
// can retry.
func writeRegistriesConfig(clusterDoc *cluster.Cluster, creds *machine.RegistryCredentials,
	store machine.ManifestStore, source machine.ManifestSource) machine.RegistriesStatus {
	status := machine.RegistriesStatus{}

	// The function always builds both maps. Each map disappears from
	// the output when it is empty (omitempty). The gate below, for
	// the nothing-declared case, reads the size of each map.
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
			fmt.Fprintf(os.Stderr, "liken: registries: removing %s: %v\n", registriesConfigPath, err)
		}
		return status
	}

	raw, err := yaml.Marshal(&file)
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: registries: rendering %s: %v\n", registriesConfigPath, err)
		return status
	}
	// 0600, the same permission as the join token, because this file
	// embeds passwords.
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
