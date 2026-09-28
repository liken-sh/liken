package kubernetes

// A client for the operator's workstation, built from the admin
// kubeconfig that the identity package computes. The in-cluster
// client (apiclient.go) authenticates with a ServiceAccount bearer
// token that kubelet mounts and refreshes. A workstation has no
// kubelet and no token. Its kubeconfig embeds a client certificate
// instead, and the TLS handshake itself carries the identity: the
// API server reads the certificate's CN as the username and each O
// as a group, with no user database behind it. So this client sends
// no Authorization header at all.

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"sigs.k8s.io/yaml"
)

// kubeconfigFile is the part of a kubeconfig this client reads: the
// first cluster's address and CA, and the first user's certificate.
// liken writes single-entry kubeconfigs (identity/kubeconfig.go),
// and this parser holds no opinion about richer files beyond taking
// their first entries. The []byte fields decode from base64 on
// their own: sigs.k8s.io/yaml routes through encoding/json, which
// defines []byte that way, and kubeconfig chose base64 for the same
// reason.
type kubeconfigFile struct {
	Clusters []struct {
		Cluster struct {
			Server                   string `json:"server"`
			CertificateAuthorityData []byte `json:"certificate-authority-data"`
		} `json:"cluster"`
	} `json:"clusters"`
	Users []struct {
		User struct {
			ClientCertificateData []byte `json:"client-certificate-data"`
			ClientKeyData         []byte `json:"client-key-data"`
		} `json:"user"`
	} `json:"users"`
}

// KubeconfigClient builds a client from a kubeconfig file. The
// client trusts only the embedded CA, not the system trust store,
// so it accepts only the cluster's own API server. The timeouts
// match the in-cluster client's reasoning (apiclient.go): every one
// of them limits a server that stops responding without sending any
// signal.
func KubeconfigClient(path string) (*Client, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var kc kubeconfigFile
	if err := yaml.Unmarshal(raw, &kc); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	switch {
	case len(kc.Clusters) == 0 && len(kc.Users) == 0:
		return nil, fmt.Errorf("%s names no cluster and no user", path)
	case len(kc.Clusters) == 0:
		return nil, fmt.Errorf("%s names no cluster", path)
	case len(kc.Users) == 0:
		return nil, fmt.Errorf("%s names no user", path)
	}
	clusterEntry, userEntry := kc.Clusters[0].Cluster, kc.Users[0].User

	cert, err := tls.X509KeyPair(userEntry.ClientCertificateData, userEntry.ClientKeyData)
	if err != nil {
		return nil, fmt.Errorf("reading the client certificate in %s: %w", path, err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(clusterEntry.CertificateAuthorityData) {
		return nil, fmt.Errorf("%s carries no certificate authority", path)
	}

	return NewClient(clusterEntry.Server, &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs:      roots,
				Certificates: []tls.Certificate{cert},
			},
			DialContext: (&net.Dialer{
				Timeout:   5 * time.Second,
				KeepAlive: 10 * time.Second,
			}).DialContext,
			ResponseHeaderTimeout: 10 * time.Second,
			IdleConnTimeout:       30 * time.Second,
		},
	}, ""), nil
}
