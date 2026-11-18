package main

const (
	// managedBy is the operator's name. A Reservation whose spec.holder
	// is this name gives its telescope to the operator. It is also the
	// component of the operator's Events, the name of its Lease, and the
	// value of labelManagedBy.
	managedBy = "astrophotography-operator"
	// labelManagedBy is on every object the operator creates. The
	// watches of pods, Services, Deployments, and Jobs select on it, so
	// they return only the operator's own objects.
	labelManagedBy = "app.kubernetes.io/managed-by"
)
