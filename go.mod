// One module for the whole repo. liken's Go programs, the init that
// boots the machine (init/), the operators that manage the machines
// and the fleet from inside the cluster (machine-operator/ and
// cluster-operator/), and the log relays that carry its streams
// into the cluster (logs/), version together (one VERSION file stamps
// every binary), release together (one initramfs), and share
// the machine package (machine/, the Machine API as Go types). Multiple
// modules are for code that versions and releases independently, and
// nothing here does, so a single module fits. It also means a shared
// package is just an import, with no publishing or replace directives
// required.
module github.com/chrisguidry/liken

go 1.26.4

require (
	github.com/beevik/ntp v1.5.0
	github.com/insomniacslk/dhcp v0.0.0-20260603135910-a415979eb11e
	github.com/vishvananda/netlink v1.3.1
	golang.org/x/sys v0.46.0
	sigs.k8s.io/yaml v1.6.0
)

require (
	github.com/alexflint/go-arg v1.6.0 // indirect
	github.com/alexflint/go-scalar v1.2.0 // indirect
	github.com/aws/aws-sdk-go v1.49.4 // indirect
	github.com/golang/freetype v0.0.0-20170609003504-e2365dfdc4a0 // indirect
	github.com/google/go-github/v82 v82.0.0 // indirect
	github.com/google/go-querystring v1.2.0 // indirect
	github.com/jmespath/go-jmespath v0.4.0 // indirect
	github.com/josharian/native v1.1.0 // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/mdlayher/packet v1.1.2 // indirect
	github.com/mdlayher/socket v0.4.1 // indirect
	github.com/narqo/go-badge v0.0.0-20230821190521-c9a75c019a59 // indirect
	github.com/pierrec/lz4/v4 v4.1.14 // indirect
	github.com/rs/zerolog v1.35.1 // indirect
	github.com/u-root/uio v0.0.0-20230220225925-ffce2a382923 // indirect
	github.com/vishvananda/netns v0.0.5 // indirect
	github.com/vladopajic/go-test-coverage/v2 v2.18.8 // indirect
	go.yaml.in/yaml/v2 v2.4.2 // indirect
	golang.org/x/image v0.38.0 // indirect
	golang.org/x/net v0.44.0 // indirect
	golang.org/x/sync v0.8.0 // indirect
	golang.org/x/tools v0.26.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

tool github.com/vladopajic/go-test-coverage/v2
