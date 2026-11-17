//! The index of the cluster's addresses: every name and address a
//! client could be configured with to reach a `Service` or a machine.
//! A client reaches a `Service` by its DNS names, its load balancer's
//! addresses, and the hosts of each `Ingress` that routes to it, and
//! reaches a machine by its name and its addresses.

use std::collections::{HashMap, HashSet};
use std::sync::Arc;

use k8s_openapi::api::core::v1::{Node, Service};
use k8s_openapi::api::networking::v1::Ingress;
use kube::ResourceExt;

use super::parse::Address;
use transect_graph::edges::{Endpoint, Object};

/// The ports an `Ingress` host answers on.
const INGRESS_PORTS: [u16; 2] = [80, 443];

/// What one name reaches, and where it resolves.
#[derive(Debug)]
struct Name {
    to: Endpoint,
    /// The namespace a short name resolves in, through the search path
    /// of that namespace's pods only; none for a name that resolves from
    /// anywhere.
    namespace: Option<String>,
    /// The ports the target answers on; empty when any port reaches it,
    /// as on a machine.
    ports: Vec<u16>,
    /// Whether it is a headless `Service`'s name, under which each of
    /// its pods has a name of its own, such as `db-0.db`.
    headless: bool,
    /// The path an `Ingress` rule routes on this host to `to`, its
    /// `Service`; none for a name that is not an `Ingress` host.
    path: Option<String>,
}

#[derive(Debug, Default)]
pub struct Addresses {
    names: HashMap<String, Vec<Name>>,
    /// The ports each `Service` exposes: each of its ports, and the
    /// node port of each on a `NodePort` or `LoadBalancer` `Service`,
    /// which every machine's address answers on.
    exposed: HashMap<Endpoint, Vec<u16>>,
    /// The namespaces that contain a `Service`, where a two-label name
    /// such as `db.shop` is a `Service`'s name.
    namespaces: HashSet<String>,
    /// The cluster's DNS domain, such as `cluster.local`.
    domain: String,
}

impl Addresses {
    /// Indexes each `Service`'s DNS names in every form, and its load
    /// balancer's addresses; each `Ingress`'s hosts; and each machine's
    /// name and addresses.  `domain` is the cluster's DNS domain, such
    /// as `cluster.local`.
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

    /// The short name resolves only in the `Service`'s own namespace; the
    /// longer forms resolve from any namespace, through the
    /// `svc.<domain>` and `<domain>` entries of every pod's search path.
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

    /// Each host of an `Ingress`'s rules reaches the `Service` each of
    /// its paths routes to, so a URL's path selects the `Service` that
    /// serves it, as the ingress controller would.  A host with a
    /// wildcard names no one address, so the index omits it, and it
    /// omits a backend that names a resource instead of a `Service`.
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

    /// What `address` reaches when a pod in `namespace` is configured
    /// with it.  Only a whole name matches, never part of one.  A port,
    /// when one is written, must be one the target answers on.  A name
    /// that matches nothing may be one pod's name under a headless
    /// `Service`, such as `db-0.db.shop`, which reaches that `Service`.
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

    /// The `Service` that `host` names in a form only a `Service`'s DNS
    /// name has, whether or not the cluster still has that `Service`:
    /// `name.namespace.svc`, `name.namespace.svc.<domain>`, or
    /// `name.namespace` where the namespace contains a `Service`,
    /// because an outside host such as `example.com` has two labels
    /// too.  Such a name never reaches outside the cluster: in a pod,
    /// the search path resolves it before any public name.
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

    /// Each `Service` that an `Ingress` routes `address` to when its
    /// host is the `Ingress`'s and its port is one the `Ingress` does
    /// not answer on, such as a forge's SSH port on the forge's web
    /// host.  Another `Service` of the same workload most likely serves
    /// that port.  Finding it needs the graph, so `reach` finds it from
    /// these.
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

/// Each port of `service`, and on a `NodePort` or `LoadBalancer`
/// `Service` the node port of each.  Kubernetes gives a `ClusterIP`
/// `Service` no node ports, and a stale one in its spec answers
/// nowhere.
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

/// Of `names`, the `Ingress` paths that route `path` and every name
/// that is not an `Ingress` path.  The function keeps only the longest
/// matching path, as an ingress controller selects it, so a host whose
/// `/` goes to an application and whose `/outpost` goes to a sign-in
/// proxy reaches only the application from a URL at its root.
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

/// Whether an `Ingress` path routes `path`: it is `/`, the path itself,
/// or a prefix of it that ends between two of its segments.
fn routes(prefix: &str, path: &str) -> bool {
    let prefix = prefix.trim_end_matches('/');
    prefix.is_empty()
        || path == prefix
        || path
            .strip_prefix(prefix)
            .is_some_and(|rest| rest.starts_with('/'))
}
