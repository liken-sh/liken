# The identity domain: the cluster's certificate authorities and join
# token, generated before any machine boots.
#
# mint.sh explains the certificate authorities; kubeconfig.sh explains
# how an operator's credential is computed from them without asking
# the machine. An identity belongs to a deployment, not to the OS, so
# this Makefile takes the output directory as DIST and produces
# nothing of its own: the root Makefile passes this repo's own
# deployment directory (dev-cluster/identity). The files are private
# keys and never belong in history; the deployment directory
# gitignores them. Losing an identity is harmless for now: the next
# build mints a new one and the next boot uses it.
#
# The identity can also come from adopt.sh, which imports an existing
# cluster's identity instead of minting one (the migration path; see
# the Cluster spec's origin field). mint.sh keeps every artifact that
# already exists, so an adopted identity survives the rule below
# untouched.
DIST ?=

ifeq ($(strip $(DIST)),)
$(error DIST must name the deployment's identity directory, e.g. make DIST=../dev-cluster/identity)
endif

# The whole identity is minted by one script run, which GNU make
# expresses as a grouped target (&:): one recipe that produces the
# whole set, rather than several rules racing to run the same script.
# mint.sh is idempotent per artifact, so a re-run (say, after the set
# grows a new artifact) fills gaps without replacing what exists.
IDENTITY := $(DIST)/tls/server-ca.crt \
            $(DIST)/tls/client-ca.crt \
            $(DIST)/tls/request-header-ca.crt \
            $(DIST)/tls/etcd/server-ca.crt \
            $(DIST)/tls/etcd/peer-ca.crt \
            $(DIST)/tls/service.key \
            $(DIST)/token

$(IDENTITY) &: mint.sh
	./mint.sh $(DIST)

$(DIST)/kubeconfig: kubeconfig.sh $(IDENTITY)
	./kubeconfig.sh $(DIST)

.PHONY: all kubeconfig clean

all: $(IDENTITY)

kubeconfig: $(DIST)/kubeconfig

clean:
	rm -rf $(DIST)
