package kubernetes

// This file reads and reports on Machines: the operations both
// operators share. The machine operator reads and writes its own
// Machine. The cluster operator reads every Machine. The watches are
// in the informer package.

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/liken-sh/liken/machine"
)

func GetMachine(c *Client, name string) (*machine.Machine, error) {
	return get[machine.Machine](c, MachinesPath+"/"+name)
}

func ListMachines(c *Client) ([]machine.Machine, error) {
	return List[machine.Machine](c, MachinesPath)
}

// PublishStatus writes through the status subresource. This is a
// separate endpoint (…/machines/<name>/status) that updates only the
// status half of the object. Because of this, a controller can never
// accidentally rewrite the spec it acts on, and RBAC can grant access
// to the two halves separately. The write is a PUT request that
// carries the object's resourceVersion. If anything else changed the
// object in the meantime, the server answers with 409 Conflict
// instead of applying the stale copy. The caller then reads the
// object again on its next pass and tries again. This pattern is
// optimistic concurrency, and every Kubernetes controller uses it to
// handle contention.
//
// It answers the resourceVersion the API server gave the write. A
// caller that reads a watch's copy records it, so the copy does not
// answer until the watch delivers the write (informer.Wrote).
func PublishStatus(c *Client, m *machine.Machine, status *machine.MachineStatus) (string, error) {
	updated := *m
	updated.Status = *status
	body, err := json.Marshal(&updated)
	if err != nil {
		return "", err
	}
	return putStatus(c, MachinesPath+"/"+m.Metadata.Name+"/status", body)
}

// putStatus writes a status subresource and answers the
// resourceVersion of the object the API server stored. An answer with
// no body carries no version, and the write still succeeded, so the
// version is empty.
func putStatus(c *Client, path string, body []byte) (string, error) {
	var written struct {
		Metadata struct {
			ResourceVersion string `json:"resourceVersion"`
		} `json:"metadata"`
	}
	err := c.RequestJSON(http.MethodPut, path, body, &written)
	if errors.Is(err, io.EOF) {
		return "", nil
	}
	return written.Metadata.ResourceVersion, err
}
