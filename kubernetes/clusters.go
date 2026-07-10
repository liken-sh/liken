package kubernetes

// Reading and reporting on Clusters. The machine operator reads the
// one its manifest names; the cluster operator lists, because the
// list *is* how it learns which Cluster it operates: it takes no
// configuration at all, and a fleet has exactly one Cluster to find.

import (
	"encoding/json"
	"net/http"

	"github.com/chrisguidry/liken/machine"
)

func GetCluster(c *Client, name string) (*machine.Cluster, error) {
	return get[machine.Cluster](c, ClustersPath+"/"+name)
}

func ListClusters(c *Client) ([]machine.Cluster, error) {
	return List[machine.Cluster](c, ClustersPath)
}

// PublishClusterStatus writes through the Cluster's status
// subresource: a separate endpoint (…/clusters/<name>/status) that
// updates *only* the status half of the object, so the one writer of
// the Cluster's status can never accidentally rewrite the spec it is
// acting on, and RBAC can grant the two halves separately. The write
// is a PUT carrying the object's resourceVersion: if anything else
// changed the object in between, the server answers 409 Conflict
// instead of applying our stale copy, and the caller's next pass
// re-reads and tries again. This is optimistic concurrency, the same
// contract PublishStatus describes for Machines.
func PublishClusterStatus(c *Client, cluster *machine.Cluster) error {
	body, err := json.Marshal(cluster)
	if err != nil {
		return err
	}
	path := ClustersPath + "/" + cluster.Metadata.Name + "/status"
	return c.RequestJSON(http.MethodPut, path, body, nil)
}
