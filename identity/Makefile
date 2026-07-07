# The identity domain: the cluster's certificate authorities and join
# token, generated before any machine boots.
#
# mint.sh explains the certificate authorities; kubeconfig.sh explains
# how an operator's credential is computed from them without asking
# the machine. Everything lands in dist/, which is gitignored; these
# are private keys, and they never belong in history. Losing dist/ is
# harmless for now: the next build mints a new identity and the next
# boot uses it.

# The whole identity is minted by one script run, which GNU make
# expresses as a grouped target (&:): one recipe that produces the
# whole set, rather than several rules racing to run the same script.
# mint.sh is idempotent per artifact, so a re-run (say, after the set
# grows a new artifact) fills gaps without replacing what exists.
IDENTITY := dist/tls/server-ca.crt \
            dist/tls/client-ca.crt \
            dist/tls/request-header-ca.crt \
            dist/tls/etcd/server-ca.crt \
            dist/tls/etcd/peer-ca.crt \
            dist/tls/service.key \
            dist/token

$(IDENTITY) &: mint.sh
	./mint.sh

dist/kubeconfig: kubeconfig.sh $(IDENTITY)
	./kubeconfig.sh

.PHONY: all kubeconfig clean

all: $(IDENTITY)

kubeconfig: dist/kubeconfig

clean:
	rm -rf dist
