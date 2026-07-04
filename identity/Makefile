# The identity domain: who this cluster is, decided before it boots.
#
# mint.sh explains the certificate authorities; kubeconfig.sh explains
# how an operator's credential is computed from them without asking
# the machine. Everything lands in dist/, which is gitignored — these
# are private keys, and they never belong in history. Losing dist/ is
# harmless in the current era: the next build mints a new identity and
# the next boot wears it.

# All the authorities are minted by one script run, which GNU make
# expresses as a grouped target (&:) — one recipe that produces the
# whole set, rather than five rules racing to run the same script.
CAS := dist/tls/server-ca.crt \
       dist/tls/client-ca.crt \
       dist/tls/request-header-ca.crt \
       dist/tls/etcd/server-ca.crt \
       dist/tls/etcd/peer-ca.crt \
       dist/tls/service.key

$(CAS) &: mint.sh
	./mint.sh

dist/kubeconfig: kubeconfig.sh $(CAS)
	./kubeconfig.sh

.PHONY: all kubeconfig clean

all: $(CAS)

kubeconfig: dist/kubeconfig

clean:
	rm -rf dist
