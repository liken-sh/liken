package main

// This file mints and keeps the serving pair. media-api serves HTTPS
// because a Bearer token travels in every request and the cluster
// network carries no encryption of its own. It mints its own CA and
// leaf rather than depending on another controller, so a cluster with
// no certificate manager still gets an encrypted API on first start,
// and an owner with a CA replaces both objects and the API serves
// what it finds. The key and the leaf go to a Secret, media-api-tls,
// because a private key is read by this process alone. The CA
// certificate alone goes to a ConfigMap, media-api-ca, because it is
// public data that a client with no Secret access must read to trust
// the API.

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"sync"
	"time"
)

// The names of the two objects, and the one DNS name the leaf serves,
// the Service in liken-system.
const (
	apiTLSSecretName   = "media-api-tls"
	apiCAConfigMapName = "media-api-ca"
	apiServiceDNSName  = "media-api.liken-system.svc"
)

// The keys of the Secret. tls.crt and tls.key are the Kubernetes
// convention for a kubernetes.io/tls Secret, so other tooling reads
// the pair. ca.crt and ca.key are kept beside them so the keeper can
// re-mint a leaf under the same CA a year later, and an owner's pair
// with no ca.key is served unchanged.
const (
	apiTLSCertKey    = "tls.crt"
	apiTLSKeyKey     = "tls.key"
	apiCACertKey     = "ca.crt"
	apiCAKeyKey      = "ca.key"
	apiTLSSecretType = "kubernetes.io/tls"
)

// The lifetimes. The CA is valid for ten years, longer than a machine. The
// leaf is valid for one year, and the keeper re-mints it when under a third
// of that life remains, so a rotation has months of margin and a
// clock that is off by days changes nothing.
const (
	apiAuthorityLife = 10 * 365 * 24 * time.Hour
	apiLeafLife      = 365 * 24 * time.Hour
	apiLeafRemint    = 3
)

// Secret is the core Secret as this program reads and writes it. The
// data values are base64 in JSON, which a []byte field encodes and
// decodes on its own.
type Secret struct {
	APIVersion string            `json:"apiVersion,omitempty"`
	Kind       string            `json:"kind,omitempty"`
	Metadata   ObjectMeta        `json:"metadata"`
	Type       string            `json:"type,omitempty"`
	Data       map[string][]byte `json:"data,omitempty"`
}

func secretsPath(namespace string) string { return podPrefix + namespace + "/secrets" }

func GetSecret(c *Client, namespace, name string) (*Secret, error) {
	secret := &Secret{}
	if err := c.RequestJSON(http.MethodGet, secretsPath(namespace)+"/"+name, nil, secret); err != nil {
		return nil, err
	}
	return secret, nil
}

func CreateSecret(c *Client, secret *Secret) (*Secret, error) {
	body, err := json.Marshal(secret)
	if err != nil {
		return nil, err
	}
	created := &Secret{}
	if err := c.RequestJSON(http.MethodPost, secretsPath(secret.Metadata.Namespace), body, created); err != nil {
		return nil, err
	}
	return created, nil
}

// UpdateSecret writes the whole object back, so a re-minted leaf lands
// beside the CA that is already there and the CA is never lost.
func UpdateSecret(c *Client, secret *Secret) (*Secret, error) {
	body, err := json.Marshal(secret)
	if err != nil {
		return nil, err
	}
	written := &Secret{}
	path := secretsPath(secret.Metadata.Namespace) + "/" + secret.Metadata.Name
	if err := c.RequestJSON(http.MethodPut, path, body, written); err != nil {
		return nil, err
	}
	return written, nil
}

// certificateKeeper owns the serving pair. It mints one at first
// start, publishes the CA, and afterwards answers the pair it finds,
// re-minting the leaf as it nears its end.
type certificateKeeper struct {
	client    *Client
	namespace string
	// The clock is a field so a test drives a leaf to the end of its
	// life without waiting a year.
	now func() time.Time

	mu       sync.Mutex
	notAfter time.Time
}

func newCertificateKeeper(client *Client, namespace string) *certificateKeeper {
	return &certificateKeeper{client: client, namespace: namespace, now: time.Now}
}

// ensure is the whole of the start-up and of each later review: read
// or mint the pair, publish the CA certificate, renew a leaf near its
// end, and load what stands.
func (k *certificateKeeper) ensure() (*tls.Certificate, error) {
	secret, err := k.pair()
	if err != nil {
		return nil, err
	}
	if err := k.publish(secret.Data[apiCACertKey]); err != nil {
		return nil, err
	}
	secret, err = k.renew(secret)
	if err != nil {
		return nil, err
	}
	return k.load(secret)
}

// expirySeconds is what the certificate expiry gauge reads: the
// seconds the loaded leaf has left. A keeper that has loaded nothing
// answers zero, which an alert reads as expired rather than as fine.
func (k *certificateKeeper) expirySeconds() float64 {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.notAfter.IsZero() {
		return 0
	}
	return k.notAfter.Sub(k.now()).Seconds()
}

// pair reads the Secret or mints one. The API never overwrites a pair
// that exists, so an owner's pair stands. A create that loses the
// race to another pod reads the winner, so two pods that start at
// once serve one pair.
func (k *certificateKeeper) pair() (*Secret, error) {
	secret, err := GetSecret(k.client, k.namespace, apiTLSSecretName)
	if err == nil {
		return secret, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, fmt.Errorf("reading secret %s/%s: %w", k.namespace, apiTLSSecretName, err)
	}

	minted, err := k.mint()
	if err != nil {
		return nil, err
	}
	created, err := CreateSecret(k.client, minted)
	if errors.Is(err, ErrConflict) {
		won, err := GetSecret(k.client, k.namespace, apiTLSSecretName)
		if err != nil {
			return nil, fmt.Errorf("reading secret %s/%s after a conflict: %w", k.namespace, apiTLSSecretName, err)
		}
		return won, nil
	}
	if err != nil {
		return nil, fmt.Errorf("creating secret %s/%s: %w", k.namespace, apiTLSSecretName, err)
	}
	return created, nil
}

func (k *certificateKeeper) mint() (*Secret, error) {
	now := k.now()
	ca, err := mintAuthority(now)
	if err != nil {
		return nil, err
	}
	certPEM, keyPEM, err := ca.mintLeaf(now, apiServiceDNSName)
	if err != nil {
		return nil, err
	}
	return &Secret{
		APIVersion: podAPIVersion,
		Kind:       "Secret",
		Metadata:   ObjectMeta{Name: apiTLSSecretName, Namespace: k.namespace},
		Type:       apiTLSSecretType,
		Data: map[string][]byte{
			apiTLSCertKey: certPEM,
			apiTLSKeyKey:  keyPEM,
			apiCACertKey:  ca.certPEM,
			apiCAKeyKey:   ca.keyPEM,
		},
	}, nil
}

// publish writes the CA certificate to the ConfigMap, the anchor a
// client with no Secret access reads. A ConfigMap that exists is left
// as it is, and a create that meets a conflict is success, because
// the other writer published the same CA.
func (k *certificateKeeper) publish(caPEM []byte) error {
	if len(caPEM) == 0 {
		return nil
	}
	_, err := GetConfigMap(k.client, k.namespace, apiCAConfigMapName)
	if err == nil {
		return nil
	}
	if !errors.Is(err, ErrNotFound) {
		return fmt.Errorf("reading configmap %s/%s: %w", k.namespace, apiCAConfigMapName, err)
	}

	_, err = CreateConfigMap(k.client, &ConfigMap{
		APIVersion: podAPIVersion,
		Kind:       "ConfigMap",
		Metadata:   ObjectMeta{Name: apiCAConfigMapName, Namespace: k.namespace},
		Data:       map[string]string{apiCACertKey: string(caPEM)},
	})
	if errors.Is(err, ErrConflict) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("creating configmap %s/%s: %w", k.namespace, apiCAConfigMapName, err)
	}
	return nil
}

// renew re-mints the leaf when under a third of its life remains and
// writes it back. An owner's pair with no CA key cannot be re-signed
// and is served as it stands; the expiry gauge is the owner's
// warning.
func (k *certificateKeeper) renew(secret *Secret) (*Secret, error) {
	leaf, err := parseCertificate(secret.Data[apiTLSCertKey])
	if err != nil {
		return nil, fmt.Errorf("reading %s from secret %s/%s: %w", apiTLSCertKey, k.namespace, apiTLSSecretName, err)
	}
	if !k.expiring(leaf) || len(secret.Data[apiCAKeyKey]) == 0 {
		return secret, nil
	}

	ca, err := authorityFrom(secret.Data[apiCACertKey], secret.Data[apiCAKeyKey])
	if err != nil {
		return nil, fmt.Errorf("reading the CA from secret %s/%s: %w", k.namespace, apiTLSSecretName, err)
	}
	now := k.now()
	certPEM, keyPEM, err := ca.mintLeaf(now, apiServiceDNSName)
	if err != nil {
		return nil, err
	}
	secret.Data[apiTLSCertKey] = certPEM
	secret.Data[apiTLSKeyKey] = keyPEM
	written, err := UpdateSecret(k.client, secret)
	if err != nil {
		return nil, fmt.Errorf("updating secret %s/%s: %w", k.namespace, apiTLSSecretName, err)
	}
	return written, nil
}

func (k *certificateKeeper) expiring(leaf *x509.Certificate) bool {
	life := leaf.NotAfter.Sub(leaf.NotBefore)
	return leaf.NotAfter.Sub(k.now()) < life/apiLeafRemint
}

// load builds the tls.Certificate and parses its leaf, which the
// expiry gauge reads, and records the leaf's end.
func (k *certificateKeeper) load(secret *Secret) (*tls.Certificate, error) {
	pair, err := tls.X509KeyPair(secret.Data[apiTLSCertKey], secret.Data[apiTLSKeyKey])
	if err != nil {
		return nil, fmt.Errorf("loading the pair from secret %s/%s: %w", k.namespace, apiTLSSecretName, err)
	}
	leaf, err := parseCertificate(secret.Data[apiTLSCertKey])
	if err != nil {
		return nil, fmt.Errorf("reading %s from secret %s/%s: %w", apiTLSCertKey, k.namespace, apiTLSSecretName, err)
	}
	pair.Leaf = leaf

	k.mu.Lock()
	defer k.mu.Unlock()
	k.notAfter = leaf.NotAfter
	return &pair, nil
}

// authority is this domain's CA: the certificate, the key that signs
// a leaf, and each one's PEM as the Secret holds it.
type authority struct {
	certificate *x509.Certificate
	key         *ecdsa.PrivateKey
	certPEM     []byte
	keyPEM      []byte
}

// mintAuthority mints the self-signed CA of the first start: a P-256
// key, ten years, and a path length of zero, so it signs leaves and
// no further CA.
func mintAuthority(now time.Time) (*authority, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generating the CA key: %w", err)
	}
	serial, err := serialNumber()
	if err != nil {
		return nil, err
	}
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: apiCAConfigMapName},
		NotBefore:             now,
		NotAfter:              now.Add(apiAuthorityLife),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("signing the CA certificate: %w", err)
	}
	return newAuthority(der, key)
}

// authorityFrom reads the CA back off the Secret, which is what signs
// every later leaf, so a re-minted leaf verifies against the CA the
// clients already hold.
func authorityFrom(certPEM, keyPEM []byte) (*authority, error) {
	certificate, err := parseCertificate(certPEM)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(keyPEM)
	if block == nil {
		return nil, errors.New("the CA key is not PEM")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("reading the CA key: %w", err)
	}
	key, held := parsed.(*ecdsa.PrivateKey)
	if !held {
		return nil, fmt.Errorf("the CA key is %T, not an ECDSA key", parsed)
	}
	return &authority{
		certificate: certificate,
		key:         key,
		certPEM:     certPEM,
		keyPEM:      keyPEM,
	}, nil
}

func newAuthority(der []byte, key *ecdsa.PrivateKey) (*authority, error) {
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("reading the CA certificate back: %w", err)
	}
	keyPEM, err := encodeKey(key)
	if err != nil {
		return nil, err
	}
	return &authority{
		certificate: certificate,
		key:         key,
		certPEM:     encodePEM("CERTIFICATE", der),
		keyPEM:      keyPEM,
	}, nil
}

// mintLeaf mints the serving leaf: one DNS name, server
// authentication only, one year.
func (a *authority) mintLeaf(now time.Time, dnsName string) (certPEM, keyPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generating the serving key: %w", err)
	}
	serial, err := serialNumber()
	if err != nil {
		return nil, nil, err
	}
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: dnsName},
		DNSNames:              []string{dnsName},
		NotBefore:             now,
		NotAfter:              now.Add(apiLeafLife),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, a.certificate, &key.PublicKey, a.key)
	if err != nil {
		return nil, nil, fmt.Errorf("signing the serving certificate: %w", err)
	}
	keyPEM, err = encodeKey(key)
	if err != nil {
		return nil, nil, err
	}
	return encodePEM("CERTIFICATE", der), keyPEM, nil
}

// serialNumber draws a random serial under 2^159, large enough that
// two mints never collide and within the 20 bytes RFC 5280 allows.
func serialNumber() (*big.Int, error) {
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 159))
	if err != nil {
		return nil, fmt.Errorf("drawing a serial number: %w", err)
	}
	return serial, nil
}

func encodeKey(key *ecdsa.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("encoding the key: %w", err)
	}
	return encodePEM("PRIVATE KEY", der), nil
}

func encodePEM(kind string, der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: der})
}

// parseCertificate reads the first certificate in a PEM file, which
// is the leaf on a Secret and the current CA on a ConfigMap.
func parseCertificate(certPEM []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return nil, errors.New("the certificate is not PEM")
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("reading the certificate: %w", err)
	}
	return certificate, nil
}
