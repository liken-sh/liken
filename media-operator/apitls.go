package main

// This file creates and renews the TLS certificate that media-api
// serves. media-api uses HTTPS because every request carries a Bearer
// token and the cluster network does not encrypt traffic. media-api
// creates its own CA and leaf certificate, so a cluster with no
// certificate manager has an encrypted API on first start. A cluster
// owner who has a CA can replace both objects, and media-api serves
// what it finds in them.
//
// The leaf certificate and its key are in the Secret media-api-tls,
// because only media-api needs the private key. The CA certificate is
// also in the ConfigMap media-api-ca, so a client that cannot read
// Secrets can still trust the API.

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

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// The leaf certificate is valid only for apiServiceDNSName, the DNS
// name of the media-api Service.
const (
	apiTLSSecretName   = "media-api-tls"
	apiCAConfigMapName = "media-api-ca"
	apiServiceDNSName  = "media-api.liken-system.svc"
)

// tls.crt and tls.key are the keys of a kubernetes.io/tls Secret, so
// other tools can read the pair. The keeper also stores ca.crt and
// ca.key in the Secret, so it can sign each renewed leaf with the same
// CA.
const (
	apiTLSCertKey    = "tls.crt"
	apiTLSKeyKey     = "tls.key"
	apiCACertKey     = "ca.crt"
	apiCAKeyKey      = "ca.key"
	apiTLSSecretType = "kubernetes.io/tls"
)

// The keeper renews the leaf when less than 1/apiLeafRenewDivisor of
// its lifetime remains, about four months for a one-year leaf. That
// gives a failed renewal months to succeed, and a clock that is off by
// a few days does not matter.
const (
	apiAuthorityLife    = 10 * 365 * 24 * time.Hour
	apiLeafLife         = 365 * 24 * time.Hour
	apiLeafRenewDivisor = 3
)

// Secret is the core Secret. The API server sends data values as
// base64, which encoding/json decodes into a []byte field.
type Secret struct {
	APIVersion string            `json:"apiVersion,omitempty"`
	Kind       string            `json:"kind,omitempty"`
	Metadata   ObjectMeta        `json:"metadata"`
	Type       string            `json:"type,omitempty"`
	Data       map[string][]byte `json:"data,omitempty"`
}

func secretsPath(namespace string) string { return podPrefix + namespace + "/secrets" }

func GetSecret(c *apiclient.Client, namespace, name string) (*Secret, error) {
	secret := &Secret{}
	if err := c.RequestJSON(http.MethodGet, secretsPath(namespace)+"/"+name, nil, secret); err != nil {
		return nil, err
	}
	return secret, nil
}

func CreateSecret(c *apiclient.Client, secret *Secret) (*Secret, error) {
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

// UpdateSecret replaces the whole Secret with a PUT. The caller passes
// the Secret it read, so the CA keys in it are written back unchanged.
func UpdateSecret(c *apiclient.Client, secret *Secret) (*Secret, error) {
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

// certificateKeeper creates the serving certificate on first start,
// and after that loads it from the Secret and renews it near expiry.
type certificateKeeper struct {
	client    *apiclient.Client
	namespace string
	// now is a field so a test can move the clock to the end of a
	// leaf's lifetime.
	now func() time.Time

	mu       sync.Mutex
	notAfter time.Time
}

func newCertificateKeeper(client *apiclient.Client, namespace string) *certificateKeeper {
	return &certificateKeeper{client: client, namespace: namespace, now: time.Now}
}

// ensure returns the certificate to serve. It reads or creates the
// Secret, creates the CA ConfigMap when it is missing, and renews a
// leaf that is near expiry. media-api calls it at start, every
// certificateReview, and each time the Secret changes.
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

// expirySeconds returns the seconds until the loaded leaf expires, for
// the certificate expiry gauge. It returns zero before any certificate
// is loaded, so an alert on the gauge treats that state as expired.
func (k *certificateKeeper) expirySeconds() float64 {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.notAfter.IsZero() {
		return 0
	}
	return k.notAfter.Sub(k.now()).Seconds()
}

// pair reads the Secret, or creates it when it does not exist. It never
// overwrites an existing Secret, so an owner's pair is kept. A conflict
// on create means another media-api pod created the Secret first, and
// pair reads that one, so both pods serve the same certificate.
func (k *certificateKeeper) pair() (*Secret, error) {
	secret, err := GetSecret(k.client, k.namespace, apiTLSSecretName)
	if err == nil {
		return secret, nil
	}
	if !errors.Is(err, apiclient.ErrNotFound) {
		return nil, fmt.Errorf("reading Secret %s/%s: %w", k.namespace, apiTLSSecretName, err)
	}

	minted, err := k.mint()
	if err != nil {
		return nil, err
	}
	created, err := CreateSecret(k.client, minted)
	if errors.Is(err, apiclient.ErrConflict) {
		won, err := GetSecret(k.client, k.namespace, apiTLSSecretName)
		if err != nil {
			return nil, fmt.Errorf("reading Secret %s/%s after a conflict: %w", k.namespace, apiTLSSecretName, err)
		}
		return won, nil
	}
	if err != nil {
		return nil, fmt.Errorf("creating Secret %s/%s: %w", k.namespace, apiTLSSecretName, err)
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

// publish creates the CA ConfigMap when it does not exist, and leaves
// an existing one unchanged. It does nothing for a Secret with no
// ca.crt. A conflict on create is not an error, because another
// media-api pod created the ConfigMap from the same Secret.
func (k *certificateKeeper) publish(caPEM []byte) error {
	if len(caPEM) == 0 {
		return nil
	}
	_, err := GetConfigMap(k.client, k.namespace, apiCAConfigMapName)
	if err == nil {
		return nil
	}
	if !errors.Is(err, apiclient.ErrNotFound) {
		return fmt.Errorf("reading ConfigMap %s/%s: %w", k.namespace, apiCAConfigMapName, err)
	}

	_, err = CreateConfigMap(k.client, &ConfigMap{
		APIVersion: podAPIVersion,
		Kind:       "ConfigMap",
		Metadata:   ObjectMeta{Name: apiCAConfigMapName, Namespace: k.namespace},
		Data:       map[string]string{apiCACertKey: string(caPEM)},
	})
	if errors.Is(err, apiclient.ErrConflict) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("creating ConfigMap %s/%s: %w", k.namespace, apiCAConfigMapName, err)
	}
	return nil
}

// renew signs a new leaf and updates the Secret when the leaf is near
// expiry. A Secret with no ca.key came from an owner, and renew returns
// it unchanged even when it is near expiry. The owner learns of the
// expiry from the gauge that expirySeconds feeds.
func (k *certificateKeeper) renew(secret *Secret) (*Secret, error) {
	leaf, err := parseCertificate(secret.Data[apiTLSCertKey])
	if err != nil {
		return nil, fmt.Errorf("reading %s from Secret %s/%s: %w", apiTLSCertKey, k.namespace, apiTLSSecretName, err)
	}
	if !k.expiring(leaf) || len(secret.Data[apiCAKeyKey]) == 0 {
		return secret, nil
	}

	ca, err := authorityFrom(secret.Data[apiCACertKey], secret.Data[apiCAKeyKey])
	if err != nil {
		return nil, fmt.Errorf("reading the CA from Secret %s/%s: %w", k.namespace, apiTLSSecretName, err)
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
		return nil, fmt.Errorf("updating Secret %s/%s: %w", k.namespace, apiTLSSecretName, err)
	}
	return written, nil
}

func (k *certificateKeeper) expiring(leaf *x509.Certificate) bool {
	life := leaf.NotAfter.Sub(leaf.NotBefore)
	return leaf.NotAfter.Sub(k.now()) < life/apiLeafRenewDivisor
}

// load builds the tls.Certificate from the Secret and records the
// leaf's expiry for expirySeconds.
func (k *certificateKeeper) load(secret *Secret) (*tls.Certificate, error) {
	pair, err := tls.X509KeyPair(secret.Data[apiTLSCertKey], secret.Data[apiTLSKeyKey])
	if err != nil {
		return nil, fmt.Errorf("loading the pair from Secret %s/%s: %w", k.namespace, apiTLSSecretName, err)
	}
	leaf, err := parseCertificate(secret.Data[apiTLSCertKey])
	if err != nil {
		return nil, fmt.Errorf("reading %s from Secret %s/%s: %w", apiTLSCertKey, k.namespace, apiTLSSecretName, err)
	}
	pair.Leaf = leaf

	k.mu.Lock()
	defer k.mu.Unlock()
	k.notAfter = leaf.NotAfter
	return &pair, nil
}

// authority is the CA, parsed and as the PEM that the Secret stores.
type authority struct {
	certificate *x509.Certificate
	key         *ecdsa.PrivateKey
	certPEM     []byte
	keyPEM      []byte
}

// mintAuthority creates a self-signed CA with a P-256 key. Its path
// length is zero, so it can sign leaf certificates but no
// intermediate CAs.
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

// authorityFrom parses the CA stored in the Secret, so a renewed leaf
// is signed by the CA that clients already trust.
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
	key, isECDSA := parsed.(*ecdsa.PrivateKey)
	if !isECDSA {
		return nil, fmt.Errorf("the CA key is a %T, want an ECDSA key", parsed)
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

// mintLeaf creates a leaf certificate and key for one DNS name. The
// certificate is valid for server authentication only.
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

// serialNumber returns a random serial number, so two certificates of
// one CA are unlikely to share one. It stays below 2^159 so that its
// DER encoding, with the sign bit, fits in the 20 octets that RFC 5280
// allows.
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

// parseCertificate parses the first certificate in PEM data.
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
