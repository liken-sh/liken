//! Who issues each `Ingress`'s TLS certificate, by either of the two
//! ways that a certificate tool's knowledge declares: the object an
//! annotation on the `Ingress` names through `referencedBy` with role
//! `issues-tls`, or the object a certificate names through a reference
//! with that role, when the certificate writes the `Secret` the
//! `Ingress` serves its TLS from.  Most clusters issue through
//! certificate objects, and the annotation takes precedence when an
//! `Ingress` has both.

use std::collections::HashMap;

use kube::ResourceExt;

use super::Snapshot;
use transect_graph::edges::Object;
use transect_network::entry::Issuers;
use transect_resources::knowledge::{self, Names, Role};

impl Snapshot {
    /// The issuer of each `Ingress` that names one, the first by kind,
    /// namespace, and name when it names several.
    pub(super) fn tls_issuers(&self) -> Issuers {
        let mut issuers = self.annotated_issuers();
        let by_secret = self.issuers_by_secret();
        for ingress in &self.ingresses {
            let namespace = ingress.namespace();
            let object = Object::new(
                "networking.k8s.io",
                "Ingress",
                namespace.as_deref(),
                &ingress.name_any(),
            );
            if issuers.contains_key(&object) {
                continue;
            }
            let tls = ingress
                .spec
                .iter()
                .flat_map(|spec| spec.tls.iter().flatten());
            let secrets = tls.filter_map(|tls| tls.secret_name.clone());
            let found = secrets
                .filter_map(|secret| by_secret.get(&(namespace.clone(), secret)))
                .min();
            if let Some(issuer) = found {
                issuers.insert(object, issuer.clone());
            }
        }
        issuers
    }

    /// The issuer of each `Secret` a certificate writes, by the
    /// `Secret`'s namespace and name: the object the certificate's
    /// reference with role `issues-tls` names, when the cluster holds it.
    fn issuers_by_secret(&self) -> HashMap<(Option<String>, String), Object> {
        let mut found: HashMap<(Option<String>, String), Object> = HashMap::new();
        for (object, relations) in &self.relations {
            let Some(known) = self.kinds.of(&object.group, &object.kind) else {
                continue;
            };
            let Some(written) = known.writes_secret.as_deref() else {
                continue;
            };
            let issuing = |path: &str| {
                let references = known.references.iter();
                references
                    .into_iter()
                    .any(|r| r.path == path && r.role == Some(Role::IssuesTls))
            };
            let secret = relations.iter().find_map(|relation| match &relation.names {
                Names::Object(secret) if relation.path == written && relation.edge.is_none() => {
                    Some(secret)
                }
                _ => None,
            });
            let issuer = relations
                .iter()
                .filter(|relation| issuing(&relation.path))
                .filter_map(|relation| match &relation.names {
                    Names::Object(issuer) if self.held.contains(issuer) => Some(issuer),
                    _ => None,
                })
                .min();
            if let (Some(secret), Some(issuer)) = (secret, issuer) {
                let key = (secret.namespace.clone(), secret.name.clone());
                let known = found.entry(key).or_insert_with(|| issuer.clone());
                if issuer < known {
                    *known = issuer.clone();
                }
            }
        }
        found
    }

    /// The issuer each `Ingress`'s annotations name.
    fn annotated_issuers(&self) -> Issuers {
        let issues_tls = |group: &str, kind: &str| {
            let known = self.kinds.of(group, kind);
            let by = known.into_iter().flat_map(|known| &known.referenced_by);
            by.into_iter().any(|by| by.role == Some(Role::IssuesTls))
        };
        let targets: Vec<Object> = self
            .custom
            .iter()
            .filter(|(resource, _)| issues_tls(&resource.group, &resource.kind))
            .flat_map(|(resource, objects)| {
                objects.iter().map(|object| {
                    let namespace = object.namespace();
                    let name = object.name_any();
                    Object::new(&resource.group, &resource.kind, namespace.as_deref(), &name)
                })
            })
            .collect();
        if targets.is_empty() {
            return Issuers::new();
        }
        let ingresses: Vec<(Object, &_)> = self
            .ingresses
            .iter()
            .map(|ingress| {
                let namespace = ingress.namespace();
                let name = ingress.name_any();
                let object =
                    Object::new("networking.k8s.io", "Ingress", namespace.as_deref(), &name);
                (object, &ingress.metadata)
            })
            .collect();
        let referrals = knowledge::referrals(&self.kinds, &targets, &ingresses);
        let mut issuers = Issuers::new();
        for referral in referrals {
            if referral.role != Some(Role::IssuesTls) {
                continue;
            }
            let issuer = issuers
                .entry(referral.referrer)
                .or_insert_with(|| referral.target.clone());
            if referral.target < *issuer {
                *issuer = referral.target;
            }
        }
        issuers
    }
}

#[cfg(test)]
mod tests {
    use std::sync::Arc;

    use rstest::rstest;

    use super::super::super::fixture;
    use super::*;

    fn storefront() -> Object {
        Object::new(
            "networking.k8s.io",
            "Ingress",
            Some("acme-shop"),
            "storefront-route",
        )
    }

    /// The fixture, with its `Ingress` annotated when `annotated` is
    /// true, and its `Certificate` kept or dropped.
    fn issued(annotated: bool, certificate: bool) -> Option<Object> {
        let mut snapshot = fixture::snapshot();
        if !certificate {
            snapshot
                .relations
                .retain(|object, _| object.kind != "Certificate");
        }
        if annotated {
            let mut ingress = (*snapshot.ingresses[0]).clone();
            ingress.metadata.annotations = Some(
                [(
                    "cert-manager.io/cluster-issuer".to_string(),
                    "shop-issuer".to_string(),
                )]
                .into(),
            );
            snapshot.ingresses[0] = Arc::new(ingress);
        }
        snapshot.tls_issuers().remove(&storefront())
    }

    #[rstest]
    #[case::by_the_certificate_that_writes_its_secret(false, true, true)]
    #[case::by_its_annotation(true, false, true)]
    #[case::by_neither(false, false, false)]
    fn finds_who_issues_an_ingresss_certificate(
        #[case] annotated: bool,
        #[case] certificate: bool,
        #[case] found: bool,
    ) {
        let issuer = Object::new("cert-manager.io", "ClusterIssuer", None, "shop-issuer");
        assert_eq!(issued(annotated, certificate), found.then_some(issuer));
    }
}
