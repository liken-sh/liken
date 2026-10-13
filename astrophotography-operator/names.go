package main

// The names that this operator shares with the cluster: its name as a
// Reservation's holder, and the labels on every object it creates.

const (
	// managedBy is the operator's name: the holder that a Reservation
	// names to give the operator its telescope, the component of its
	// Events, the name of its Lease, and the value of labelManagedBy.
	managedBy = "astrophotography-operator"
	// labelManagedBy is on every object the operator creates. The
	// watches of pods, Services, Deployments, and Jobs select by it, so
	// the operator reads its own objects and no others.
	labelManagedBy = "app.kubernetes.io/managed-by"
)
