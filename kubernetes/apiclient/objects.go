package apiclient

import (
	"encoding/json"
	"net/http"
)

// Get reads one object.
func Get[T any](c *Client, path string) (*T, error) {
	out := new(T)
	if err := c.RequestJSON(http.MethodGet, path, nil, out); err != nil {
		return nil, err
	}
	return out, nil
}

// ReplaceStatus writes an object's status through its status
// subresource, so a spec that a person edited between the read and the
// write is not overwritten. The request body is the whole object,
// because the subresource takes a whole object, but the API server uses
// only its status. The caller must set the object's apiVersion and
// kind, because an object read from a list can lack them and the API
// server rejects a write that has neither.
//
// On success, ReplaceStatus replaces *object with the API server's
// response, which has the resourceVersion that the next write needs. It
// decodes the response into a new value, so a field that the response
// omits, such as a status field that the CRD schema prunes, does not
// stay in the caller's copy. On an error, *object is unchanged.
func ReplaceStatus[T any](c *Client, path string, object *T) error {
	body, err := json.Marshal(object)
	if err != nil {
		return err
	}
	stored := new(T)
	if err := c.RequestJSON(http.MethodPut, path+"/status", body, stored); err != nil {
		return err
	}
	*object = *stored
	return nil
}
