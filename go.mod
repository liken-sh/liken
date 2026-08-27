// One module for the whole repo. liken's Go programs, the init that
// boots the machine (init/), the operators that manage the machines
// and the fleet from inside the cluster (machine-operator/ and
// cluster-operator/), and the log relays that carry its streams
// into the cluster (logs/), version together (one VERSION file stamps
// every binary), release together (one initramfs), and share
// the API packages (api/ for the grammar every document speaks,
// machine/ and cluster/ for the documents themselves). Multiple
// modules are for code that versions and releases independently, and
// nothing here does, so a single module fits. It also means a shared
// package is just an import, with no publishing or replace directives
// required.
module github.com/liken-sh/liken

go 1.26.5

toolchain go1.27.0

require (
	github.com/beevik/ntp v1.5.0
	github.com/insomniacslk/dhcp v0.0.0-20260728151720-c308df0fdcef
	github.com/klauspost/compress v1.19.2
	github.com/liken-sh/brand v0.0.0-20260826005113-79f51d4d6ec6
	github.com/vishvananda/netlink v1.3.1
	golang.org/x/crypto v0.55.0
	golang.org/x/sys v0.47.0
	google.golang.org/grpc v1.83.2
	// The DRA plugin API that the node's kubelet calls. This side of a
	// gRPC contract must not lead the side that answers it, so the pin
	// follows the Kubernetes version k3s ships: k3s/VERSION names
	// v1.36.3+k3s1, so kubelet stays on v0.36.3.
	k8s.io/kubelet v0.36.3
	sigs.k8s.io/yaml v1.6.0
)

require (
	github.com/BurntSushi/toml v1.6.0 // indirect
	github.com/alexflint/go-arg v1.6.1 // indirect
	github.com/alexflint/go-scalar v1.2.0 // indirect
	github.com/aws/aws-sdk-go-v2 v1.44.0 // indirect
	github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream v1.7.20 // indirect
	github.com/aws/aws-sdk-go-v2/credentials v1.19.39 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.4.40 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.7.40 // indirect
	github.com/aws/aws-sdk-go-v2/internal/v4a v1.4.41 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.13.19 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/checksum v1.10.0 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.13.40 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/s3shared v1.19.41 // indirect
	github.com/aws/aws-sdk-go-v2/service/s3 v1.108.0 // indirect
	github.com/aws/smithy-go v1.28.1 // indirect
	github.com/golang/freetype v0.0.0-20170609003504-e2365dfdc4a0 // indirect
	github.com/google/go-github/v88 v88.0.0 // indirect
	github.com/google/go-querystring v1.2.0 // indirect
	github.com/josharian/native v1.1.0 // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/mattn/go-colorable v0.1.15 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/mdlayher/packet v1.1.2 // indirect
	github.com/mdlayher/socket v0.6.1 // indirect
	github.com/narqo/go-badge v0.0.0-20230821190521-c9a75c019a59 // indirect
	github.com/pierrec/lz4/v4 v4.1.29 // indirect
	github.com/rs/zerolog v1.35.1 // indirect
	github.com/u-root/uio v0.0.0-20240224005618-d2acac8f3701 // indirect
	github.com/vishvananda/netns v0.0.5 // indirect
	github.com/vladopajic/go-test-coverage/v2 v2.19.0 // indirect
	go.yaml.in/yaml/v2 v2.4.4 // indirect
	golang.org/x/exp/typeparams v0.0.0-20260824195058-e88cd73687aa // indirect
	golang.org/x/image v0.45.0 // indirect
	golang.org/x/mod v0.40.0 // indirect
	golang.org/x/net v0.58.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/text v0.41.0 // indirect
	golang.org/x/tools v0.49.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260825221802-da73d73af1c5 // indirect
	google.golang.org/protobuf v1.36.12 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	honnef.co/go/tools v0.8.1 // indirect
)

tool (
	github.com/vladopajic/go-test-coverage/v2
	honnef.co/go/tools/cmd/staticcheck
)
