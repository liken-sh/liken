// Package scaffold starts a deployment from answers. `liken new` asks
// a short series of plain questions, and writes the deployment
// directory: cluster.yaml and one machine manifest per machine. The
// rest of the toolkit (mint, layer, stick) builds on that directory.
//
// The generated documents are the documentation. Each one carries the
// teaching comments that a person needs to change it later, adapted
// from the dev cluster's manifests. A scaffold that writes an invalid
// document would fail its user at first boot. Because of this, this
// package parses everything it generates back through the same
// strict parsers that machines use, before it writes a single file.
// A failure there is a bug in this package, and the error message
// says so.
package scaffold

import (
	"bufio"
	"embed"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"text/template"

	"github.com/liken-sh/liken/cluster"
	"github.com/liken-sh/liken/machine"
)

//go:embed cluster.yaml.tmpl machine.yaml.tmpl
var templates embed.FS

// answers holds everything the questions collect. It is also the
// templates' input.
type answers struct {
	ClusterName string
	Machines    []machineAnswers
	Leaders     []string
	Endpoint    string
	NodeCIDR    string
	Upstreams   []string
	Features    []string
	Source      string

	// This is the fleet-wide hardware shape. The interview asks for
	// interface names and the disk layout once, and writes them into
	// every manifest, where a person can edit them per machine
	// afterward.
	UplinkNIC    string // empty means single-NIC machines
	ClusterNIC   string
	Gateway      string // single-NIC only: no DHCP to supply a route
	Nameservers  []string
	Disks        []string // 1 or 3 devices
	RebootPolicy string
}

type machineAnswers struct {
	Name    string
	Address string // CIDR form, inside NodeCIDR
}

// New runs the questions against in/out, and writes the deployment
// directory. It refuses a directory that already has a cluster.yaml,
// because scaffolding is for starting a deployment, not overwriting
// one.
func New(dir string, in io.Reader, out io.Writer) error {
	if _, err := os.Stat(filepath.Join(dir, "cluster.yaml")); err == nil {
		return fmt.Errorf("%s already has a cluster.yaml; the scaffold only starts new deployments", dir)
	}

	a, err := interview(bufio.NewScanner(in), out, filepath.Base(strings.TrimRight(dir, "/")))
	if err != nil {
		return err
	}

	clusterYAML, err := render("cluster.yaml.tmpl", a)
	if err != nil {
		return err
	}
	if _, err := cluster.ParseCluster(clusterYAML); err != nil {
		return fmt.Errorf("this is a scaffold bug: the generated cluster.yaml does not parse: %w", err)
	}

	machines := map[string][]byte{}
	for _, m := range a.Machines {
		doc, err := render("machine.yaml.tmpl", struct {
			answers
			Machine machineAnswers
		}{a, m})
		if err != nil {
			return err
		}
		parsed, err := machine.Parse(doc)
		if err != nil {
			return fmt.Errorf("this is a scaffold bug: %s's manifest does not parse: %w", m.Name, err)
		}
		if err := parsed.Spec.Storage.Validate(); err != nil {
			return fmt.Errorf("this is a scaffold bug: %s's storage does not validate: %w", m.Name, err)
		}
		machines[m.Name] = doc
	}

	// Nothing was written until everything validated.
	if err := os.MkdirAll(filepath.Join(dir, "machines"), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "cluster.yaml"), clusterYAML, 0o644); err != nil {
		return err
	}
	for name, doc := range machines {
		// The filename must equal the machine's name. The image
		// carries every manifest, and a boot selects its own
		// manifest as machines/<liken.machine>.yaml.
		if err := os.WriteFile(filepath.Join(dir, "machines", name+".yaml"), doc, 0o644); err != nil {
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(gitignore), 0o644); err != nil {
		return err
	}

	fmt.Fprintf(out, "\nwrote %s/cluster.yaml and %d machine manifest(s)\n", dir, len(machines))
	fmt.Fprintf(out, "next: liken mint %s\n", filepath.Join(dir, "identity"))
	return nil
}

const gitignore = `# The identity directory holds the cluster's private keys and join
# token: never commit it. The image directory holds built artifacts.
identity/
image/
`

func render(name string, data any) ([]byte, error) {
	t, err := template.ParseFS(templates, name)
	if err != nil {
		return nil, err
	}
	var buf strings.Builder
	if err := t.Execute(&buf, data); err != nil {
		return nil, err
	}
	return []byte(buf.String()), nil
}

// interview asks every question, validates each answer as it
// arrives, and asks again until the answer holds. The prompts always
// show the default that an empty answer takes.
func interview(in *bufio.Scanner, out io.Writer, defaultName string) (answers, error) {
	a := answers{}
	ask := func(prompt, deflt string) (string, error) {
		if deflt != "" {
			fmt.Fprintf(out, "%s [%s]: ", prompt, deflt)
		} else {
			fmt.Fprintf(out, "%s: ", prompt)
		}
		if !in.Scan() {
			return "", fmt.Errorf("no more answers (input ended at %q)", prompt)
		}
		answer := strings.TrimSpace(in.Text())
		if answer == "" {
			answer = deflt
		}
		return answer, nil
	}

	var err error
	if a.ClusterName, err = ask("cluster name", defaultName); err != nil {
		return a, err
	}

	// This asks for the machines, then for leaders among them. One
	// leader works for a lone machine or a small fleet. Three leaders
	// work once there are three machines to choose from, because
	// etcd's quorum needs an odd count.
	names, err := ask("machine names, space-separated", "machine-1")
	if err != nil {
		return a, err
	}
	machines := strings.Fields(names)
	if len(machines) == 0 || slices.ContainsFunc(machines, func(n string) bool {
		return strings.ContainsAny(n, "/. ") || n == ""
	}) {
		return a, fmt.Errorf("machine names become hostnames and filenames; %q won't do", names)
	}

	defaultLeaders := machines[:1]
	if len(machines) >= 3 {
		defaultLeaders = machines[:3]
	}
	for {
		leaders, err := ask("leaders (odd count; the first is the founding leader)", strings.Join(defaultLeaders, " "))
		if err != nil {
			return a, err
		}
		a.Leaders = strings.Fields(leaders)
		if len(a.Leaders)%2 == 0 {
			fmt.Fprintln(out, "etcd needs an odd number of leaders (1, 3, 5): a tie has no majority")
			continue
		}
		outsider := slices.IndexFunc(a.Leaders, func(l string) bool { return !slices.Contains(machines, l) })
		if outsider >= 0 {
			fmt.Fprintf(out, "%s is not one of the machines\n", a.Leaders[outsider])
			continue
		}
		break
	}

	// This asks for the cluster subnet, and each machine's fixed
	// address on it. The founding leader's address becomes the
	// endpoint that every follower uses to make first contact. This
	// is why these addresses are fixed, and not assigned through
	// DHCP.
	var prefix netip.Prefix
	for {
		cidr, err := ask("cluster subnet (the machines' own network)", "10.10.0.0/24")
		if err != nil {
			return a, err
		}
		if prefix, err = netip.ParsePrefix(cidr); err == nil {
			a.NodeCIDR = cidr
			break
		}
		fmt.Fprintln(out, "that isn't a CIDR like 10.10.0.0/24")
	}
	base := prefix.Addr()
	for i, name := range machines {
		// The defaults count up from the subnet's first host address:
		// .1, .2, and so on, in machine order.
		addr := base
		for range i + 1 {
			addr = addr.Next()
		}
		deflt := addr.String()
		for {
			answer, err := ask(fmt.Sprintf("%s's address", name), deflt)
			if err != nil {
				return a, err
			}
			ip, err := netip.ParseAddr(answer)
			if err != nil || !prefix.Contains(ip) {
				fmt.Fprintf(out, "%s must be an address inside %s\n", name, a.NodeCIDR)
				continue
			}
			a.Machines = append(a.Machines, machineAnswers{
				Name:    name,
				Address: fmt.Sprintf("%s/%d", ip, prefix.Bits()),
			})
			if name == a.Leaders[0] {
				a.Endpoint = fmt.Sprintf("https://%s:6443", ip)
			}
			break
		}
	}

	// This asks for the interfaces once for the whole fleet. Two NICs
	// is the designed shape: an uplink on DHCP, and a fixed address
	// on the cluster segment. A single-NIC machine puts its fixed
	// address on its only interface, and needs a gateway and
	// nameservers spelled out, because there is no DHCP lease to
	// supply them.
	uplink, err := ask(`uplink interface for internet access ("none" if machines have only one interface)`, "eth0")
	if err != nil {
		return a, err
	}
	a.UplinkNIC = strings.TrimSpace(uplink)
	if strings.EqualFold(a.UplinkNIC, "none") {
		a.UplinkNIC = ""
	}
	clusterDefault := "eth1"
	if a.UplinkNIC == "" {
		clusterDefault = "eth0"
	}
	if a.ClusterNIC, err = ask("cluster interface (carries the fixed address)", clusterDefault); err != nil {
		return a, err
	}
	if a.UplinkNIC == "" {
		for {
			gw, err := ask("gateway (single-NIC machines have no DHCP to learn a route from)", "")
			if err != nil {
				return a, err
			}
			if _, err := netip.ParseAddr(gw); err == nil {
				a.Gateway = gw
				break
			}
			fmt.Fprintln(out, "that isn't an IP address")
		}
		ns, err := ask("nameservers, space-separated", "1.1.1.1 9.9.9.9")
		if err != nil {
			return a, err
		}
		a.Nameservers = strings.Fields(ns)
	}

	// This asks for the disks. One device fits a real machine with
	// one drive, with all seven roles carved from it. Three devices
	// match the dev cluster's shape: state, pods, boot. The roles and
	// sizes follow the same reasoning that the dev cluster's comments
	// teach.
	for {
		disks, err := ask("disk devices, space-separated (1 disk, or 3 for state/pods/boot)", "/dev/sda")
		if err != nil {
			return a, err
		}
		a.Disks = strings.Fields(disks)
		if len(a.Disks) == 1 || len(a.Disks) == 3 {
			break
		}
		fmt.Fprintln(out, "one disk or three: one carries every role, three split state/pods/boot")
	}

	ntp, err := ask("NTP servers for the leaders (liken deliberately has no default)", "time.cloudflare.com time.nist.gov")
	if err != nil {
		return a, err
	}
	a.Upstreams = strings.Fields(ntp)

	for {
		policy, err := ask("reboot policy: Manual (a person grants each reboot) or Auto", "Manual")
		if err != nil {
			return a, err
		}
		if policy == "Manual" || policy == "Auto" {
			a.RebootPolicy = policy
			break
		}
		fmt.Fprintln(out, "Manual or Auto, capitalized the way the API spells them")
	}

	features, err := ask("features to enable, space-separated (traefik servicelb metrics-server iscsi nfs), or none", "")
	if err != nil {
		return a, err
	}
	a.Features = strings.Fields(features)
	for _, f := range a.Features {
		if !slices.Contains([]string{"traefik", "servicelb", "metrics-server", "iscsi", "nfs"}, f) {
			return a, fmt.Errorf("%q is not in liken's feature vocabulary", f)
		}
	}

	if a.Source, err = ask("release channel URL for over-the-network updates (blank to decide later)", ""); err != nil {
		return a, err
	}
	return a, nil
}
