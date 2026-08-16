// One module, one package, one program. The operator and the daemon
// it runs ship in one image, so there is nothing here that versions
// or releases on its own.
//
// The Kubernetes libraries are pinned to the versions liken builds
// against, because the two drivers serve the same kubelet on the same
// node and speak the same DRA protocol version to it. The Go version
// matches liken's for the same reason.
module github.com/liken-sh/bluetooth-operator

go 1.26.5

require (
	github.com/godbus/dbus/v5 v5.2.2
	golang.org/x/sys v0.47.0
	google.golang.org/grpc v1.82.1
	k8s.io/kubelet v0.36.2
)

require (
	golang.org/x/net v0.53.0 // indirect
	golang.org/x/text v0.36.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260414002931-afd174a4e478 // indirect
	google.golang.org/protobuf v1.36.12-0.20260120151049-f2248ac996af // indirect
)
