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
			t.Errorf("the request path is %q, want the Secret's", r.URL.Path)
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
		t.Errorf("the Secret type is %q, want the one in the response", secret.Type)
	}
	if got := string(secret.Data[".dockerconfigjson"]); got != `{"auths":{}}` {
		t.Errorf("the data is %q, want it base64-decoded", got)
	}
}

func TestGetRegistryCredentialsSecretAbsenceIsOrdinary(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	secret, err := GetRegistryCredentialsSecret(client)
	if secret != nil || err != nil {
		t.Errorf("the result for an absent Secret is %v, %v, want nil and no error", secret, err)
	}
}

func TestGetRegistryCredentialsSecretReportsRealFailures(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "etcdserver: request timed out", http.StatusInternalServerError)
	}))
	if _, err := GetRegistryCredentialsSecret(client); err == nil {
		t.Error("a 500 response returned no error, want one")
	}
}
