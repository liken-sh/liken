package main

import (
	"crypto"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"math/big"
	"sync"
	"testing"
	"time"
)

func TestAFreshInstallMintsECDSAKeys(t *testing.T) {
	cases := []struct {
		name        string
		secret      string
		certificate string
		key         string
	}{
		{"the CA", apiTLSSecret, tlsCABundle, caKeyFile},
		{"the public leaf", apiTLSSecret, tlsCertFile, tlsKeyFile},
		{"the capture leaf", captureTLSSecret, tlsCertFile, tlsKeyFile},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			store := newObjectStore(t)
			if _, err := store.certificates().ensure(); err != nil {
				t.Fatalf("minting the certificates: %v", err)
			}
			held := store.secretData(t, c.secret)
			certificate, err := parseCertificate(held[c.certificate])
			if err != nil {
				t.Fatalf("parsing the certificate: %v", err)
			}
			public, ok := certificate.PublicKey.(*ecdsa.PublicKey)
			if !ok || public.Curve != elliptic.P256() {
				t.Errorf("the certificate's key is %T, want a P-256 ECDSA key", certificate.PublicKey)
			}
			if certificate.SignatureAlgorithm != x509.ECDSAWithSHA256 {
				t.Errorf("the certificate is signed with %v, want ECDSA with SHA-256", certificate.SignatureAlgorithm)
			}
			block, _ := pem.Decode(held[c.key])
			if block == nil || block.Type != "PRIVATE KEY" {
				t.Fatalf("the key is not PKCS#8 PEM: %q", held[c.key])
			}
			if _, err := x509.ParsePKCS8PrivateKey(block.Bytes); err != nil {
				t.Errorf("the key does not parse as PKCS#8: %v", err)
			}
		})
	}
}

// rsaKey is the RSA key that every fixture below signs and serves with.
// An install gives the CA and each leaf a key of its own, but these
// tests check only key types and signatures, and RSA key generation is
// slow, so they share one key.
var rsaKey = sync.OnceValue(func() *rsa.PrivateKey {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	return key
})

// rsaInstall is the certificates of a cluster installed with RSA keys:
// a CA whose key is PKCS#1 PEM, and the API leaf and the capture leaf
// that the CA signed.
type rsaInstall struct {
	authority []byte
	key       []byte
	public    *x509.Certificate
	capture   *x509.Certificate
}

// writeRSAInstall creates an rsaInstall that is valid from at, and
// writes it into the store's two Secrets and its ConfigMap.
func writeRSAInstall(t *testing.T, store *objectStore, at time.Time) rsaInstall {
	t.Helper()
	key := rsaKey()
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: DriverName + " capture CA"},
		NotBefore:             at.Add(-time.Hour),
		NotAfter:              at.Add(caLifetime),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	caBody, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("signing the RSA CA: %v", err)
	}
	authority, err := x509.ParseCertificate(caBody)
	if err != nil {
		t.Fatalf("parsing the RSA CA: %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	leaf := func(serial int64, names []string) (*x509.Certificate, []byte) {
		template := &x509.Certificate{
			SerialNumber: big.NewInt(serial),
			Subject:      pkix.Name{CommonName: names[0]},
			NotBefore:    at.Add(-time.Hour),
			NotAfter:     at.Add(leafLifetime),
			KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
			ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
			DNSNames:     names,
		}
		body, err := x509.CreateCertificate(rand.Reader, template, authority, &key.PublicKey, key)
		if err != nil {
			t.Fatalf("signing the RSA leaf: %v", err)
		}
		parsed, err := x509.ParseCertificate(body)
		if err != nil {
			t.Fatalf("parsing the RSA leaf: %v", err)
		}
		return parsed, encodeCertificate(body)
	}
	public, publicPEM := leaf(2, serviceNames(apiService, "liken-system"))
	capture, capturePEM := leaf(3, []string{captureAudience})
	caPEM := encodeCertificate(caBody)

	encoded := func(data map[string][]byte) map[string]string {
		out := map[string]string{}
		for name, value := range data {
			out[name] = base64.StdEncoding.EncodeToString(value)
		}
		return out
	}
	store.mu.Lock()
	store.secrets[apiTLSSecret] = secret{
		Metadata: EndpointMeta{Name: apiTLSSecret},
		Type:     "kubernetes.io/tls",
		Data: encoded(map[string][]byte{
			tlsCertFile: publicPEM, tlsKeyFile: keyPEM, tlsCABundle: caPEM, caKeyFile: keyPEM,
		}),
	}
	store.secrets[captureTLSSecret] = secret{
		Metadata: EndpointMeta{Name: captureTLSSecret},
		Type:     "kubernetes.io/tls",
		Data: encoded(map[string][]byte{
			tlsCertFile: capturePEM, tlsKeyFile: keyPEM, tlsCABundle: caPEM,
		}),
	}
	store.configMaps[apiCAConfigMap] = configMap{
		Metadata: EndpointMeta{Name: apiCAConfigMap},
		Data:     map[string]string{tlsCABundle: string(caPEM)},
	}
	store.mu.Unlock()
	return rsaInstall{authority: caPEM, key: keyPEM, public: public, capture: capture}
}

// The API serves the RSA leaf that it finds, and leaves the RSA CA and
// the capture leaf as they are, so clients keep the CA that they
// already trust.
func TestAnRSAInstallIsServedAsItIs(t *testing.T) {
	store := newObjectStore(t)
	at := time.Date(2026, 9, 16, 0, 0, 0, 0, time.UTC)
	install := writeRSAInstall(t, store, at)

	certs := store.certificates()
	certs.now = func() time.Time { return at.Add(30 * 24 * time.Hour) }
	served, err := certs.ensure()
	if err != nil {
		t.Fatalf("adopting the RSA install: %v", err)
	}
	if served.Leaf.SerialNumber.Cmp(install.public.SerialNumber) != 0 {
		t.Errorf("the API serves serial %v, want the RSA leaf's %v", served.Leaf.SerialNumber, install.public.SerialNumber)
	}
	if _, ok := served.PrivateKey.(*rsa.PrivateKey); !ok {
		t.Errorf("the API serves a %T key, want the RSA key", served.PrivateKey)
	}

	held := store.secretData(t, apiTLSSecret)
	if string(held[tlsCABundle]) != string(install.authority) || string(held[caKeyFile]) != string(install.key) {
		t.Error("the RSA CA was replaced")
	}
	capture, err := parseCertificate(store.secretData(t, captureTLSSecret)[tlsCertFile])
	if err != nil {
		t.Fatalf("parsing the capture leaf: %v", err)
	}
	if capture.SerialNumber.Cmp(install.capture.SerialNumber) != 0 {
		t.Error("the RSA capture leaf was minted again before its renewal")
	}
	if string(certs.anchor()) != string(install.authority) {
		t.Error("the API anchors on a CA other than the RSA CA")
	}
}

// A renewed leaf has an ECDSA key, and the RSA CA signs it with RSA and
// SHA-256, because the CA key is RSA. The RSA CA stays the CA.
func TestAnRSACASignsECDSALeavesAtRenewal(t *testing.T) {
	cases := []struct {
		name   string
		secret string
	}{
		{"the public leaf", apiTLSSecret},
		{"the capture leaf", captureTLSSecret},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			store := newObjectStore(t)
			at := time.Date(2026, 9, 16, 0, 0, 0, 0, time.UTC)
			install := writeRSAInstall(t, store, at)

			certs := store.certificates()
			certs.now = func() time.Time { return at.Add(11 * 30 * 24 * time.Hour) }
			if _, err := certs.ensure(); err != nil {
				t.Fatalf("renewing the leaves: %v", err)
			}

			leaf, err := parseCertificate(store.secretData(t, c.secret)[tlsCertFile])
			if err != nil {
				t.Fatalf("parsing the new leaf: %v", err)
			}
			if leaf.PublicKeyAlgorithm != x509.ECDSA {
				t.Errorf("the new leaf's key is %v, want ECDSA", leaf.PublicKeyAlgorithm)
			}
			if leaf.SignatureAlgorithm != x509.SHA256WithRSA {
				t.Errorf("the new leaf is signed with %v, want RSA with SHA-256", leaf.SignatureAlgorithm)
			}
			authority, err := parseCertificate(install.authority)
			if err != nil {
				t.Fatalf("parsing the RSA CA: %v", err)
			}
			if err := leaf.CheckSignatureFrom(authority); err != nil {
				t.Errorf("the new leaf is not signed by the RSA CA: %v", err)
			}
			held := store.secretData(t, apiTLSSecret)
			if string(held[tlsCABundle]) != string(install.authority) || string(held[caKeyFile]) != string(install.key) {
				t.Error("the RSA CA was replaced at a leaf's renewal")
			}
		})
	}
}

// caKeyForm is a CA private key in one PEM encoding, with the public
// key that the API must derive from it.
type caKeyForm struct {
	name   string
	body   []byte
	public crypto.PublicKey
}

// caKeyForms returns each PEM encoding of a CA key that the API
// accepts.
func caKeyForms(t *testing.T) []caKeyForm {
	t.Helper()
	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating the ECDSA key: %v", err)
	}
	sec1, err := x509.MarshalECPrivateKey(ecKey)
	if err != nil {
		t.Fatalf("encoding the ECDSA key in SEC1: %v", err)
	}
	ecPKCS8, err := x509.MarshalPKCS8PrivateKey(ecKey)
	if err != nil {
		t.Fatalf("encoding the ECDSA key in PKCS#8: %v", err)
	}
	rsaPKCS8, err := x509.MarshalPKCS8PrivateKey(rsaKey())
	if err != nil {
		t.Fatalf("encoding the RSA key in PKCS#8: %v", err)
	}
	encode := func(kind string, der []byte) []byte {
		return pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: der})
	}
	return []caKeyForm{
		{"RSA in PKCS#1", encode("RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(rsaKey())), rsaKey().Public()},
		{"RSA in PKCS#8", encode("PRIVATE KEY", rsaPKCS8), rsaKey().Public()},
		{"ECDSA in PKCS#8", encode("PRIVATE KEY", ecPKCS8), ecKey.Public()},
		{"ECDSA in SEC1", encode("EC PRIVATE KEY", sec1), ecKey.Public()},
	}
}

func TestACAKeyIsReadInEachFormASecretHolds(t *testing.T) {
	for _, form := range caKeyForms(t) {
		t.Run(form.name, func(t *testing.T) {
			key, err := parseKey(form.body)
			if err != nil {
				t.Fatalf("reading the key: %v", err)
			}
			public, ok := key.Public().(interface{ Equal(crypto.PublicKey) bool })
			if !ok || !public.Equal(form.public) {
				t.Error("the key read back is not the key written")
			}
		})
	}
}

func TestAKeyThatCannotSignIsARefusal(t *testing.T) {
	exchange, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generating the X25519 key: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(exchange)
	if err != nil {
		t.Fatalf("encoding the X25519 key in PKCS#8: %v", err)
	}
	if _, err := parseKey(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})); err == nil {
		t.Error("an X25519 key was read as a CA key")
	}
}
