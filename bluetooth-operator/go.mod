// One module, two programs, two images: the operator, and the
// entrypoint of the image that carries bluetoothd. They release
// together on one version, so there is nothing here that versions on
// its own.
//
// The Kubernetes libraries are pinned to the versions liken builds
// against, because the two drivers serve the same kubelet on the same
// node and speak the same DRA protocol version to it. The Go version
// matches liken's for the same reason.
module github.com/liken-sh/bluetooth-operator

go 1.26.5

toolchain go1.27.0

require (
	github.com/godbus/dbus/v5 v5.2.2
	golang.org/x/sys v0.47.0
	google.golang.org/grpc v1.83.2
	// The DRA plugin API that the node's kubelet calls. This side of a
	// gRPC contract must not lead the side that answers it, so the pin
	// follows the Kubernetes version k3s ships on a liken machine:
	// liken's k3s/VERSION names v1.36.3+k3s1, so kubelet stays on
	// v0.36.3.
	k8s.io/kubelet v0.36.3
)

require (
	github.com/alexflint/go-arg v1.6.0 // indirect
	github.com/alexflint/go-scalar v1.2.0 // indirect
	github.com/aws/aws-sdk-go-v2 v1.43.0 // indirect
	github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream v1.7.14 // indirect
	github.com/aws/aws-sdk-go-v2/credentials v1.19.30 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.4.31 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.7.31 // indirect
	github.com/aws/aws-sdk-go-v2/internal/v4a v1.4.32 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.13.13 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/checksum v1.9.24 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.13.31 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/s3shared v1.19.32 // indirect
	github.com/aws/aws-sdk-go-v2/service/s3 v1.106.0 // indirect
	github.com/aws/smithy-go v1.27.3 // indirect
	github.com/golang/freetype v0.0.0-20170609003504-e2365dfdc4a0 // indirect
	github.com/google/go-github/v88 v88.0.0 // indirect
	github.com/google/go-querystring v1.2.0 // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/narqo/go-badge v0.0.0-20230821190521-c9a75c019a59 // indirect
	github.com/rs/zerolog v1.35.1 // indirect
	github.com/vladopajic/go-test-coverage/v2 v2.19.0 // indirect
	golang.org/x/image v0.41.0 // indirect
	golang.org/x/net v0.58.0 // indirect
	golang.org/x/text v0.41.0 // indirect
	golang.org/x/tools v0.48.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260825221802-da73d73af1c5 // indirect
	google.golang.org/protobuf v1.36.12 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

tool github.com/vladopajic/go-test-coverage/v2
