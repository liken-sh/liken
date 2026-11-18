package main

// The API mints its own certificate authority (CA) at its first start,
// so an install needs no cert-manager and no setup step before the
// first request. The CA signs a leaf for the names of the API's
// Service, and a leaf for the capture sidecars under the name
// display-capture. An owner who runs cert-manager writes the Secret
// display-api-tls instead, and the API serves that Secret's
// certificate and mints nothing.

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// A leaf is renewed when less than a third of its life remains, so a
// pod that was down for several months still starts with a valid leaf.
const (
	authorityLife = 10 * 365 * 24 * time.Hour
	leafLife      = 365 * 24 * time.Hour
)

// sidecarName is the only SAN on the sidecar's leaf. The API checks the
// sidecar's certificate against this name and not against the pod's IP
// address, because a pod gets a new IP address when it restarts.
const sidecarName = captureAudience

// kubernetes.io/tls is the Secret type for a serving certificate, and
// it requires tls.crt and tls.key. The API also writes ca.crt. It
// writes ca.key only in the API's own Secret, so that after a restart
// the API signs with the CA it minted.
const (
	tlsSecretType = "kubernetes.io/tls"
	tlsCertKey    = "tls.crt"
	tlsKeyKey     = "tls.key"
	caCertKey     = "ca.crt"
	caKeyKey      = "ca.key"
)

// objectMeta, Secret, and ConfigMap have only the fields that this
// program reads and writes.
type objectMeta struct {
	Name            string `json:"name"`
	Namespace       string `json:"namespace,omitempty"`
	ResourceVersion string `json:"resourceVersion,omitempty"`
}

// Data is map[string][]byte because Secret data is base64 in JSON, and
// encoding/json encodes a []byte as base64.
type Secret struct {
	APIVersion string            `json:"apiVersion,omitempty"`
	Kind       string            `json:"kind,omitempty"`
	Metadata   objectMeta        `json:"metadata"`
	Type       string            `json:"type,omitempty"`
	Data       map[string][]byte `json:"data,omitempty"`
}

type ConfigMap struct {
	APIVersion string            `json:"apiVersion,omitempty"`
	Kind       string            `json:"kind,omitempty"`
	Metadata   objectMeta        `json:"metadata"`
	Data       map[string]string `json:"data,omitempty"`
}

func secretsPath(namespace string) string {
	return "/api/v1/namespaces/" + namespace + "/secrets"
}

func configMapsPath(namespace string) string {
	return "/api/v1/namespaces/" + namespace + "/configmaps"
}

// certificateAuthority is a CA's certificate and private key, parsed
// for signing, and the PEM encoding of each that the Secret stores.
type certificateAuthority struct {
	certificate *x509.Certificate
	key         *ecdsa.PrivateKey
	certPEM     []byte
	keyPEM      []byte
}

// mintAuthority creates a self-signed CA that is valid for
// authorityLife from now.
func mintAuthority(now time.Time) (*certificateAuthority, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generating the authority's key: %w", err)
	}
	serial, err := randomSerial()
	if err != nil {
		return nil, err
	}

	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: apiAudience + "-ca"},
		NotBefore:             now,
		NotAfter:              now.Add(authorityLife),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, key.Public(), key)
	if err != nil {
		return nil, fmt.Errorf("signing the authority certificate: %w", err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("parsing the signed authority certificate: %w", err)
	}
	keyPEM, err := privateKeyPEM(key)
	if err != nil {
		return nil, err
	}
	return &certificateAuthority{
		certificate: certificate,
		key:         key,
		certPEM:     pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		keyPEM:      keyPEM,
	}, nil
}

// mintLeaf creates a serving certificate for dnsNames, signed by the
// CA and valid for leafLife from now, and returns it and its private
// key as PEM.
func (ca *certificateAuthority) mintLeaf(commonName string, dnsNames []string, now time.Time) (certPEM, keyPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generating the leaf certificate's key: %w", err)
	}
	serial, err := randomSerial()
	if err != nil {
		return nil, nil, err
	}

	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: commonName},
		DNSNames:              dnsNames,
		NotBefore:             now,
		NotAfter:              now.Add(leafLife),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca.certificate, key.Public(), ca.key)
	if err != nil {
		return nil, nil, fmt.Errorf("signing the leaf certificate: %w", err)
	}
	keyPEM, err = privateKeyPEM(key)
	if err != nil {
		return nil, nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), keyPEM, nil
}

// needsRenewal reports whether less than a third of the leaf's life
// remains at now.
func needsRenewal(leaf *x509.Certificate, now time.Time) bool {
	life := leaf.NotAfter.Sub(leaf.NotBefore)
	return 3*leaf.NotAfter.Sub(now) < life
}

// RFC 5280 requires a certificate's serial number to be positive and
// unique among the certificates of one CA. A random number below 2^128,
// plus one, is positive, and a collision is not a practical risk.
func randomSerial() (*big.Int, error) {
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, fmt.Errorf("generating a random serial number: %w", err)
	}
	return serial.Add(serial, big.NewInt(1)), nil
}

func privateKeyPEM(key *ecdsa.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("encoding a private key as PKCS #8: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil
}

// servingMaterial is what the API serves TLS with: the leaf, its key,
// the CA certificate that clients trust, and the leaf's expiry, which
// the certificate expiry gauge reports.
type servingMaterial struct {
	CertPEM, KeyPEM []byte
	CAPEM           []byte
	Expires         time.Time
}

// ensureServingMaterial reads the Secret that the API serves from, and
// creates it with a new CA and leaf when it does not exist.
//
// A Secret without ca.key belongs to the owner, who wrote it with
// cert-manager or by hand. For such a Secret, it returns a nil
// authority and mints nothing.
func ensureServingMaterial(c *apiclient.Client, namespace, secretName string, names []string, now time.Time) (*servingMaterial, *certificateAuthority, error) {
	held, err := apiclient.Get[Secret](c, secretsPath(namespace)+"/"+secretName)
	if err == nil {
		return readServingMaterial(held)
	}
	if !errors.Is(err, apiclient.ErrNotFound) {
		return nil, nil, err
	}

	ca, err := mintAuthority(now)
	if err != nil {
		return nil, nil, err
	}
	certPEM, keyPEM, err := ca.mintLeaf(apiAudience, names, now)
	if err != nil {
		return nil, nil, err
	}
	secret := &Secret{
		APIVersion: "v1",
		Kind:       "Secret",
		Metadata:   objectMeta{Name: secretName, Namespace: namespace},
		Type:       tlsSecretType,
		Data: map[string][]byte{
			tlsCertKey: certPEM,
			tlsKeyKey:  keyPEM,
			caCertKey:  ca.certPEM,
			caKeyKey:   ca.keyPEM,
		},
	}

	// When the create loses a race, use the Secret that won. Two
	// replicas that each kept their own CA would serve certificates
	// from different CAs, and a client that trusts one CA would reject
	// the other replica.
	err = writeObject(c, http.MethodPost, secretsPath(namespace), secret)
	if errors.Is(err, apiclient.ErrConflict) {
		winner, readErr := apiclient.Get[Secret](c, secretsPath(namespace)+"/"+secretName)
		if readErr != nil {
			return nil, nil, readErr
		}
		return readServingMaterial(winner)
	}
	if err != nil {
		return nil, nil, err
	}
	return readServingMaterial(secret)
}

// readServingMaterial reads the serving material from a Secret, and
// the CA too when the Secret has ca.key.
func readServingMaterial(secret *Secret) (*servingMaterial, *certificateAuthority, error) {
	leaf, err := leafOf(secret)
	if err != nil {
		return nil, nil, err
	}
	material := &servingMaterial{
		CertPEM: secret.Data[tlsCertKey],
		KeyPEM:  secret.Data[tlsKeyKey],
		CAPEM:   secret.Data[caCertKey],
		Expires: leaf.NotAfter,
	}
	if len(secret.Data[caKeyKey]) == 0 {
		return material, nil, nil
	}
	ca, err := readAuthority(secret.Data[caCertKey], secret.Data[caKeyKey])
	if err != nil {
		return nil, nil, err
	}
	return material, ca, nil
}

// readAuthority parses a CA from its PEM certificate and its PKCS #8
// ECDSA private key.
func readAuthority(certPEM, keyPEM []byte) (*certificateAuthority, error) {
	certificate, err := parsePEM(certPEM)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(keyPEM)
	if block == nil {
		return nil, fmt.Errorf("the authority's private key contains no PEM block")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parsing the authority's private key: %w", err)
	}
	key, ok := parsed.(*ecdsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("the authority's private key is %T, want ECDSA", parsed)
	}
	return &certificateAuthority{certificate: certificate, key: key, certPEM: certPEM, keyPEM: keyPEM}, nil
}

func leafOf(secret *Secret) (*x509.Certificate, error) {
	return parsePEM(secret.Data[tlsCertKey])
}

func parsePEM(certPEM []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return nil, fmt.Errorf("the certificate contains no PEM block")
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parsing a certificate: %w", err)
	}
	return certificate, nil
}

// ensureSidecarLeaf writes the sidecar's leaf to its own Secret, which
// the operator's pod on each node mounts as an optional volume. It
// creates the Secret when it is missing, and replaces the leaf when the
// leaf does not parse, has another SAN, or is due for renewal.
func ensureSidecarLeaf(c *apiclient.Client, namespace, secretName string, ca *certificateAuthority, sanName string, now time.Time) error {
	path := secretsPath(namespace) + "/" + secretName
	held, err := apiclient.Get[Secret](c, path)
	if err != nil && !errors.Is(err, apiclient.ErrNotFound) {
		return err
	}
	if err == nil && sidecarLeafUsable(held, sanName, now) {
		return nil
	}

	certPEM, keyPEM, err := ca.mintLeaf(sanName, []string{sanName}, now)
	if err != nil {
		return err
	}
	secret := &Secret{
		APIVersion: "v1",
		Kind:       "Secret",
		Metadata:   objectMeta{Name: secretName, Namespace: namespace},
		Type:       tlsSecretType,
		Data: map[string][]byte{
			tlsCertKey: certPEM,
			tlsKeyKey:  keyPEM,
			caCertKey:  ca.certPEM,
		},
	}
	if held == nil {
		err = writeObject(c, http.MethodPost, secretsPath(namespace), secret)
		// A conflict means another API replica created the Secret
		// first. Every replica signs with the CA in the API's own
		// Secret, so the other replica's leaf is as valid as this one.
		if errors.Is(err, apiclient.ErrConflict) {
			return nil
		}
		return err
	}
	secret.Metadata.ResourceVersion = held.Metadata.ResourceVersion
	return writeObject(c, http.MethodPut, path, secret)
}

// renewServingMaterial mints a new API leaf when the current one needs
// renewal, writes it to the Secret, and returns the new material.
// Otherwise it returns nil and writes nothing.
func renewServingMaterial(c *apiclient.Client, namespace, secretName string,
	ca *certificateAuthority, names []string, now time.Time) (*servingMaterial, error) {
	path := secretsPath(namespace) + "/" + secretName
	held, err := apiclient.Get[Secret](c, path)
	if err != nil {
		return nil, err
	}
	leaf, err := leafOf(held)
	if err != nil {
		return nil, err
	}
	if !needsRenewal(leaf, now) {
		return nil, nil
	}
	certPEM, keyPEM, err := ca.mintLeaf(apiAudience, names, now)
	if err != nil {
		return nil, err
	}
	held.Data[tlsCertKey] = certPEM
	held.Data[tlsKeyKey] = keyPEM
	if err := writeObject(c, http.MethodPut, path, held); err != nil {
		return nil, err
	}
	material, _, err := readServingMaterial(held)
	return material, err
}

// sidecarLeafUsable reports whether the sidecar's Secret has a usable
// leaf: one whose only SAN is sanName and that is not due for renewal.
// It does not check which CA signed the leaf.
func sidecarLeafUsable(secret *Secret, sanName string, now time.Time) bool {
	leaf, err := leafOf(secret)
	if err != nil {
		return false
	}
	if len(leaf.DNSNames) != 1 || leaf.DNSNames[0] != sanName {
		return false
	}
	return !needsRenewal(leaf, now)
}

// ensureTrustAnchor writes the CA certificate, without the key, to a
// ConfigMap, so a client can read it with no access to Secrets.
func ensureTrustAnchor(c *apiclient.Client, namespace, configMapName string, caPEM []byte) error {
	path := configMapsPath(namespace) + "/" + configMapName
	held, err := apiclient.Get[ConfigMap](c, path)
	if err != nil && !errors.Is(err, apiclient.ErrNotFound) {
		return err
	}

	anchor := &ConfigMap{
		APIVersion: "v1",
		Kind:       "ConfigMap",
		Metadata:   objectMeta{Name: configMapName, Namespace: namespace},
		Data:       map[string]string{caCertKey: string(caPEM)},
	}
	if held == nil {
		err = writeObject(c, http.MethodPost, configMapsPath(namespace), anchor)
		// When the create loses a race, keep the winner if it has the
		// same CA, and otherwise replace it. The replacement sends the
		// winner's resourceVersion, so it fails with a conflict when
		// another write came first.
		if errors.Is(err, apiclient.ErrConflict) {
			winner, readErr := apiclient.Get[ConfigMap](c, path)
			if readErr != nil {
				return readErr
			}
			if winner.Data[caCertKey] == string(caPEM) {
				return nil
			}
			anchor.Metadata.ResourceVersion = winner.Metadata.ResourceVersion
			return writeObject(c, http.MethodPut, path, anchor)
		}
		return err
	}
	if held.Data[caCertKey] == string(caPEM) {
		return nil
	}
	anchor.Metadata.ResourceVersion = held.Metadata.ResourceVersion
	return writeObject(c, http.MethodPut, path, anchor)
}

func writeObject(c *apiclient.Client, method, path string, object any) error {
	body, err := json.Marshal(object)
	if err != nil {
		return err
	}
	return c.RequestJSON(method, path, body, nil)
}
