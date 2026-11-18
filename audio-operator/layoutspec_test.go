package main

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
)

// sinkServer is an API server that serves a fixed set of Sinks.
type sinkServer struct {
	sinks map[string]Sink
}

func (s *sinkServer) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, SinksPath+"/"):
			sink, ok := s.sinks[strings.TrimPrefix(r.URL.Path, SinksPath+"/")]
			if !ok {
				http.Error(w, `{"kind":"Status","code":404}`, http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(sink)
		default:
			http.Error(w, "the fake API server serves only a GET of one Sink", http.StatusBadRequest)
		}
	})
}

// The declare container names the endpoints itself, reads each Sink,
// and keys each layout by the graph address of its endpoint. A missing
// Sink and a Sink with no layout produce no entry.
func TestTheDeclareContainerReadsEachSinksLayout(t *testing.T) {
	endpoints := liken1Endpoints(t)
	hdmi := endpoints["liken-1-pci-0000-00-1f-3-hdmi-0"]
	quad := []string{"FL", "FR", "RL", "RR"}
	server := &sinkServer{sinks: map[string]Sink{
		hdmi.Name(): {Metadata: EndpointMeta{Name: hdmi.Name()}, Spec: SinkSpec{Layout: quad}},
	}}
	var outputs []alsaEndpoint
	for _, endpoint := range endpoints {
		endpoint.DeviceName = ""
		outputs = append(outputs, endpoint)
	}

	specs := apiSpecs(testClient(t, server.handler()), "liken-1", outputs)
	if len(specs) != 1 || !slices.Equal(specs[hdmi.graphAddress()], quad) {
		t.Errorf("specs = %v, want the HDMI slot's alone", specs)
	}
}

// When the API server refuses the read, the declare container returns no
// specs, so every layout comes from the ELD and the channel map and the
// pod still starts.
func TestTheDeclareContainerFallsBackWhenTheSinksCannotBeRead(t *testing.T) {
	endpoints := liken1Endpoints(t)
	var outputs []alsaEndpoint
	for _, endpoint := range endpoints {
		outputs = append(outputs, endpoint)
	}
	if specs := apiSpecs(testClient(t, failingAPI()), "liken-1", outputs); specs != nil {
		t.Errorf("apiSpecs returned %v from an API server that refused, want nil", specs)
	}
}
