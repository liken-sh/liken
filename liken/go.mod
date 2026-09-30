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
module github.com/liken-sh/liken/liken

go 1.27.1

require (
	github.com/beevik/ntp v1.5.0
	github.com/google/go-containerregistry v0.22.1
	github.com/insomniacslk/dhcp v0.0.0-20260728151720-c308df0fdcef
	github.com/klauspost/compress v1.19.2
	github.com/liken-sh/brand v0.0.0
	// The client, the watch, and the cache that liken's operators share
	// with the other operators. The module is in this repository, and the
	// replace below builds against the copy in the tree, so no version of
	// it is published.
	github.com/liken-sh/liken/kubernetes v0.0.0
	github.com/prometheus/client_golang v1.24.1
	github.com/vishvananda/netlink v1.3.1
	golang.org/x/crypto v0.55.0
	golang.org/x/sys v0.47.0
	google.golang.org/grpc v1.83.2
	k8s.io/api v0.36.3
	// The operators watch the API server through client-go's reflector
	// (kubernetes/informer), and the cluster operator elects its leader
	// with client-go's leaderelection. Both follow the same minor as the
	// API server they talk to, the Kubernetes version k3s/VERSION names.
	k8s.io/apimachinery v0.36.3
	k8s.io/client-go v0.36.3
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
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/docker/cli v29.7.2+incompatible // indirect
	github.com/docker/docker-credential-helpers v0.9.3 // indirect
	github.com/emicklei/go-restful/v3 v3.13.0 // indirect
	github.com/fxamacker/cbor/v2 v2.9.0 // indirect
	github.com/go-logr/logr v1.4.3 // indirect
	github.com/go-openapi/jsonpointer v0.21.0 // indirect
	github.com/go-openapi/jsonreference v0.20.2 // indirect
	github.com/go-openapi/swag v0.23.0 // indirect
	github.com/golang/freetype v0.0.0-20170609003504-e2365dfdc4a0 // indirect
	github.com/google/gnostic-models v0.7.0 // indirect
	github.com/google/go-github/v88 v88.0.0 // indirect
	github.com/google/go-querystring v1.2.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/josharian/intern v1.0.0 // indirect
	github.com/josharian/native v1.1.0 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/mailru/easyjson v0.7.7 // indirect
	github.com/mattn/go-colorable v0.1.15 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/mdlayher/packet v1.1.2 // indirect
	github.com/mdlayher/socket v0.6.1 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.3-0.20250322232337-35a7c28c31ee // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/narqo/go-badge v0.0.0-20230821190521-c9a75c019a59 // indirect
	github.com/opencontainers/go-digest v1.0.0 // indirect
	github.com/opencontainers/image-spec v1.1.1 // indirect
	github.com/pierrec/lz4/v4 v4.1.29 // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	github.com/prometheus/client_model v0.6.2 // indirect
	github.com/prometheus/common v0.70.1 // indirect
	github.com/prometheus/procfs v0.21.1 // indirect
	github.com/rs/zerolog v1.35.1 // indirect
	github.com/sirupsen/logrus v1.9.4 // indirect
	github.com/u-root/uio v0.0.0-20240224005618-d2acac8f3701 // indirect
	github.com/vishvananda/netns v0.0.5 // indirect
	github.com/vladopajic/go-test-coverage/v2 v2.19.0 // indirect
	github.com/x448/float16 v0.8.4 // indirect
	go.yaml.in/yaml/v2 v2.4.4 // indirect
	go.yaml.in/yaml/v3 v3.0.4 // indirect
	golang.org/x/exp/typeparams v0.0.0-20260824195058-e88cd73687aa // indirect
	golang.org/x/image v0.45.0 // indirect
	golang.org/x/mod v0.40.0 // indirect
	golang.org/x/net v0.58.0 // indirect
	golang.org/x/oauth2 v0.36.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/term v0.45.0 // indirect
	golang.org/x/text v0.41.0 // indirect
	golang.org/x/time v0.14.0 // indirect
	golang.org/x/tools v0.49.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260825221802-da73d73af1c5 // indirect
	google.golang.org/protobuf v1.36.12 // indirect
	gopkg.in/evanphx/json-patch.v4 v4.13.0 // indirect
	gopkg.in/inf.v0 v0.9.1 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	gotest.tools/v3 v3.5.2 // indirect
	honnef.co/go/tools v0.8.1 // indirect
	k8s.io/klog/v2 v2.140.0 // indirect
	k8s.io/kube-openapi v0.0.0-20260317180543-43fb72c5454a // indirect
	k8s.io/utils v0.0.0-20260210185600-b8788abfbbc2 // indirect
	sigs.k8s.io/json v0.0.0-20250730193827-2d320260d730 // indirect
	sigs.k8s.io/randfill v1.0.0 // indirect
	sigs.k8s.io/structured-merge-diff/v6 v6.3.3 // indirect
)

tool (
	github.com/vladopajic/go-test-coverage/v2
	honnef.co/go/tools/cmd/staticcheck
)

// brand is a component in this repository, so every consumer builds
// against the tree beside it and never against a published version.
replace github.com/liken-sh/brand => ../brand

replace github.com/liken-sh/liken/kubernetes => ../kubernetes
