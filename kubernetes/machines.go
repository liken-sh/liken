package kubernetes

// Reading and reporting on Machines: the verbs both operators share.
// The machine operator reads and writes its own Machine; the cluster
// operator reads them all. Watching them lives in watch.go.

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

// PublishStatus writes through the status subresource: a separate
// endpoint (…/machines/<name>/status) that updates *only* the status
// half of the object. That means a controller can never accidentally
// rewrite the spec it is acting on, and RBAC can grant the two halves
// separately. The write is a PUT carrying the object's resourceVersion:
// if anything else changed the object in between, the server answers
// 409 Conflict instead of applying our stale copy. The caller's next
// pass re-reads and tries again. This is optimistic concurrency, and
// it is how every Kubernetes controller handles contention.
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
