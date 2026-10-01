package main

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
)

// sinkServer is an API server that holds some Sinks and records the
// Events it receives.
type sinkServer struct {
	sinks  map[string]Sink
	mutex  sync.Mutex
	events []event
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
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/events"):
			var posted event
			_ = json.NewDecoder(r.Body).Decode(&posted)
			s.mutex.Lock()
			s.events = append(s.events, posted)
			s.mutex.Unlock()
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte("{}"))
		default:
			http.Error(w, "unexpected", http.StatusBadRequest)
		}
	})
}

// The declare container names the endpoints itself, reads each Sink,
// and keys the layouts by the node they declare. A Sink that does not
// exist yet and a Sink with no layout give nothing.
func TestTheDeclareContainerReadsEachSinksLayout(t *testing.T) {
	endpoints := likenOne(t)
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

// An API server that refuses the read leaves every layout to the ELD
// and the channel map, and the pod starts.
func TestTheDeclareContainerFallsBackWhenTheSinksDoNotRead(t *testing.T) {
	endpoints := likenOne(t)
	var outputs []alsaEndpoint
	for _, endpoint := range endpoints {
		outputs = append(outputs, endpoint)
	}
	if specs := apiSpecs(testClient(t, failingAPI()), "liken-1", outputs); specs != nil {
		t.Errorf("specs = %v from an API server that refused", specs)
	}
}

// A spec.layout that a person writes reaches the declaration, and the
// change is recorded as one Event on the Sink.
func TestASpecLayoutRewritesTheDeclarationAndRecordsAnEvent(t *testing.T) {
	operator, endpoints := layoutReconciler(t)
	analog := endpoints[1]
	quad := []string{"FL", "FR", "RL", "RR"}
	server := &sinkServer{sinks: map[string]Sink{
		analog.Name(): {Metadata: EndpointMeta{Name: analog.Name(), UID: "uid-1"}, Spec: SinkSpec{Layout: quad}},
	}}
	operator.control = &endpointControl{client: testClient(t, server.handler()), machine: "node-1"}

	states := operator.reconcileLayouts(endpoints, pwGraph{})
	want := channelLayout{Source: layoutFromSpec, Positions: quad}
	if state := states[analog.Name()]; !state.Layout.equal(want) {
		t.Errorf("the analog sink reports %s, want %s", state.Layout, want)
	}
	if len(server.events) != 1 {
		t.Fatalf("events = %+v, want one", server.events)
	}
	recorded := server.events[0]
	if recorded.Reason != layoutChangedReason || recorded.InvolvedObject.UID != "uid-1" ||
		!strings.Contains(recorded.Message, "from no positions to FL,FR,RL,RR from Spec") {
		t.Errorf("the event is %+v", recorded)
	}
}
