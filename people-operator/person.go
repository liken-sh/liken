package main

// The Person kind, as the operator reads and writes it. The struct has
// the spec fields the operator reads and every status field. A write to
// the status subresource replaces the whole status, so a status field
// missing here would be erased by each write.

import (
	"github.com/liken-sh/liken/kubernetes/conditions"
	"github.com/liken-sh/liken/kubernetes/informer"
)

const (
	personAPIVersion = "people.liken.sh/v1alpha1"
	personKind       = "Person"
	peoplePath       = "/apis/people.liken.sh/v1alpha1/people"
)

// A new value of checkAvatarAnnotation makes the operator read the
// Person's picture again. Any value works: the operator reads the
// picture when the value differs from status.avatar.checked.
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
	// The CRD declares status.uid for a controller that assigns uids.
	// This operator never sets it, but keeps the value it read so that
	// its status writes do not erase it.
	UID        *int64      `json:"uid,omitempty"`
	Thumbnail  string      `json:"thumbnail,omitempty"`
	Avatar     avatarState `json:"avatar,omitzero"`
	Conditions []condition `json:"conditions,omitempty"`
}

// avatarState records where the thumbnail came from: the source URL,
// the version of the picture read from it, and the value of the
// check-avatar annotation at that read.
type avatarState struct {
	Source       string `json:"source,omitempty"`
	ETag         string `json:"etag,omitempty"`
	LastModified string `json:"lastModified,omitempty"`
	Size         int64  `json:"size,omitempty"`
	Checked      string `json:"checked,omitempty"`
}

func (a avatarState) version() pictureVersion {
	return pictureVersion{ETag: a.ETag, LastModified: a.LastModified, Size: a.Size}
}

// condition has the shape of metav1.Condition. It always writes
// message, as an empty string when there is none, and the CRD's schema
// accepts an empty message.
type condition = conditions.Condition

const avatarReady = "AvatarReady"

func (s personStatus) ready() condition {
	c, _ := conditions.Find(s.Conditions, avatarReady)
	return c
}

func (p *person) checkRequest() string {
	return p.Metadata.Annotations[checkAvatarAnnotation]
}

func personPath(name string) string { return peoplePath + "/" + name }
