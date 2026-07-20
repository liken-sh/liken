package kubernetes

// This file reads and reports on Machines: the operations both
// operators share. The machine operator reads and writes its own
// Machine. The cluster operator reads every Machine. Watching
// Machines is implemented in watch.go.

import (
	"encoding/json"
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
func PublishStatus(c *Client, m *machine.Machine, status *machine.MachineStatus) error {
	updated := *m
	updated.Status = *status
	body, err := json.Marshal(&updated)
	if err != nil {
		return err
	}
	path := MachinesPath + "/" + m.Metadata.Name + "/status"
	return c.RequestJSON(http.MethodPut, path, body, nil)
}
