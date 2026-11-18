package main

// The reasons of the Events that this program posts for actions and
// faults that change no condition. For a condition transition,
// endpointevents.go posts an Event with the condition's own reason,
// such as NoNode. A person filters kubectl get events on a reason,
// and the troubleshooting guide names each one, so a reason is part of
// the published interface.
//
// A Sink and a Source are cluster-scoped, so kubernetes/events posts
// their Events in the namespace default, where kubectl describe sink
// finds them.

const (
	// reasonCaptured is posted by the API when a tap sends its first
	// bytes. The message names the caller and the aspect.
	reasonCaptured = "Captured"

	// reasonLayoutChanged is posted on a Sink when the operator writes a
	// new channel layout and PipeWire restarts in its container to
	// apply it.
	reasonLayoutChanged = "LayoutChanged"

	// reasonLayoutWriteFailed is posted on a Sink when the write of its
	// new channel layout fails with an error other than the last one.
	reasonLayoutWriteFailed = "LayoutWriteFailed"

	// reasonSpecRefused is posted on a Sink or a Source when its spec
	// sets a value that the endpoint cannot take, such as a codec that
	// the speaker does not offer, and the refused values differ from
	// those of the last pass.
	reasonSpecRefused = "SpecRefused"

	// reasonPipeWireLost is posted on each Sink of the machine at the
	// first failed graph read of a run, and reasonPipeWireRecovered at
	// the first read that succeeds after a failure.
	reasonPipeWireLost      = "PipeWireLost"
	reasonPipeWireRecovered = "PipeWireRecovered"

	// reasonBluetoothUnavailable is posted on each speaker's Sink when a
	// read of the paired speakers from bluetoothd first fails, and
	// reasonBluetoothAvailable when a read succeeds again.
	reasonBluetoothUnavailable = "BluetoothUnavailable"
	reasonBluetoothAvailable   = "BluetoothAvailable"
)
