module github.com/chrisguidry/liken/init

go 1.26.4

// The machine package lives in this repo, not on a module proxy; the
// replace directive is what lets this module build standalone.
replace github.com/chrisguidry/liken/machine => ../machine

require (
	github.com/chrisguidry/liken/machine v0.0.0-00010101000000-000000000000
	github.com/insomniacslk/dhcp v0.0.0-20260603135910-a415979eb11e
	github.com/vishvananda/netlink v1.3.1
	golang.org/x/sys v0.46.0
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
	sigs.k8s.io/yaml v1.6.0 // indirect
)
