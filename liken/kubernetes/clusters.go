package kubernetes

// This file reads and reports on Clusters. The machine operator reads
// the one Cluster that its manifest names. The cluster operator lists
// all Clusters, because the list names the Cluster it operates. The
// cluster operator needs no configuration at all: a fleet has
// exactly one Cluster to find.

import (
	"encoding/json"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/liken/cluster"
)

func GetCluster(c *apiclient.Client, name string) (*cluster.Cluster, error) {
	return apiclient.Get[cluster.Cluster](c, ClustersPath+"/"+name)
}

// PublishClusterStatus writes through the Cluster's status
// subresource. This is a separate endpoint (…/clusters/<name>/status)
// that updates only the status half of the object. Because of this,
// the single writer of the Cluster's status can never accidentally
// rewrite the spec it acts on, and RBAC can grant access to the two
// halves separately. The write is a PUT request that carries the
// object's resourceVersion. If anything else changed the object in
// the meantime, the server answers with 409 Conflict instead of
// applying the stale copy. The caller then reads the object again on
// its next pass and tries again. This pattern is optimistic
// concurrency, the same contract that PublishStatus uses for
// Machines, and it answers the written resourceVersion the same way.
func PublishClusterStatus(c *apiclient.Client, clusterDoc *cluster.Cluster) (string, error) {
	body, err := json.Marshal(clusterDoc)
	if err != nil {
		return "", err
	}
	return putStatus(c, ClustersPath+"/"+clusterDoc.Metadata.Name+"/status", body)
}
