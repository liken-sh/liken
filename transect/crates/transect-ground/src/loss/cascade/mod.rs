//! The cascade: what depends on a down workload and loses it, found one
//! hop out along the edges that point at the workload.
//!
//! A workload configured with the down workload's address loses that
//! workload. An application that signs its users in through it loses
//! sign-in. A host that an `Ingress` routes to it through a `Service`
//! stops responding, and a custom resource that it controls has no
//! controller. The walk stops after one hop. Only intent records
//! whether the loss of a dependency takes the dependent down too, so a
//! second hop read from the API alone would report outages that do not
//! happen.
//!
//! The sidebar of a loss of machines and the card of one workload,
//! `IfDown`, both use this code, so both show the same lines for a
//! workload.

use std::collections::BTreeMap;
use std::sync::Arc;

use k8s_openapi::api::networking::v1::Ingress;
use kube::ResourceExt;

use transect_graph::edges::{Endpoint, Graph, Kind, Object, Provenance};
use transect_graph::links::Linker;
use transect_resources::intent::{Intents, Line};

/// What one dependent loses when one workload goes down.  `provides` is
/// the down workload's `provides` text from intent.  A caller's line
/// shows it in place of the workload's name, because "loses office file
/// conversion" tells the reader more than a `Deployment`'s name.
#[derive(Clone, Debug, PartialEq, Eq, PartialOrd, Ord)]
pub struct Loss {
    pub dependent: Dependent,
    pub lost: Object,
    pub provides: Option<Line>,
    /// Where the edge was written, when only intent records the edge.
    pub declared_in: Option<Provenance>,
}

/// What depends on a down workload, by the kind of edge that points at
/// the workload.
#[derive(Clone, Debug, PartialEq, Eq, PartialOrd, Ord)]
pub enum Dependent {
    /// A workload configured with its address, from a depends-on edge.
    Caller(Object),
    /// An application that signs its users in through it, from an
    /// authenticates-through edge.
    SignsIn(Object),
    /// A host that an `Ingress` routes to a `Service` that selects it.
    Host(String),
    /// A custom resource that it controls, from a controlled-by edge.
    Controlled(Object),
}

impl Dependent {
    /// The sort key within a line: callers, then applications that sign
    /// in, then hosts, then custom resources.  Each group sorts by the
    /// name that the line shows, so the reader scans names in order.
    fn order(&self) -> (u8, &str, &str) {
        match self {
            Self::Caller(object) => (0, &object.name, &object.kind),
            Self::SignsIn(object) => (1, &object.name, &object.kind),
            Self::Host(host) => (2, host, ""),
            Self::Controlled(object) => (3, &object.name, &object.kind),
        }
    }

    /// The dependent's object, or `None` for a host.
    pub fn object(&self) -> Option<&Object> {
        match self {
            Self::Caller(object) | Self::SignsIn(object) | Self::Controlled(object) => Some(object),
            Self::Host(_) => None,
        }
    }

    /// The name that a line shows for the dependent.  Two dependents of
    /// different kinds can have the same name.
    pub fn name(&self) -> &str {
        match self {
            Self::Caller(object) | Self::SignsIn(object) | Self::Controlled(object) => &object.name,
            Self::Host(host) => host,
        }
    }
}

/// The stores that `Edges::of` reads to find what depends on a workload.
#[derive(Clone, Copy)]
pub struct Edges<'r> {
    pub graph: &'r Graph,
    /// The `Ingress` objects.  Their rules give the hosts, which the
    /// routes-to edges do not record.
    pub ingresses: &'r [Arc<Ingress>],
    /// The intents, which record what each lost workload provides.
    pub intents: &'r Intents,
}

impl Edges<'_> {
    /// What depends on `workload` and loses it when it goes down: its
    /// callers, then the applications that sign in through it, then the
    /// hosts that reach it, then the custom resources it controls.  Each
    /// group is sorted by name.
    pub fn of(&self, workload: &Object) -> Vec<Loss> {
        let end = Endpoint::Object(workload.clone());
        let objects = |kind: Kind| -> Vec<Object> {
            let ends = self.graph.reaching(kind, &end).into_iter();
            let mut found: Vec<Object> = ends.filter_map(object).cloned().collect();
            found.sort();
            found
        };
        let callers = objects(Kind::DependsOn).into_iter().map(Dependent::Caller);
        let signing = objects(Kind::AuthenticatesThrough).into_iter();
        let signing = signing.map(Dependent::SignsIn);
        let hosts = self.hosts(&end).into_iter().map(Dependent::Host);
        let controlled = objects(Kind::ControlledBy).into_iter();
        let controlled = controlled.map(Dependent::Controlled);
        let dependents = callers.chain(signing).chain(hosts).chain(controlled);
        let provides = self.provides(workload);
        dependents
            .map(|dependent| Loss {
                declared_in: self.declared_in(&dependent, &end),
                dependent,
                lost: workload.clone(),
                provides: provides.clone(),
            })
            .collect()
    }

    /// Where the edge from `dependent` to `end` was written, when every
    /// such edge comes from intent.  The dependent's line then shows the
    /// provenance mark.
    fn declared_in(&self, dependent: &Dependent, end: &Endpoint) -> Option<Provenance> {
        let (kind, from) = match dependent {
            Dependent::Caller(object) => (Kind::DependsOn, object),
            Dependent::SignsIn(object) => (Kind::AuthenticatesThrough, object),
            Dependent::Host(_) | Dependent::Controlled(_) => return None,
        };
        let from = Endpoint::Object(from.clone());
        let edges = self.graph.edges_reaching(kind, end);
        let edges: Vec<_> = edges.filter(|edge| edge.from == from).collect();
        let intent =
            |edge: &&transect_graph::edges::Edge| matches!(edge.provenance, Provenance::Intent(_));
        match edges.iter().all(intent) {
            true => edges.first().map(|edge| edge.provenance.clone()),
            false => None,
        }
    }

    /// What `workload` provides, according to its intent.
    pub fn provides(&self, workload: &Object) -> Option<Line> {
        self.intents.of(workload)?.provides.clone()
    }

    /// The hosts of the `Ingress` rules that route to a `Service` that
    /// selects `end`, each once, sorted.  A default backend has no host
    /// of its own, so it adds none.
    fn hosts(&self, end: &Endpoint) -> Vec<String> {
        let mut hosts: Vec<String> = Vec::new();
        for service in self.graph.reaching(Kind::Selects, end) {
            let Some(service) = object(service) else {
                continue;
            };
            for ingress in self
                .graph
                .reaching(Kind::RoutesTo, &Endpoint::Object(service.clone()))
            {
                let Some(ingress) = object(ingress).and_then(|o| self.ingress(o)) else {
                    continue;
                };
                hosts.extend(routed(ingress, &service.name));
            }
        }
        hosts.sort_by(|a, b| transect_graph::dns::order(a, b));
        hosts.dedup();
        hosts
    }

    fn ingress(&self, object: &Object) -> Option<&Ingress> {
        let mut ingresses = self.ingresses.iter().map(AsRef::as_ref);
        ingresses.find(|ingress| {
            ingress.namespace() == object.namespace && ingress.name_any() == object.name
        })
    }
}

fn object(end: &Endpoint) -> Option<&Object> {
    match end {
        Endpoint::Object(object) => Some(object),
        _ => None,
    }
}

/// The hosts of `ingress`'s rules that have a path with `service` as its
/// backend.
fn routed<'a>(ingress: &'a Ingress, service: &'a str) -> impl Iterator<Item = String> + 'a {
    let rules = ingress
        .spec
        .iter()
        .flat_map(|spec| spec.rules.iter().flatten());
    rules.filter_map(move |rule| {
        let paths = rule.http.iter().flat_map(|http| &http.paths);
        let mut backends = paths.filter_map(|path| path.backend.service.as_ref());
        let routes = backends.any(|backend| backend.name == service);
        routes.then(|| rule.host.clone()).flatten()
    })
}

/// One dependent and every down workload it loses, which the page shows
/// as one line.
#[derive(Clone, Debug, PartialEq)]
pub struct Hop {
    /// The namespace that the line appears under.  It is the
    /// dependent's own namespace, or for a host, the lost workload's,
    /// because the `Ingress` of the host is in that namespace.
    pub namespace: Option<String>,
    pub dependent: Dependent,
    pub lost: Vec<Object>,
    /// What each lost workload provides, for each one that has intent.
    pub provides: BTreeMap<Object, Line>,
    /// Where each edge that only intent records was written.
    pub declared_in: Vec<Provenance>,
}

impl Hop {
    /// The objects that the line shows: the dependent and the lost
    /// workloads.
    pub fn objects(&self) -> impl Iterator<Item = &Object> {
        self.dependent.object().into_iter().chain(&self.lost)
    }

    /// Where the intent behind the line was written, each place once,
    /// for the provenance marks after the line.  That intent is each edge
    /// that only intent records, and on a caller's line, the `provides`
    /// text that the line shows in place of a name.
    pub fn provenances(&self) -> Vec<&Provenance> {
        let provides = match self.dependent {
            Dependent::Caller(_) => self.provides.values().collect(),
            _ => Vec::new(),
        };
        let provides = provides.into_iter().map(|line| &line.provenance);
        let mut found: Vec<&Provenance> = Vec::new();
        for provenance in self.declared_in.iter().chain(provides) {
            if !found.contains(&provenance) {
                found.push(provenance);
            }
        }
        found
    }
}

/// Groups `losses` by dependent, sorted by namespace and then by
/// `Dependent::order`.  Each hop lists its lost workloads by name.
pub fn hops(losses: impl IntoIterator<Item = Loss>) -> Vec<Hop> {
    type Joined = (Vec<Object>, BTreeMap<Object, Line>, Vec<Provenance>);
    let mut by: BTreeMap<(Option<String>, Dependent), Joined> = BTreeMap::new();
    for Loss {
        dependent,
        lost,
        provides,
        declared_in,
    } in losses
    {
        let namespace = match &dependent {
            Dependent::Caller(object)
            | Dependent::SignsIn(object)
            | Dependent::Controlled(object) => object.namespace.clone(),
            Dependent::Host(_) => lost.namespace.clone(),
        };
        let (found, provided, written) = by.entry((namespace, dependent)).or_default();
        if let Some(line) = provides {
            provided.insert(lost.clone(), line);
        }
        if let Some(provenance) = declared_in
            && !written.contains(&provenance)
        {
            written.push(provenance);
        }
        if !found.contains(&lost) {
            found.push(lost);
        }
    }
    let mut found: Vec<Hop> = by
        .into_iter()
        .map(
            |((namespace, dependent), (mut lost, provides, declared_in))| {
                lost.sort_by(|a, b| (&a.name, a).cmp(&(&b.name, b)));
                Hop {
                    namespace,
                    dependent,
                    lost,
                    provides,
                    declared_in,
                }
            },
        )
        .collect();
    found.sort_by(|a, b| {
        (&a.namespace, a.dependent.order()).cmp(&(&b.namespace, b.dependent.order()))
    });
    found
}

/// What the dependents of one workload lose if it goes down, for the
/// card on the workload's page.
#[derive(Debug, Default, PartialEq)]
pub struct IfDown {
    /// The workload's namespace, which the lines leave out.
    pub namespace: Option<String>,
    pub hops: Vec<Hop>,
    /// The links to the pages of the objects that the lines name.
    pub linker: Linker,
    /// The plural of each custom kind that the workload controls, which
    /// a line uses when it counts the objects of that kind.
    pub plurals: transect_graph::edges::Plurals,
}

impl IfDown {
    pub fn of(edges: &Edges, workload: &Object) -> Self {
        Self {
            namespace: workload.namespace.clone(),
            hops: hops(edges.of(workload)),
            linker: Linker::default(),
            plurals: Default::default(),
        }
    }

    pub fn with_plurals(self, plurals: &transect_graph::edges::Plurals) -> Self {
        Self {
            plurals: plurals.clone(),
            ..self
        }
    }

    /// `self` with a link on each dependent in the lines, to the page
    /// that `href` returns for it.  The workload whose card this is gets
    /// no link, because the card is on its own page, and neither does
    /// its name inside `provides` text from intent.
    pub fn linked(self, href: impl Fn(&Object) -> Option<String>) -> Self {
        let objects = self
            .hops
            .iter()
            .filter_map(|hop| hop.dependent.object())
            .cloned();
        Self {
            linker: Linker::new(objects.collect::<Vec<_>>(), href),
            ..self
        }
    }
}

#[cfg(test)]
mod tests;
