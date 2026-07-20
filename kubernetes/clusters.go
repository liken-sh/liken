package kubernetes

// This file reads and reports on Clusters. The machine operator reads
// the one Cluster that its manifest names. The cluster operator lists
// all Clusters, because the list is how it learns which Cluster it
// operates. The cluster operator needs no configuration at all: a
// fleet has exactly one Cluster to find.

import (
	"encoding/json"
	"net/http"

	"github.com/liken-sh/liken/cluster"
)

func GetCluster(c *Client, name string) (*cluster.Cluster, error) {
	return get[cluster.Cluster](c, ClustersPath+"/"+name)
}

func ListClusters(c *Client) ([]cluster.Cluster, error) {
	return List[cluster.Cluster](c, ClustersPath)
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
// Machines.
func PublishClusterStatus(c *Client, clusterDoc *cluster.Cluster) error {
	body, err := json.Marshal(clusterDoc)
	if err != nil {
		return err
	}
	path := ClustersPath + "/" + clusterDoc.Metadata.Name + "/status"
	return c.RequestJSON(http.MethodPut, path, body, nil)
}
