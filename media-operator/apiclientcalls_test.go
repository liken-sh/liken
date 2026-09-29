package main

// These tests run every call this client makes against a server that
// answers each one, and against one that fails each one, so no reader or writer
// swallows a failure.

import (
	"net/http"
	"testing"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// Every read and write this client makes, against a server that fails
// each one. A caller sees the failure rather than an empty object.
func TestEveryCallCarriesTheServersFailure(t *testing.T) {
	cases := []struct {
		name string
		call func(c *apiclient.Client) error
	}{
		{name: "get a play", call: func(c *apiclient.Client) error { _, err := GetPlay(c, "house", "movie"); return err }},
		{name: "delete a play", call: func(c *apiclient.Client) error { return DeletePlay(c, "house", "movie") }},
		{name: "get a player", call: func(c *apiclient.Client) error { _, err := GetPlayer(c, "house", "theater"); return err }},
		{name: "put a play status", call: func(c *apiclient.Client) error {
			return apiclient.ReplaceStatus(c, playPath("house", "movie"), &Play{Metadata: ObjectMeta{Name: "movie", Namespace: "house"}})
		}},
		{name: "put a player status", call: func(c *apiclient.Client) error {
			return apiclient.ReplaceStatus(c, playerPath("house", "theater"), &Player{Metadata: ObjectMeta{Name: "theater", Namespace: "house"}})
		}},
		{name: "get a remote", call: func(c *apiclient.Client) error { _, err := GetRemote(c, "house", "wand"); return err }},
		{name: "put a remote status", call: func(c *apiclient.Client) error {
			_, err := PutRemoteStatus(c, &Remote{Metadata: ObjectMeta{Name: "wand", Namespace: "house"}})
			return err
		}},
		{name: "get a claim", call: func(c *apiclient.Client) error { _, err := GetResourceClaim(c, "house", "movie"); return err }},
		{name: "create a claim", call: func(c *apiclient.Client) error {
			_, err := CreateResourceClaim(c, &ResourceClaim{Metadata: ObjectMeta{Name: "movie", Namespace: "house"}})
			return err
		}},
		{name: "delete a claim", call: func(c *apiclient.Client) error { return DeleteResourceClaim(c, "house", "movie") }},
		{name: "get a pod", call: func(c *apiclient.Client) error { _, err := GetPod(c, "house", "movie"); return err }},
		{name: "create a pod", call: func(c *apiclient.Client) error {
			_, err := CreatePod(c, &Pod{Metadata: ObjectMeta{Name: "movie", Namespace: "house"}})
			return err
		}},
		{name: "delete a pod", call: func(c *apiclient.Client) error { return DeletePod(c, "house", "movie") }},
		{name: "apply a display override", call: func(c *apiclient.Client) error {
			return ApplyDisplayOverride(c, "panel", nil)
		}},
		{name: "apply a receiver session", call: func(c *apiclient.Client) error {
			return ApplyReceiverSession(c, "den-receiver", nil)
		}},
		{name: "release a receiver spec session", call: func(c *apiclient.Client) error {
			return ReleaseReceiverSpecSession(c, "den-receiver")
		}},
	}
	client := testAPIClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			mustFail(t, each.call(client))
		})
	}
}

// The three deletes treat an already-absent object as success, because
// each one races another pass or a person who deleted the object first.
func TestAnAbsentObjectIsASuccessfulDelete(t *testing.T) {
	cases := []struct {
		name string
		call func(c *apiclient.Client) error
	}{
		{name: "a play", call: func(c *apiclient.Client) error { return DeletePlay(c, "house", "movie") }},
		{name: "a pod", call: func(c *apiclient.Client) error { return DeletePod(c, "house", "movie") }},
		{name: "a claim", call: func(c *apiclient.Client) error { return DeleteResourceClaim(c, "house", "movie") }},
	}
	client := testAPIClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			mustSucceed(t, each.call(client))
		})
	}
}

// Each read reaches its own path.
func TestEachReadNamesItsOwnPath(t *testing.T) {
	api := &cannedAPI{answers: map[string]any{
		"GET /apis/media.liken.sh/v1alpha1/namespaces/house/plays/movie":    Play{Metadata: ObjectMeta{Name: "movie"}},
		"GET /apis/media.liken.sh/v1alpha1/namespaces/house/remotes/wand":   Remote{},
		"GET /apis/resource.k8s.io/v1/namespaces/house/resourceclaims/mine": ResourceClaim{},
		"GET /api/v1/namespaces/house/pods/movie-playback":                  Pod{Metadata: ObjectMeta{Name: "movie-playback"}},
	}}
	client := testAPIClient(t, api.handler())

	play, err := GetPlay(client, "house", "movie")
	mustSucceed(t, err)
	mustMatch(t, play.Metadata.Name, "movie")

	_, err = GetRemote(client, "house", "wand")
	mustSucceed(t, err)

	_, err = GetResourceClaim(client, "house", "mine")
	mustSucceed(t, err)

	pod, err := GetPod(client, "house", "movie-playback")
	mustSucceed(t, err)
	mustMatch(t, pod.Metadata.Name, "movie-playback")
}

// A Player's status goes through the status subresource, so the write
// can never touch the spec a person declared.
func TestAPlayerStatusWritesTheStatusSubresource(t *testing.T) {
	api := &cannedAPI{answers: map[string]any{
		"PUT /apis/media.liken.sh/v1alpha1/namespaces/house/players/theater/status": Player{
			Metadata: ObjectMeta{Name: "theater", Namespace: "house", ResourceVersion: "13"},
		},
	}}
	player := &Player{
		Metadata: ObjectMeta{Name: "theater", Namespace: "house", ResourceVersion: "12"},
		Status:   PlayerStatus{Activity: "playing", Play: "movie"},
	}

	mustSucceed(t, apiclient.ReplaceStatus(testAPIClient(t, api.handler()), playerPath("house", "theater"), player))
	mustMatch(t, player.Metadata.ResourceVersion, "13")
	mustMatch(t, api.requests[0].Method, http.MethodPut)
	mustMatch(t, api.requests[0].Path, "/apis/media.liken.sh/v1alpha1/namespaces/house/players/theater/status")
}

// A Remote's status goes through its own subresource, so the write never
// rewrites the device selector a person declared.
func TestPutRemoteStatusWritesTheStatusSubresource(t *testing.T) {
	api := &cannedAPI{answers: map[string]any{
		"PUT /apis/media.liken.sh/v1alpha1/namespaces/house/remotes/wand/status": Remote{
			Metadata: ObjectMeta{Name: "wand", Namespace: "house", ResourceVersion: "8"},
		},
	}}
	remote := &Remote{Metadata: ObjectMeta{Name: "wand", Namespace: "house", ResourceVersion: "7"}}

	written, err := PutRemoteStatus(testAPIClient(t, api.handler()), remote)
	mustSucceed(t, err)
	mustMatch(t, written.Metadata.ResourceVersion, "8")
	mustMatch(t, api.requests[0].Path, "/apis/media.liken.sh/v1alpha1/namespaces/house/remotes/wand/status")
}

// The session apply reaches the Receiver's status subresource, under
// this operator's field manager, as an apply patch that carries the
// session alone. A nil session carries an empty status, which is the
// lift.
//
// The active and awake flags are always on the wire, so a session that
// carries neither still states both as false.
func TestTheSessionApplyCarriesTheSessionAlone(t *testing.T) {
	cases := []struct {
		name    string
		session *ReceiverSession
		want    string
	}{
		{
			name:    "a session the run holds",
			session: &ReceiverSession{Player: "house/theater", Input: "GAME", Active: true, Awake: true, VolumeTopic: "liken/media/players/house/theater/volume"},
			want:    `{"apiVersion":"equipment.liken.sh/v1alpha1","kind":"Receiver","metadata":{"name":"den-receiver"},"status":{"session":{"player":"house/theater","input":"GAME","active":true,"awake":true,"volumeTopic":"liken/media/players/house/theater/volume"}}}`,
		},
		{
			name:    "a session at a dark panel",
			session: &ReceiverSession{Player: "house/theater", Input: "GAME", VolumeTopic: "liken/media/players/house/theater/volume"},
			want:    `{"apiVersion":"equipment.liken.sh/v1alpha1","kind":"Receiver","metadata":{"name":"den-receiver"},"status":{"session":{"player":"house/theater","input":"GAME","active":false,"awake":false,"volumeTopic":"liken/media/players/house/theater/volume"}}}`,
		},
		{
			name: "the lift",
			want: `{"apiVersion":"equipment.liken.sh/v1alpha1","kind":"Receiver","metadata":{"name":"den-receiver"},"status":{}}`,
		},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			api := &cannedAPI{answers: map[string]any{
				"PATCH /apis/equipment.liken.sh/v1alpha1/receivers/den-receiver/status": Receiver{},
			}}

			mustSucceed(t, ApplyReceiverSession(testAPIClient(t, api.handler()), "den-receiver", each.session))

			mustMatch(t, len(api.requests), 1)
			mustMatch(t, api.requests[0].Method, http.MethodPatch)
			mustMatch(t, api.requests[0].Path, "/apis/equipment.liken.sh/v1alpha1/receivers/den-receiver/status")
			mustMatch(t, string(api.requests[0].Body), each.want)
		})
	}
}

// The spec release reaches the Receiver's own path as an apply patch
// with an empty spec, which releases the spec.session this manager
// owns and nothing else.
func TestTheSpecReleaseCarriesAnEmptySpec(t *testing.T) {
	api := &cannedAPI{answers: map[string]any{
		"PATCH /apis/equipment.liken.sh/v1alpha1/receivers/den-receiver": Receiver{},
	}}

	mustSucceed(t, ReleaseReceiverSpecSession(testAPIClient(t, api.handler()), "den-receiver"))

	mustMatch(t, len(api.requests), 1)
	mustMatch(t, api.requests[0].Path, "/apis/equipment.liken.sh/v1alpha1/receivers/den-receiver")
	mustMatch(t, string(api.requests[0].Body),
		`{"apiVersion":"equipment.liken.sh/v1alpha1","kind":"Receiver","metadata":{"name":"den-receiver"},"spec":{}}`)
}
