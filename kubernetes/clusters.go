package kubernetes

// Reading Clusters. The machine operator reads the one its manifest
// names; the cluster operator lists, because the list *is* how it
// learns which Cluster it operates: it takes no configuration at
// all, and a fleet has exactly one Cluster to find.

import (
	"net/http"

	"github.com/chrisguidry/liken/machine"
)

func GetCluster(c *Client, name string) (*machine.Cluster, error) {
	cluster := &machine.Cluster{}
	if err := c.RequestJSON(http.MethodGet, ClustersPath+"/"+name, nil, cluster); err != nil {
		return nil, err
	}
	return cluster, nil
}

func ListClusters(c *Client) ([]machine.Cluster, error) {
	var list struct {
		Items []machine.Cluster `json:"items"`
	}
	if err := c.RequestJSON(http.MethodGet, ClustersPath, nil, &list); err != nil {
		return nil, err
	}
	return list.Items, nil
}
