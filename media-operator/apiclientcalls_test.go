package main

import (
	"net/http"
	"testing"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// Against a server that fails every request, each read and write
// returns the error, not an empty object.
func TestEveryCallReturnsTheServersFailure(t *testing.T) {
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

// The delete functions treat an object that is already gone as a
// success, because each can race with another pass or with a person who
// deleted the object first.
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

func TestEachReadRequestsItsOwnPath(t *testing.T) {
	api := &cannedAPI{responses: map[string]any{
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

// A Player's status is written through the status subresource, so the
// write cannot change the spec that a person declared.
func TestAPlayerStatusWritesTheStatusSubresource(t *testing.T) {
	api := &cannedAPI{responses: map[string]any{
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

// A Remote's status is written through its status subresource, so the
// write cannot change the device selector that a person declared.
func TestPutRemoteStatusWritesTheStatusSubresource(t *testing.T) {
	api := &cannedAPI{responses: map[string]any{
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

// ApplyReceiverSession sends an apply patch to the Receiver's status
// subresource with the session as the only field. A nil session sends an
// empty status, which releases the session that this field manager
// owns. The active and awake fields are always sent, even as false.
func TestTheSessionApplyCarriesTheSessionAlone(t *testing.T) {
	cases := []struct {
		name    string
		session *ReceiverSession
		want    string
	}{
		{
			name:    "a session with active and awake set",
			session: &ReceiverSession{Player: "house/theater", Input: "GAME", Active: true, Awake: true},
			want:    `{"apiVersion":"equipment.liken.sh/v1alpha1","kind":"Receiver","metadata":{"name":"den-receiver"},"status":{"session":{"player":"house/theater","input":"GAME","active":true,"awake":true}}}`,
		},
		{
			name:    "a session with neither active nor awake",
			session: &ReceiverSession{Player: "house/theater", Input: "GAME"},
			want:    `{"apiVersion":"equipment.liken.sh/v1alpha1","kind":"Receiver","metadata":{"name":"den-receiver"},"status":{"session":{"player":"house/theater","input":"GAME","active":false,"awake":false}}}`,
		},
		{
			name: "a session with volume, power, and input asks",
			session: &ReceiverSession{
				Player:    "house/theater",
				Input:     "GAME",
				Awake:     true,
				VolumeAsk: &VolumeAsk{Level: 44.5, At: "2026-10-04T12:15:25.164Z"},
				PowerAsk:  &ReceiverAction{Action: "toggle", At: "2026-10-04T12:20:01.002Z"},
				InputAsk:  &ReceiverAction{Action: inputEnsure, At: "2026-10-04T12:20:07.410Z"},
			},
			want: `{"apiVersion":"equipment.liken.sh/v1alpha1","kind":"Receiver","metadata":{"name":"den-receiver"},"status":{"session":{"player":"house/theater","input":"GAME","active":false,"awake":true,` +
				`"volumeAsk":{"level":44.5,"mute":false,"at":"2026-10-04T12:15:25.164Z"},"powerAsk":{"action":"toggle","at":"2026-10-04T12:20:01.002Z"},"inputAsk":{"action":"ensure","at":"2026-10-04T12:20:07.410Z"}}}}`,
		},
		{
			name: "no session",
			want: `{"apiVersion":"equipment.liken.sh/v1alpha1","kind":"Receiver","metadata":{"name":"den-receiver"},"status":{}}`,
		},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			api := &cannedAPI{responses: map[string]any{
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

// ReleaseReceiverSpecSession sends an apply patch with an empty spec,
// which releases the spec.session fields that this field manager owns
// and no other field.
func TestTheSpecReleaseCarriesAnEmptySpec(t *testing.T) {
	api := &cannedAPI{responses: map[string]any{
		"PATCH /apis/equipment.liken.sh/v1alpha1/receivers/den-receiver": Receiver{},
	}}

	mustSucceed(t, ReleaseReceiverSpecSession(testAPIClient(t, api.handler()), "den-receiver"))

	mustMatch(t, len(api.requests), 1)
	mustMatch(t, api.requests[0].Path, "/apis/equipment.liken.sh/v1alpha1/receivers/den-receiver")
	mustMatch(t, string(api.requests[0].Body),
		`{"apiVersion":"equipment.liken.sh/v1alpha1","kind":"Receiver","metadata":{"name":"den-receiver"},"spec":{}}`)
}
