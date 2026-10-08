package main

// The reasons of the Events this program posts, in one place, because
// a reason is part of the published interface: a person filters
// `kubectl get events` on it, and the troubleshooting guide names
// each one.
//
// A Sink and a Source are cluster-scoped, so their Events go in the
// namespace default (kubernetes/events). `kubectl describe sink`
// finds them there.
//
// Each condition transition posts one Event more, with the
// condition's own reason, such as NoMonitor or NoNode
// (endpointevents.go). The reasons below are the actions and the
// faults that change no condition.

const (
	// reasonCaptured is posted by the API container on each tap that
	// produced bytes, with the caller and the aspect.
	reasonCaptured = "Captured"

	// reasonLayoutChanged is posted on a Sink when the operator writes
	// a new channel layout and PipeWire restarts in its container to
	// apply it.
	reasonLayoutChanged = "LayoutChanged"

	// reasonLayoutWriteFailed is posted on a Sink when the operator
	// cannot write the declaration that holds its new layout.
	reasonLayoutWriteFailed = "LayoutWriteFailed"

	// reasonSpecRefused is posted on a Sink or a Source when its spec
	// states a value the endpoint cannot take, such as a codec the
	// speaker does not offer.
	reasonSpecRefused = "SpecRefused"

	// reasonPipeWireLost is posted on each Sink of the machine when a
	// graph read fails, and reasonPipeWireRecovered when a read
	// succeeds after a failure.
	reasonPipeWireLost      = "PipeWireLost"
	reasonPipeWireRecovered = "PipeWireRecovered"

	// reasonBluetoothUnavailable is posted on each speaker's Sink when
	// bluetoothd stops answering, and reasonBluetoothAvailable when it
	// answers again.
	reasonBluetoothUnavailable = "BluetoothUnavailable"
	reasonBluetoothAvailable   = "BluetoothAvailable"
)
