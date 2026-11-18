package main

import (
	"context"
	"errors"
	"maps"
	"strings"
	"testing"
	"testing/synctest"
)

func TestTheAgentReadsItsNodeAndPodFromTheDownwardAPI(t *testing.T) {
	set := map[string]string{"NODE_NAME": "node-1", "POD_NAME": "media-capabilities-x", "POD_NAMESPACE": "liken-system"}
	env, err := downwardEnv(func(name string) string { return set[name] })
	if err != nil || !maps.Equal(env, set) {
		t.Errorf("downwardEnv = %v, %v, want %v and no error", env, err, set)
	}
}

func TestTheAgentRefusesToStartWithoutItsNode(t *testing.T) {
	_, err := downwardEnv(func(name string) string { return "" })
	if err == nil || !strings.Contains(err.Error(), "NODE_NAME") {
		t.Errorf("downwardEnv with no variables returned %v, want an error that names NODE_NAME", err)
	}
}

func TestTheQueryChildIsThisProgramInQueryMode(t *testing.T) {
	q, err := selfQuerier()
	if err != nil {
		t.Fatalf("building the querier: %v", err)
	}
	cmd := q.command(t.Context(), "/dev/dri/renderD128")
	if got := strings.Join(cmd.Args[1:], " "); got != "query /dev/dri/renderD128" || q.timeout != queryTimeout {
		t.Errorf("the query child runs %q with timeout %s, want %q with %s", got, q.timeout, "query /dev/dri/renderD128", queryTimeout)
	}
}

// oneVersion is a watch that delivers one version of the slice and
// closes its channel when its context ends.
func oneVersion(slice *ResourceSlice) func(context.Context, func(*ResourceSlice)) <-chan struct{} {
	return func(ctx context.Context, seen func(*ResourceSlice)) <-chan struct{} {
		done := make(chan struct{})
		go func() {
			defer close(done)
			seen(slice)
			<-ctx.Done()
		}()
		return done
	}
}

func servesUntilTheEnd(ctx context.Context) error {
	<-ctx.Done()
	return nil
}

func TestTheAgentReadsItsClaimAndPublishesTheSliceTheWatchDelivers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		api, client := startAPI(t)
		node := nodeObject{}
		node.Metadata.Name, node.Metadata.UID = "node-1", "uid-1"
		api.put(t, "/api/v1/nodes/node-1", node)
		api.put(t, "/api/v1/namespaces/liken-system/pods/media-capabilities-x", map[string]any{
			"status": map[string]any{"resourceClaimStatuses": []any{
				map[string]any{"name": claimName, "resourceClaimName": "claim-1"},
			}},
		})
		api.put(t, "/apis/resource.k8s.io/v1/namespaces/liken-system/resourceclaims/claim-1", map[string]any{
			"status": map[string]any{"allocation": map[string]any{"devices": map[string]any{"results": []any{
				map[string]any{"driver": "liken.sh", "device": "pci-0000-00-02-0"},
			}}}},
		})
		queries := &fakeQueries{answers: map[string]report{"/dev/dri/renderD128": meteorLake()}}
		a, err := newAgent(client, map[string]string{
			"NODE_NAME": "node-1", "POD_NAME": "media-capabilities-x", "POD_NAMESPACE": "liken-system",
		}, queries.query)
		if err != nil {
			t.Fatalf("creating the agent: %v", err)
		}
		a.renderNode = renderNodeOf

		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() {
			done <- operate(ctx, a, oneVersion(likenSlice(gpuDevice("pci-0000-00-02-0", "0000:00:02.0"))), servesUntilTheEnd)
		}()
		synctest.Wait()
		slice, ok := get[ResourceSlice](t, api, mediaSlicePath)
		cancel()
		if err := <-done; err != nil {
			t.Errorf("operate returned %v after its context ended", err)
		}
		if !ok || len(slice.Spec.Devices) != 1 || slice.Metadata.OwnerReferences[0].UID != "uid-1" {
			t.Errorf("slice = %+v, want the GPU's device, owned by the Node", slice)
		}
	})
}

func TestTheAgentStopsWhenTheKubeletPluginFails(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a, _ := testAgent(t, &fakeQueries{})
		err := operate(t.Context(), a, oneVersion(nil), func(context.Context) error {
			return errors.New("the registration socket: bind: address already in use")
		})
		if err == nil || !strings.Contains(err.Error(), "address already in use") {
			t.Errorf("operate returned %v, want the error of the plugin", err)
		}
	})
}

func TestAnAgentWhoseNodeCannotBeReadDoesNotStart(t *testing.T) {
	_, client := startAPI(t)
	_, err := newAgent(client, map[string]string{"NODE_NAME": "node-1"}, nil)
	if err == nil || !strings.Contains(err.Error(), "node-1") {
		t.Errorf("newAgent returned %v, want an error that names node-1", err)
	}
}
