//! The index of every name and address that a client could be
//! configured with to reach a `Service` or a machine. A client reaches
//! a `Service` by its DNS names, its load balancer's addresses, and the
//! hosts of each `Ingress` that routes to it. A client reaches a
//! machine by its name and its addresses.

use std::collections::{HashMap, HashSet};
use std::sync::Arc;

use k8s_openapi::api::core::v1::{Node, Service};
use k8s_openapi::api::networking::v1::Ingress;
use kube::ResourceExt;

use super::parse::Address;
use transect_graph::edges::{Endpoint, Object};

/// The ports an `Ingress` host accepts connections on.
const INGRESS_PORTS: [u16; 2] = [80, 443];

/// What one name reaches, and where it resolves.
#[derive(Debug)]
struct Name {
    to: Endpoint,
    /// The namespace whose pods resolve this name, for a short name
    /// that only their DNS search path completes. `None` for a name
    /// that resolves from any namespace.
    namespace: Option<String>,
    /// The ports the target accepts connections on. Empty when any port reaches it,
    /// as on a machine.
    ports: Vec<u16>,
    /// Whether it is a headless `Service`'s name, under which each of
    /// its pods has a name of its own, such as `db-0.db`.
    headless: bool,
    /// The path that an `Ingress` rule routes on this host to `to`, its
    /// `Service`. `None` for a name that is not an `Ingress` host.
    path: Option<String>,
}

/// Every name and address that reaches a `Service` or a machine, and
/// the ports that each `Service` exposes.
#[derive(Debug, Default)]
pub struct Addresses {
    names: HashMap<String, Vec<Name>>,
    /// The ports each `Service` exposes: each of its ports, and the
    /// node port of each on a `NodePort` or `LoadBalancer` `Service`,
    /// which every machine's address accepts connections on.
    exposed: HashMap<Endpoint, Vec<u16>>,
    /// The namespaces that contain a `Service`, where a two-label name
    /// such as `db.shop` is a `Service`'s name.
    namespaces: HashSet<String>,
    /// The cluster's DNS domain, such as `cluster.local`.
    domain: String,
}

impl Addresses {
    /// `domain` is the cluster's DNS domain, such as `cluster.local`.
    pub fn build(
        services: &[Arc<Service>],
        ingresses: &[Arc<Ingress>],
        nodes: &[Arc<Node>],
        domain: &str,
    ) -> Self {
        let mut addresses = Self {
            domain: domain.trim_end_matches('.').to_ascii_lowercase(),
            ..Self::default()
        };
        for service in services {
            addresses.add_service(service, domain);
        }
        for ingress in ingresses {
            addresses.add_ingress(ingress);
        }
        for node in nodes {
            addresses.add_node(node);
        }
        addresses
    }

    fn add(&mut self, name: &str, entry: Name) {
        let key = name.trim_end_matches('.').to_ascii_lowercase();
        self.names.entry(key).or_default().push(entry);
    }

    /// Indexes the DNS names and load balancer addresses of `service`.
    /// The short name resolves only in the `Service`'s own namespace.
    /// The longer forms resolve from any namespace, because the DNS
    /// search path of a pod with the default `ClusterFirst` policy
    /// includes `svc.<domain>` and `<domain>`.
    fn add_service(&mut self, service: &Service, domain: &str) {
        let Some(namespace) = service.namespace() else {
            return;
        };
        let name = service.name_any();
        let spec = service.spec.as_ref();
        let ports: Vec<u16> = spec
            .and_then(|spec| spec.ports.as_ref())
            .into_iter()
            .flatten()
            .filter_map(|port| u16::try_from(port.port).ok())
            .collect();
        let headless = spec.and_then(|spec| spec.cluster_ip.as_deref()) == Some("None");
        self.namespaces.insert(namespace.clone());
        let to = Endpoint::Object(Object::new("", "Service", Some(&namespace), &name));
        self.exposed.insert(to.clone(), exposed(service));
        let entry = |namespace: Option<&str>| Name {
            to: to.clone(),
            namespace: namespace.map(Into::into),
            ports: ports.clone(),
            headless,
            path: None,
        };
        self.add(&name, entry(Some(&namespace)));
        let long = [
            format!("{name}.{namespace}"),
            format!("{name}.{namespace}.svc"),
            format!("{name}.{namespace}.svc.{domain}"),
        ];
        for form in long {
            self.add(&form, entry(None));
        }
        let balancers = service
            .status
            .as_ref()
            .and_then(|status| status.load_balancer.as_ref())
            .and_then(|balancer| balancer.ingress.as_ref());
        for balancer in balancers.into_iter().flatten() {
            let found = [&balancer.ip, &balancer.hostname];
            for address in found.into_iter().flatten() {
                self.add(
                    address,
                    Name {
                        headless: false,
                        ..entry(None)
                    },
                );
            }
        }
    }

    /// Indexes each host of an `Ingress`'s rules with the `Service` that
    /// each of its paths routes to, so the path of a URL selects the
    /// `Service` that serves it. A wildcard host is not one address, so
    /// the index omits it. The index also omits a backend that names a
    /// resource instead of a `Service`.
    fn add_ingress(&mut self, ingress: &Ingress) {
        let Some(namespace) = ingress.namespace() else {
            return;
        };
        let rules = ingress.spec.as_ref().and_then(|spec| spec.rules.as_ref());
        for rule in rules.into_iter().flatten() {
            let Some(host) = rule.host.as_ref().filter(|host| !host.starts_with('*')) else {
                continue;
            };
            let paths = rule.http.iter().flat_map(|http| &http.paths);
            for path in paths {
                let Some(service) = &path.backend.service else {
                    continue;
                };
                let to = Object::new("", "Service", Some(&namespace), &service.name);
                let entry = Name {
                    to: Endpoint::Object(to),
                    namespace: None,
                    ports: INGRESS_PORTS.to_vec(),
                    headless: false,
                    path: Some(path.path.clone().unwrap_or_else(|| "/".into())),
                };
                self.add(host, entry);
            }
        }
    }

    fn add_node(&mut self, node: &Node) {
        let name = node.name_any();
        let entry = || Name {
            to: Endpoint::Machine(name.clone()),
            namespace: None,
            ports: Vec::new(),
            headless: false,
            path: None,
        };
        self.add(&name, entry());
        let addresses = node.status.as_ref().and_then(|s| s.addresses.as_ref());
        for address in addresses.into_iter().flatten() {
            if address.address != name {
                self.add(&address.address, entry());
            }
        }
    }

    /// The ends that `address` reaches when a pod in `namespace` is
    /// configured with it. Only a whole name matches, never part of
    /// one. A port, when one is written, must be one that the target
    /// accepts connections on. A name that matches nothing may be the name of one
    /// pod under a headless `Service`, such as `db-0.db.shop`, and then
    /// it reaches that `Service`.
    pub fn resolve(&self, address: &Address, namespace: Option<&str>) -> Vec<&Endpoint> {
        let reaches = |name: &&Name| {
            let here = name.namespace.is_none() || name.namespace.as_deref() == namespace;
            let port = match address.port {
                Some(port) => name.ports.is_empty() || name.ports.contains(&port),
                None => true,
            };
            here && port
        };
        let whole: Vec<&Name> = self
            .names
            .get(&address.host)
            .into_iter()
            .flatten()
            .filter(reaches)
            .collect();
        if !whole.is_empty() {
            return routed(whole, &address.path);
        }
        let Some((_, service)) = address.host.split_once('.') else {
            return Vec::new();
        };
        let names = self.names.get(service).into_iter().flatten();
        let headless = names.filter(|name| name.headless).filter(reaches);
        headless.map(|name| &name.to).collect()
    }

    /// The `Service` that `host` names when `host` has the form of a
    /// `Service`'s DNS name, whether or not the cluster has that
    /// `Service`: `name.namespace.svc`, `name.namespace.svc.<domain>`, or
    /// `name.namespace` when some `Service` exists in `namespace`. The
    /// two-label form needs that check because an outside host such as
    /// `example.com` also has two labels. A pod's DNS search path tries
    /// the cluster's names before the public name, so the name reaches
    /// the `Service` whenever it exists.
    pub fn service_named(&self, host: &str) -> Option<Object> {
        let host = host.trim_end_matches('.').to_ascii_lowercase();
        let full = format!(".svc.{}", self.domain);
        let (name, namespace) = match host.strip_suffix(&full).or(host.strip_suffix(".svc")) {
            Some(service) => service.split_once('.')?,
            None => host
                .split_once('.')
                .filter(|(_, namespace)| self.namespaces.contains(*namespace))?,
        };
        let label = |part: &str| !part.is_empty() && !part.contains('.');
        (label(name) && label(namespace)).then(|| Object::new("", "Service", Some(namespace), name))
    }

    /// The `Service`s that an `Ingress` routes to when `address` has
    /// the `Ingress`'s host and a port that the `Ingress` does not
    /// answer on, such as the SSH port on a forge's web host. Another
    /// `Service` of the same workload most likely serves that port, and
    /// `reach` looks for it in the graph, starting from the `Service`s
    /// returned here.
    pub fn off_ingress_port(&self, address: &Address) -> Vec<&Endpoint> {
        if address
            .port
            .is_none_or(|port| INGRESS_PORTS.contains(&port))
        {
            return Vec::new();
        }
        let names = self.names.get(&address.host).into_iter().flatten();
        let ingress = names.filter(|name| name.path.is_some());
        routed(ingress.collect(), &address.path)
    }

    /// Whether `service` exposes `port`, as one of its ports or a node
    /// port.
    pub fn exposes(&self, service: &Endpoint, port: u16) -> bool {
        self.exposed
            .get(service)
            .is_some_and(|ports| ports.contains(&port))
    }
}

/// Each port of `service`, and the node port of each when `service` is
/// a `NodePort` or `LoadBalancer` `Service`, because only those types
/// open node ports on the machines.
fn exposed(service: &Service) -> Vec<u16> {
    let Some(spec) = service.spec.as_ref() else {
        return Vec::new();
    };
    let on_machines = matches!(spec.type_.as_deref(), Some("NodePort" | "LoadBalancer"));
    let mut ports = Vec::new();
    for port in spec.ports.iter().flatten() {
        ports.extend(u16::try_from(port.port).ok());
        if on_machines {
            ports.extend(port.node_port.and_then(|node| u16::try_from(node).ok()));
        }
    }
    ports
}

/// The ends that `names` reach for the URL path `path`, each once. A
/// name that is not an `Ingress` host always counts. Of the `Ingress`
/// paths that match `path`, only the longest counts, because the
/// `Ingress` API gives the longest matching path precedence. So a host
/// whose `/` goes to an application and whose `/outpost` goes to a
/// sign-in proxy reaches only the application from a URL at its root.
fn routed<'a>(names: Vec<&'a Name>, path: &str) -> Vec<&'a Endpoint> {
    let longest = names
        .iter()
        .filter_map(|name| name.path.as_deref())
        .filter(|prefix| routes(prefix, path))
        .map(str::len)
        .max();
    let kept = names.into_iter().filter(|name| match &name.path {
        Some(prefix) => routes(prefix, path) && Some(prefix.len()) == longest,
        None => true,
    });
    let mut found: Vec<&Endpoint> = Vec::new();
    for name in kept {
        if !found.contains(&&name.to) {
            found.push(&name.to);
        }
    }
    found
}

/// Whether the `Ingress` path `prefix` routes `path`: it is `/`, the
/// path itself, or a prefix of it that ends at a segment boundary.
fn routes(prefix: &str, path: &str) -> bool {
    let prefix = prefix.trim_end_matches('/');
    prefix.is_empty()
        || path == prefix
        || path
            .strip_prefix(prefix)
            .is_some_and(|rest| rest.starts_with('/'))
}
