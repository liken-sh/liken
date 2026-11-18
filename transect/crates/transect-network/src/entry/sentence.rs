//! The text of a workload page's `Service` and `Ingress` boxes.

use super::Entry;
use super::ingress::{IngressBox, Tls};
use super::service::ServiceBox;
use transect_graph::edges::Object;
use transect_graph::links::Reference;

impl Entry {
    pub fn services_heading(&self) -> &'static str {
        match self.services.len() {
            1 => "Service",
            _ => "Services",
        }
    }
}

impl IngressBox {
    /// "web · TLS from public", "web · TLS", or "web · no TLS", where
    /// `web` is the class and is left out when there is none.
    pub fn serving(&self) -> String {
        match self.issuer() {
            Some(issuer) => format!("{} {}", self.serving_words(), issuer.text),
            None => self.serving_words(),
        }
    }

    /// `serving` without the issuer's name, so that a template can
    /// write the name as a link after it: "web · TLS from".
    pub fn serving_words(&self) -> String {
        let tls = match &self.tls {
            Tls::None => "no TLS",
            Tls::Secret => "TLS",
            Tls::Issuer(_) => "TLS from",
        };
        match &self.class {
            Some(class) => format!("{class} · {tls}"),
            None => tls.into(),
        }
    }

    pub fn issuer(&self) -> Option<&Reference> {
        match &self.tls {
            Tls::Issuer(issuer) => Some(issuer),
            _ => None,
        }
    }

    pub fn href(&self) -> Option<String> {
        let object = Object::new(
            "networking.k8s.io",
            "Ingress",
            Some(&self.namespace),
            &self.name,
        );
        transect_graph::links::page(&object, &Default::default())
    }

    /// The hosts, or "any host" when no rule sets one.
    pub fn host_lines(&self) -> Vec<String> {
        if self.hosts.is_empty() {
            return vec!["any host".into()];
        }
        self.hosts.clone()
    }
}

impl ServiceBox {
    pub fn href(&self) -> Option<String> {
        let object = Object::new("", "Service", Some(&self.namespace), &self.name);
        transect_graph::links::page(&object, &Default::default())
    }

    /// "ClusterIP :8000", "LoadBalancer :139 :445", or "headless".
    pub fn kind_line(&self) -> String {
        let ports = self.ports.iter().map(|port| format!(" :{port}"));
        format!("{}{}", self.kind, ports.collect::<String>())
    }

    /// "3 endpoints", "2 of 3 endpoints ready", or "no endpoints".
    pub fn endpoints_line(&self) -> String {
        let count = self.endpoints;
        match (count.ready, count.total) {
            (_, 0) => "no endpoints".into(),
            (1, 1) => "1 endpoint".into(),
            (ready, total) if ready == total => format!("{total} endpoints"),
            (ready, total) => format!("{ready} of {total} endpoints ready"),
        }
    }

    /// True when the `Service` has no endpoints or some of them are not
    /// ready.  The template marks such a box.
    pub fn short(&self) -> bool {
        self.endpoints.total == 0 || self.endpoints.ready < self.endpoints.total
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;
    use crate::entry::service::Count;

    fn ingress(hosts: &[&str], class: Option<&str>, tls: Tls) -> IngressBox {
        IngressBox {
            name: "front".into(),
            hosts: hosts.iter().map(|host| host.to_string()).collect(),
            class: class.map(Into::into),
            controller: None,
            controller_guessed: false,
            tls,
            namespace: "shop".into(),
            backends: Vec::new(),
            traffic: crate::entry::traffic::Traffic::Undeclared,
        }
    }

    #[rstest]
    #[case::an_issuer(ingress(&[], Some("web"), Tls::Issuer(Reference { text: "public".into(), href: None })), "web · TLS from public")]
    #[case::a_secret(ingress(&[], Some("web"), Tls::Secret), "web · TLS")]
    #[case::plain(ingress(&[], Some("web"), Tls::None), "web · no TLS")]
    #[case::no_class(ingress(&[], None, Tls::Secret), "TLS")]
    fn serving_text(#[case] ingress: IngressBox, #[case] text: &str) {
        assert_eq!(ingress.serving(), text);
    }

    #[rstest]
    #[case::hosts(&["shop.example.com", "api.example.com"], &["shop.example.com", "api.example.com"])]
    #[case::none(&[], &["any host"])]
    fn host_text(#[case] hosts: &[&str], #[case] lines: &[&str]) {
        assert_eq!(ingress(hosts, None, Tls::None).host_lines(), lines);
    }

    fn service(kind: &str, ports: &[&str], ready: usize, total: usize) -> ServiceBox {
        ServiceBox {
            name: "web".into(),
            namespace: "shop".into(),
            kind: kind.into(),
            ports: ports.iter().map(|port| port.to_string()).collect(),
            endpoints: Count { ready, total },
        }
    }

    #[rstest]
    #[case::one_port(service("ClusterIP", &["8000"], 0, 0), "ClusterIP :8000")]
    #[case::two_ports(service("LoadBalancer", &["139", "445"], 0, 0), "LoadBalancer :139 :445")]
    #[case::no_ports(service("headless", &[], 0, 0), "headless")]
    fn kind_text(#[case] service: ServiceBox, #[case] text: &str) {
        assert_eq!(service.kind_line(), text);
    }

    #[rstest]
    #[case::one(1, "Service")]
    #[case::two(2, "Services")]
    fn services_heading_text(#[case] count: usize, #[case] heading: &str) {
        let services = (0..count)
            .map(|_| service("ClusterIP", &[], 1, 1))
            .collect();
        let entry = Entry {
            ingresses: vec![],
            services,
        };
        assert_eq!(entry.services_heading(), heading);
    }

    #[rstest]
    #[case::none(service("ClusterIP", &[], 0, 0), "no endpoints", true)]
    #[case::one(service("ClusterIP", &[], 1, 1), "1 endpoint", false)]
    #[case::all_ready(service("ClusterIP", &[], 3, 3), "3 endpoints", false)]
    #[case::some_not_ready(service("ClusterIP", &[], 2, 3), "2 of 3 endpoints ready", true)]
    fn endpoints_text(#[case] service: ServiceBox, #[case] text: &str, #[case] short: bool) {
        assert_eq!(
            (service.endpoints_line().as_str(), service.short()),
            (text, short)
        );
    }
}
