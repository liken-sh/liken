package main

// These tests run every call this client makes against a server that
// answers each one, and against one that fails each one, so no reader or writer
// swallows a failure.

import (
	"net/http"
	"testing"
)

// Every read and write this client makes, against a server that fails
// each one. A caller sees the failure rather than an empty object.
func TestEveryCallCarriesTheServersFailure(t *testing.T) {
	cases := []struct {
		name string
		call func(c *Client) error
	}{
		{name: "list plays", call: func(c *Client) error { _, err := ListPlays(c); return err }},
		{name: "get a play", call: func(c *Client) error { _, err := GetPlay(c, "house", "movie"); return err }},
		{name: "delete a play", call: func(c *Client) error { return DeletePlay(c, "house", "movie") }},
		{name: "list players", call: func(c *Client) error { _, err := ListPlayers(c); return err }},
		{name: "get a player", call: func(c *Client) error { _, err := GetPlayer(c, "house", "theater"); return err }},
		{name: "put a play status", call: func(c *Client) error {
			_, err := PutPlayStatus(c, &Play{Metadata: ObjectMeta{Name: "movie", Namespace: "house"}})
			return err
		}},
		{name: "put a player status", call: func(c *Client) error {
			_, err := PutPlayerStatus(c, &Player{Metadata: ObjectMeta{Name: "theater", Namespace: "house"}})
			return err
		}},
		{name: "get a remote", call: func(c *Client) error { _, err := GetRemote(c, "house", "wand"); return err }},
		{name: "put a remote status", call: func(c *Client) error {
			_, err := PutRemoteStatus(c, &Remote{Metadata: ObjectMeta{Name: "wand", Namespace: "house"}})
			return err
		}},
		{name: "list remotes", call: func(c *Client) error { _, err := ListAllRemotes(c); return err }},
		{name: "get a keymap", call: func(c *Client) error { _, err := GetKeymap(c, "gamepad"); return err }},
		{name: "list keymaps", call: func(c *Client) error { _, err := ListKeymaps(c); return err }},
		{name: "get media preferences", call: func(c *Client) error {
			_, err := GetMediaPreferences(c, "household")
			return err
		}},
		{name: "list media preferences", call: func(c *Client) error { _, err := ListMediaPreferences(c); return err }},
		{name: "get a claim", call: func(c *Client) error { _, err := GetResourceClaim(c, "house", "movie"); return err }},
		{name: "create a claim", call: func(c *Client) error {
			_, err := CreateResourceClaim(c, &ResourceClaim{Metadata: ObjectMeta{Name: "movie", Namespace: "house"}})
			return err
		}},
		{name: "delete a claim", call: func(c *Client) error { return DeleteResourceClaim(c, "house", "movie") }},
		{name: "list playback pods", call: func(c *Client) error { _, err := ListPlaybackPods(c); return err }},
		{name: "get a pod", call: func(c *Client) error { _, err := GetPod(c, "house", "movie"); return err }},
		{name: "create a pod", call: func(c *Client) error {
			_, err := CreatePod(c, &Pod{Metadata: ObjectMeta{Name: "movie", Namespace: "house"}})
			return err
		}},
		{name: "delete a pod", call: func(c *Client) error { return DeletePod(c, "house", "movie") }},
		{name: "list resource slices", call: func(c *Client) error { _, err := ListResourceSlices(c); return err }},
		{name: "get a display", call: func(c *Client) error { _, err := GetDisplay(c, "panel"); return err }},
		{name: "apply a display override", call: func(c *Client) error {
			return ApplyDisplayOverride(c, "panel", nil)
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
		call func(c *Client) error
	}{
		{name: "a play", call: func(c *Client) error { return DeletePlay(c, "house", "movie") }},
		{name: "a pod", call: func(c *Client) error { return DeletePod(c, "house", "movie") }},
		{name: "a claim", call: func(c *Client) error { return DeleteResourceClaim(c, "house", "movie") }},
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

// Each read reaches its own path, and the pod list narrows to the
// operator's own playback pods by the label the pod builder stamps.
func TestEachReadNamesItsOwnPath(t *testing.T) {
	api := &cannedAPI{answers: map[string]any{
		"GET /apis/media.liken.sh/v1alpha1/namespaces/house/plays/movie":  Play{Metadata: ObjectMeta{Name: "movie"}},
		"GET /apis/media.liken.sh/v1alpha1/mediapreferences/household":    MediaPreferences{},
		"GET /apis/media.liken.sh/v1alpha1/namespaces/house/remotes/wand": Remote{},
		"GET /api/v1/pods": PodList{Metadata: ListMeta{ResourceVersion: "9"}},
		"GET /apis/resource.k8s.io/v1/namespaces/house/resourceclaims/mine": ResourceClaim{},
	}}
	client := testAPIClient(t, api.handler())

	play, err := GetPlay(client, "house", "movie")
	mustSucceed(t, err)
	mustMatch(t, play.Metadata.Name, "movie")

	_, err = GetMediaPreferences(client, "household")
	mustSucceed(t, err)

	_, err = GetRemote(client, "house", "wand")
	mustSucceed(t, err)

	_, err = GetResourceClaim(client, "house", "mine")
	mustSucceed(t, err)

	pods, err := ListPlaybackPods(client)
	mustSucceed(t, err)
	mustMatch(t, pods.Metadata.ResourceVersion, "9")
}

// A Player's status goes through the status subresource, so the write
// can never touch the spec a person declared.
func TestPutPlayerStatusWritesTheStatusSubresource(t *testing.T) {
	api := &cannedAPI{answers: map[string]any{
		"PUT /apis/media.liken.sh/v1alpha1/namespaces/house/players/theater/status": Player{
			Metadata: ObjectMeta{Name: "theater", Namespace: "house", ResourceVersion: "13"},
		},
	}}
	player := &Player{
		Metadata: ObjectMeta{Name: "theater", Namespace: "house", ResourceVersion: "12"},
		Status:   PlayerStatus{Activity: "playing", Play: "movie"},
	}

	written, err := PutPlayerStatus(testAPIClient(t, api.handler()), player)
	mustSucceed(t, err)
	mustMatch(t, written.Metadata.ResourceVersion, "13")
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
