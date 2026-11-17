//! An `Ingress` as the entry draws it: the hosts it answers for, the
//! controller class that serves it, the workload that runs that
//! controller, as `serving` finds it, and the source of its TLS
//! certificate.  The certificate comes from a `Secret`, or from the
//! issuer that kind knowledge's `referencedBy` with role `issues-tls`
//! finds in the `Ingress`'s annotations.

use k8s_openapi::api::networking::v1::Ingress;
use kube::ResourceExt;

use super::serving::{Served, Serving};
use super::traffic::{self, Answers, Traffic};
use transect_graph::edges::{Endpoint, Plurals};
use transect_graph::links::Reference;

/// The annotation by which an older `Ingress` names its class, from
/// before `spec.ingressClassName`.
const CLASS_ANNOTATION: &str = "kubernetes.io/ingress.class";

#[derive(Debug, PartialEq)]
pub struct IngressBox {
    pub name: String,
    /// Every host its rules name, each once, in the order they name
    /// them.  A rule with no host answers for any.
    pub hosts: Vec<String>,
    pub class: Option<String>,
    pub tls: Tls,
    /// The workload that runs its class's controller, when one is found.
    pub controller: Option<Reference>,
    /// Whether that workload is a guess, because no `IngressController`
    /// declares the class's controller.
    pub controller_guessed: bool,
    pub namespace: String,
    /// Each `Service` and port it routes to.
    pub backends: Vec<(String, String)>,
    /// Each backend's request rate, latency, and 5xx share, or why it
    /// has none.
    pub traffic: Traffic,
}

#[derive(Debug, PartialEq)]
pub enum Tls {
    /// No TLS section: it serves plain HTTP.
    None,
    /// A certificate from a `Secret`, however that `Secret` was made.
    Secret,
    /// A certificate the named issuer issues, linked to its page.
    Issuer(Reference),
}

impl IngressBox {
    /// The box of `ingress`, whose certificate `issuer` issues when one
    /// does.
    pub fn build(ingress: &Ingress, issuer: Option<Reference>) -> Self {
        let spec = ingress.spec.as_ref();
        let rules = spec.and_then(|spec| spec.rules.as_ref());
        let mut hosts: Vec<String> = Vec::new();
        for host in rules
            .into_iter()
            .flatten()
            .filter_map(|rule| rule.host.clone())
        {
            if !hosts.contains(&host) {
                hosts.push(host);
            }
        }
        let annotations = ingress.annotations();
        let class = spec
            .and_then(|spec| spec.ingress_class_name.clone())
            .or_else(|| annotations.get(CLASS_ANNOTATION).cloned());
        let has_tls = spec
            .and_then(|spec| spec.tls.as_ref())
            .is_some_and(|tls| !tls.is_empty());
        let tls = match (has_tls, issuer) {
            (false, _) => Tls::None,
            (true, Some(issuer)) => Tls::Issuer(issuer),
            (true, None) => Tls::Secret,
        };
        Self {
            name: ingress.name_any(),
            hosts,
            class,
            tls,
            controller: None,
            controller_guessed: false,
            namespace: ingress.namespace().unwrap_or_default(),
            backends: traffic::backends(ingress),
            traffic: Traffic::Undeclared,
        }
    }

    /// The box with its backends' traffic from `answers`.
    pub fn with_traffic(self, answers: Answers) -> Self {
        let traffic = traffic::of(&self.namespace, &self.backends, answers);
        Self { traffic, ..self }
    }

    /// The box with the workload that runs its controller, as
    /// `serving` finds it.
    pub fn served_by(self, serving: &Serving, plurals: &Plurals) -> Self {
        let (found, guessed) = match serving.of(self.class.as_deref()) {
            Some(Served::Declared(object)) => (Some(object), false),
            Some(Served::Guessed(object)) => (Some(object), true),
            None => (None, false),
        };
        let controller =
            found.map(|object| Reference::to_end(&Endpoint::Object(object), None, plurals));
        Self {
            controller,
            controller_guessed: guessed,
            ..self
        }
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    fn ingress(annotations: &str, spec: &str) -> Ingress {
        serde_yaml::from_str(&format!(
            "metadata: {{namespace: shop, name: front, annotations: {annotations}}}\nspec: {spec}"
        ))
        .unwrap()
    }

    #[rstest]
    #[case::each_host_once(
        "{rules: [{host: shop.example.com}, {host: api.example.com}, {host: shop.example.com}, {}]}",
        &["shop.example.com", "api.example.com"]
    )]
    #[case::no_rules("{}", &[])]
    fn reads_its_hosts(#[case] spec: &str, #[case] hosts: &[&str]) {
        assert_eq!(IngressBox::build(&ingress("{}", spec), None).hosts, hosts);
    }

    #[rstest]
    #[case::from_the_spec("{}", "{ingressClassName: web}", Some("web"))]
    #[case::from_the_annotation("{kubernetes.io/ingress.class: legacy}", "{}", Some("legacy"))]
    #[case::the_spec_first(
        "{kubernetes.io/ingress.class: legacy}",
        "{ingressClassName: web}",
        Some("web")
    )]
    #[case::none("{}", "{}", None)]
    fn reads_its_class(#[case] annotations: &str, #[case] spec: &str, #[case] class: Option<&str>) {
        let found = IngressBox::build(&ingress(annotations, spec), None);
        assert_eq!(found.class.as_deref(), class);
    }

    const TLS: &str = "{tls: [{hosts: [shop.example.com], secretName: shop-tls}]}";

    #[rstest]
    #[case::none("{}", None, Tls::None)]
    #[case::a_secret(TLS, None, Tls::Secret)]
    #[case::an_issuer(TLS, Some("internal"), Tls::Issuer(issuer("internal")))]
    #[case::an_issuer_with_no_tls("{}", Some("internal"), Tls::None)]
    fn reads_its_tls(#[case] spec: &str, #[case] named: Option<&str>, #[case] tls: Tls) {
        let found = IngressBox::build(&ingress("{}", spec), named.map(issuer));
        assert_eq!(found.tls, tls);
    }

    fn issuer(name: &str) -> Reference {
        Reference {
            text: name.into(),
            href: None,
        }
    }
}
