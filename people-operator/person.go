package main

// The Person kind, as the operator reads and writes it. The struct
// holds only the fields the operator reads, and every status field,
// because a write to the status subresource replaces the whole status:
// a status field missing here would be erased by each write.

import (
	"github.com/liken-sh/liken/kubernetes/conditions"
	"github.com/liken-sh/liken/kubernetes/informer"
)

const (
	personAPIVersion = "people.liken.sh/v1alpha1"
	personKind       = "Person"
	peoplePath       = "/apis/people.liken.sh/v1alpha1/people"
)

// checkAvatarAnnotation asks the operator to read a Person's picture
// again. Its value has no meaning. A value that differs from
// status.avatar.checked is the request.
const checkAvatarAnnotation = "people.liken.sh/check-avatar"

type personMeta struct {
	Name            string            `json:"name"`
	UID             string            `json:"uid,omitempty"`
	ResourceVersion string            `json:"resourceVersion,omitempty"`
	Generation      int64             `json:"generation,omitempty"`
	Annotations     map[string]string `json:"annotations,omitempty"`
}

func (m *personMeta) GetName() string            { return m.Name }
func (m *personMeta) GetNamespace() string       { return "" }
func (m *personMeta) GetResourceVersion() string { return m.ResourceVersion }

type person struct {
	APIVersion string       `json:"apiVersion"`
	Kind       string       `json:"kind"`
	Metadata   personMeta   `json:"metadata"`
	Spec       personSpec   `json:"spec"`
	Status     personStatus `json:"status,omitempty"`
}

func (p *person) GetObjectMeta() informer.Meta { return &p.Metadata }

type personSpec struct {
	DisplayName string `json:"displayName"`
	Nickname    string `json:"nickname,omitempty"`
	Avatar      string `json:"avatar,omitempty"`
}

type personStatus struct {
	// UID is declared in the CRD for a controller that assigns uids.
	// This operator does not write it, and carries it so a status write
	// keeps it.
	UID        *int64      `json:"uid,omitempty"`
	Thumbnail  string      `json:"thumbnail,omitempty"`
	Avatar     avatarState `json:"avatar,omitzero"`
	Conditions []condition `json:"conditions,omitempty"`
}

// avatarState records what the thumbnail answers: the source it read,
// the version of the picture it read there, and the check request it
// answered.
type avatarState struct {
	Source       string `json:"source,omitempty"`
	ETag         string `json:"etag,omitempty"`
	LastModified string `json:"lastModified,omitempty"`
	Size         int64  `json:"size,omitempty"`
	Checked      string `json:"checked,omitempty"`
}

// version is the part of the record that identifies one copy of the
// picture at its source.
func (a avatarState) version() pictureVersion {
	return pictureVersion{ETag: a.ETag, LastModified: a.LastModified, Size: a.Size}
}

// condition is the condition type that every liken component reports,
// with the shape of metav1.Condition. A condition with no message
// writes an empty message, which the CRD's schema takes.
type condition = conditions.Condition

const avatarReady = "AvatarReady"

// ready answers the AvatarReady condition, or the zero condition when
// the status holds none.
func (s personStatus) ready() condition {
	c, _ := conditions.Find(s.Conditions, avatarReady)
	return c
}

// checkRequest answers the value of the check-avatar annotation, or the
// empty string when the Person has none.
func (p *person) checkRequest() string {
	return p.Metadata.Annotations[checkAvatarAnnotation]
}

func personPath(name string) string { return peoplePath + "/" + name }
