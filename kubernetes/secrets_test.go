package kubernetes

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"testing"
)

func TestGetRegistryCredentialsSecretDecodesData(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != RegistryCredentialsSecretPath {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		// The API serves Secret data base64-encoded. The client's
		// []byte fields must arrive already decoded.
		_ = json.NewEncoder(w).Encode(map[string]any{
			"kind": "Secret",
			"type": "kubernetes.io/dockerconfigjson",
			"data": map[string]string{
				".dockerconfigjson": base64.StdEncoding.EncodeToString([]byte(`{"auths":{}}`)),
			},
		})
	}))
	secret, err := GetRegistryCredentialsSecret(client)
	if err != nil {
		t.Fatal(err)
	}
	if secret.Type != "kubernetes.io/dockerconfigjson" {
		t.Errorf("type: got %q", secret.Type)
	}
	if got := string(secret.Data[".dockerconfigjson"]); got != `{"auths":{}}` {
		t.Errorf("data should arrive base64-decoded, got %q", got)
	}
}

func TestGetRegistryCredentialsSecretAbsenceIsOrdinary(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	secret, err := GetRegistryCredentialsSecret(client)
	if secret != nil || err != nil {
		t.Errorf("an absent Secret is nil, nil — a fleet with anonymous mirrors, not an error: %v %v", secret, err)
	}
}

func TestGetRegistryCredentialsSecretReportsRealFailures(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "etcdserver: request timed out", http.StatusInternalServerError)
	}))
	if _, err := GetRegistryCredentialsSecret(client); err == nil {
		t.Error("a 500 is a real failure and must be reported")
	}
}
