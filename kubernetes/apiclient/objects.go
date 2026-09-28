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

// ReplaceStatus writes one object's status to its status subresource,
// so a spec that a person edited between the read and the write stays
// as the person wrote it. The write sends the whole object, because
// that is what the subresource takes, and the API server keeps only the
// status half of it. The caller states the object's apiVersion and
// kind: an object read out of a list does not always hold them, and the
// API server refuses a write that holds neither.
//
// The API server's copy replaces the caller's, because the write makes
// a new resourceVersion and the next write must state it. The answer is
// decoded into a new value and not over the caller's object: a decode
// over the old object keeps each field that the answer leaves out, such
// as a status field the CRD schema drops, and the caller then holds a
// copy the API server does not. After an error the caller's object is
// as it was, with the status the API server did not take.
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
