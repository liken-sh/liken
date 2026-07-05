// One module for the whole repo. liken's Go programs — the init that
// boots the machine (init/) and the operator that manages it from
// inside the cluster (operator/) — version together (one VERSION file
// stamps both binaries), release together (one initramfs), and share
// the machine package (machine/, the Machine API as Go types). Multiple
// modules are for code that versions and releases independently;
// nothing here does, so a single module is the honest arrangement — and
// it means a shared package is just an import, no publishing or replace
// directives required.
module github.com/chrisguidry/liken

go 1.26.4

require (
	github.com/insomniacslk/dhcp v0.0.0-20260603135910-a415979eb11e
	github.com/vishvananda/netlink v1.3.1
	golang.org/x/sys v0.46.0
	sigs.k8s.io/yaml v1.6.0
)

require (
	github.com/josharian/native v1.1.0 // indirect
	github.com/mdlayher/packet v1.1.2 // indirect
	github.com/mdlayher/socket v0.4.1 // indirect
	github.com/pierrec/lz4/v4 v4.1.14 // indirect
	github.com/u-root/uio v0.0.0-20230220225925-ffce2a382923 // indirect
	github.com/vishvananda/netns v0.0.5 // indirect
	go.yaml.in/yaml/v2 v2.4.2 // indirect
	golang.org/x/net v0.38.0 // indirect
	golang.org/x/sync v0.3.0 // indirect
)
