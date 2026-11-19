# The drain holds leave out the error

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

When machine-operator cannot list the pods of its `Node` or cannot
cordon it before a reboot, the condition says that the reboot waits,
but not why. The error is only in the operator's log. The rule in
`brand/messages.md` says that a message that reports an error states
the condition first and the cause after it.

## The evidence

`gateThroughDrain` in `machine-operator/drain.go` prints the error of
`nodePods` and of the cordon patch with `fmt.Printf`, and then calls
`holdForDrain` with a fixed message: "The reboot waits because the
pods of the Node cannot be listed. The operator retries" and "The
reboot waits because the Node is not cordoned. The operator retries".
`holdForDrain` takes no error, so it cannot add the cause.

## What would confirm it

A test of `gateThroughDrain` with an API server that refuses the pod
list, which reads the message of the resulting condition.

## What a fix has to decide

Whether `holdForDrain` takes the error and puts it after the fixed
text, and whether the log line stays when the condition and its
`Event` carry the cause.
