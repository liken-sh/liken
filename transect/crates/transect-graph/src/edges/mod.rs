//! The edge graph: typed edges between objects, machines, devices, and
//! systems outside the cluster.
//!
//! Each edge kind comes from the module of the domain it describes,
//! which turns the cluster's stores into edges.  The graph is built
//! again from all of them on each change of the cluster, and every view
//! shares it.  Building it from scratch keeps every source a pure
//! function of the stores.  Views query the graph and never read the
//! sources.

mod claim;
mod device;
#[cfg(any(test, feature = "fixtures"))]
pub mod fixtures;
mod index;
mod loops;
pub mod provenance;

use std::collections::HashSet;
use std::sync::Arc;

use index::Index;

pub use claim::Claim;
pub use device::Device;
pub use loops::Loop;
#[cfg(any(test, feature = "fixtures"))]
pub use loops::fixtures as loop_fixtures;
pub use provenance::{Provenance, Stated, Way};

#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash, PartialOrd, Ord)]
pub enum Kind {
    /// A pod runs on a machine, from its `spec.nodeName`.
    RunsOn,
    /// An object is managed by its top owner, which is found by
    /// following owner references up until an owner has none.
    ManagedBy,
    /// A workload stores data on what backs its claims' volumes: a
    /// storage system outside the cluster, or the machine of a local
    /// volume.
    StoresOn,
    /// A `Service` selects a workload: its selector matches the labels
    /// of the workload's pods, or of its pod templates when it has no
    /// pods.
    Selects,
    /// An `Ingress` routes to a `Service` that one of its backends
    /// names.
    RoutesTo,
    /// A workload depends on what an address in its configuration
    /// resolves to: the workload behind a `Service`, or a machine.
    DependsOn,
    /// An object is reconciled from the source of the GitOps object
    /// that applies it.  The `applies` field of a kind's knowledge says
    /// how to find which object of that kind applied a given object.
    ReconcilesFrom,
    /// A custom resource is controlled by the workload that reconciles
    /// it.  The API does not record this, so it is inferred from RBAC,
    /// the status field managers, and the controller's image.  It is not
    /// `ManagedBy`, because code that follows owner references to find
    /// what a workload owns must not follow this edge into the operator.
    ControlledBy,
    /// A workload is allowed to call another: the target's ingress
    /// `NetworkPolicy` rules and the caller's egress rules both admit
    /// the call.  Only a target that some policy isolates for ingress
    /// has these edges, because any workload may call any other target.
    AllowedToCall,
    /// A workload's pods hold a device that Dynamic Resource Allocation
    /// allocated to their claim.
    Holds,
    /// An application signs its users in through an identity provider,
    /// as intent's `authenticates-through` field declares.  It is not
    /// `DependsOn`, because when the provider is down the application
    /// keeps running and only sign-in fails.
    AuthenticatesThrough,
    /// An object is part of another, as kind knowledge declares, such as
    /// a camera that is part of an optical train.  An object's page draws
    /// its parts nested inside it.  Loops and a cascade ignore this edge,
    /// because a part keeps working when the larger object is deleted.
    PartOf,
    /// A pod template mounts a `ServiceAccount` token for an audience
    /// that a program accepts, so the pods made from it call the
    /// workload that runs the program.  The edge leaves the object that
    /// holds the template, because the pods of an agent's turn exist
    /// only while the turn runs.  A program's knowledge states the
    /// audiences that it accepts.
    Presents,
}

impl Kind {
    /// The kind as a phrase, for tags and card lines.
    pub fn words(self) -> &'static str {
        match self {
            Self::RunsOn => "runs on",
            Self::DependsOn => "depends on",
            Self::AuthenticatesThrough => "authenticates through",
            Self::StoresOn => "stores on",
            Self::Holds => "holds",
            Self::ReconcilesFrom => "reconciles from",
            Self::ControlledBy => "controlled by",
            Self::ManagedBy => "managed by",
            Self::AllowedToCall => "allowed to call",
            Self::Selects => "selects",
            Self::RoutesTo => "routes to",
            Self::PartOf => "part of",
            Self::Presents => "presents a token to",
        }
    }

    /// The kind as a phrase from the `to` end, for a list of the objects
    /// at the `from` end of its edges.  A `Service`'s page lists under
    /// "routes to it" the `Ingress` objects that route to it.
    pub fn toward(self) -> &'static str {
        match self {
            Self::RunsOn => "runs on it",
            Self::DependsOn => "depends on it",
            Self::AuthenticatesThrough => "authenticates through it",
            Self::StoresOn => "stores on it",
            Self::Holds => "holds it",
            Self::ReconcilesFrom => "reconciles from it",
            Self::ControlledBy => "controlled by it",
            Self::ManagedBy => "managed by it",
            Self::AllowedToCall => "allowed to call it",
            Self::Selects => "selects it",
            Self::RoutesTo => "routes to it",
            Self::PartOf => "its parts",
            Self::Presents => "its callers",
        }
    }
}

/// An object in the cluster, identified by API group, kind, namespace,
/// and name.  The group tells apart two kinds with one name, such as the
/// `Cluster` kinds of two operators.  The core group is empty.  A
/// cluster-scoped object has no namespace.
#[derive(Clone, Debug, PartialEq, Eq, Hash, PartialOrd, Ord)]
pub struct Object {
    pub group: String,
    pub kind: String,
    pub namespace: Option<String>,
    pub name: String,
}

/// The plural of each custom kind, keyed by group and kind, as API
/// discovery returns it.
pub type Plurals = std::collections::HashMap<(String, String), String>;

/// The kinds that are not namespaced, by group and kind, as API
/// discovery returns them.  A reference to an object of one of these
/// kinds has no namespace.
pub type ClusterScoped = std::collections::HashSet<(String, String)>;

/// An end of an edge: the `from` end or the `to` end.
#[derive(Clone, Debug, PartialEq, Eq, Hash, PartialOrd, Ord)]
pub enum Endpoint {
    Object(Object),
    /// A machine, identified by its `Node`'s name.
    Machine(String),
    /// A system outside the cluster, such as a storage server,
    /// identified by its address or by the driver that reaches it.  The
    /// map draws it as a box in its outside-systems band.
    Outside(String),
    /// A device that a driver publishes in a `ResourceSlice`.
    Device(Device),
}

/// A kind and a name written as a reader says them, such as
/// "Deployment web".  When the name only repeats the kind, as a
/// `Catalog` named `catalog` does, the result is the kind alone.
pub fn kind_and_name(kind: &str, name: &str) -> String {
    if name.eq_ignore_ascii_case(kind) {
        kind.to_string()
    } else {
        format!("{kind} {name}")
    }
}

impl Object {
    pub fn new(group: &str, kind: &str, namespace: Option<&str>, name: &str) -> Self {
        Self {
            group: group.into(),
            kind: kind.into(),
            namespace: namespace.map(Into::into),
            name: name.into(),
        }
    }
}

impl Endpoint {
    /// Whether this end is an object in `namespace`.
    pub fn in_namespace(&self, namespace: &str) -> bool {
        matches!(self, Self::Object(object) if object.namespace.as_deref() == Some(namespace))
    }
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Edge {
    pub kind: Kind,
    pub from: Endpoint,
    pub to: Endpoint,
    /// Whether the edge is a guess, so a view can draw it as less
    /// certain.  It is separate from `provenance`, because an edge from
    /// any source can be a guess, such as a controller chosen from
    /// several candidates.
    pub guessed: bool,
    /// Where `transect` read the edge from.
    pub provenance: Provenance,
}

/// The edges of one build of the graph.  A clone shares the edges
/// without copying them.
#[derive(Clone, Debug, Default)]
pub struct Graph {
    edges: Arc<Edges>,
}

#[derive(Clone, Debug, Default)]
struct Edges {
    list: Vec<Edge>,
    /// The edges indexed by their `from` end.
    from: Index,
    /// The edges indexed by their `to` end.
    to: Index,
}

impl Graph {
    /// The graph of the edges from every source.
    pub fn new(sources: impl IntoIterator<Item = Vec<Edge>>) -> Self {
        let mut graph = Self::default();
        for edges in sources {
            graph.add(edges);
        }
        graph
    }

    /// Adds the edges of a source that reads the graph built so far,
    /// such as the depends-on source, which follows a `Service`'s
    /// `Selects` edges to the workload behind it.  Call it before any
    /// view clones the graph, or `Arc::make_mut` copies every edge.
    pub fn add(&mut self, edges: Vec<Edge>) {
        let all = Arc::make_mut(&mut self.edges);
        for edge in edges {
            let at = all.list.len();
            all.from.add(edge.kind, &edge.from, at);
            all.to.add(edge.kind, &edge.to, at);
            all.list.push(edge);
        }
    }

    /// Every edge, in the order the sources returned them.
    fn iter(&self) -> impl Iterator<Item = &Edge> {
        self.edges.list.iter()
    }

    /// The edges with an object in `namespace` at either end.
    pub fn touching_namespace<'a>(&'a self, namespace: &'a str) -> impl Iterator<Item = &'a Edge> {
        self.iter().filter(move |edge| {
            edge.from.in_namespace(namespace) || edge.to.in_namespace(namespace)
        })
    }

    /// Every `kind` edge, in the order the sources returned them.
    pub fn of_kind(&self, kind: Kind) -> impl Iterator<Item = &Edge> {
        self.iter().filter(move |edge| edge.kind == kind)
    }

    /// The `from` ends of the `kind` edges that arrive at `to`, without
    /// duplicates, in the order the sources returned them.
    pub fn reaching<'a>(&'a self, kind: Kind, to: &Endpoint) -> Vec<&'a Endpoint> {
        let edges = self.at(&self.edges.to, kind, to);
        once_each(edges.filter(|edge| &edge.to == to).map(|edge| &edge.from))
    }

    /// The `to` ends of the `kind` edges that leave `from`, without
    /// duplicates, in the order the sources returned them.
    pub fn leaving<'a>(&'a self, kind: Kind, from: &Endpoint) -> Vec<&'a Endpoint> {
        let edges = self.at(&self.edges.from, kind, from);
        once_each(edges.filter(|edge| &edge.from == from).map(|edge| &edge.to))
    }

    /// The `kind` edges that leave `from`, in the order the sources
    /// returned them.
    pub fn edges_leaving<'a>(
        &'a self,
        kind: Kind,
        from: &'a Endpoint,
    ) -> impl Iterator<Item = &'a Edge> {
        let edges = self.at(&self.edges.from, kind, from);
        edges.filter(move |edge| &edge.from == from)
    }

    /// The `kind` edges that arrive at `to`, in the order the sources
    /// returned them.
    pub fn edges_reaching<'a>(
        &'a self,
        kind: Kind,
        to: &'a Endpoint,
    ) -> impl Iterator<Item = &'a Edge> {
        let edges = self.at(&self.edges.to, kind, to);
        edges.filter(move |edge| &edge.to == to)
    }

    /// The edges of every kind that arrive at `to`, in the order the sources returned them.
    pub fn every_edge_reaching<'a>(&'a self, to: &'a Endpoint) -> impl Iterator<Item = &'a Edge> {
        self.iter().filter(move |edge| &edge.to == to)
    }

    /// The edges of `kind` that `index` lists for `end`.  The index is
    /// keyed by a hash of the end, so the caller must still check each
    /// edge's end.
    fn at<'a>(
        &'a self,
        index: &'a Index,
        kind: Kind,
        end: &Endpoint,
    ) -> impl Iterator<Item = &'a Edge> {
        let found = index.candidates(kind, end);
        found.iter().map(|&at| &self.edges.list[at])
    }
}

/// `ends` without duplicates, in order.  It tracks the ends seen in a
/// set, because a machine has an edge from every pod on it, and a search
/// of the ends found so far would be quadratic in that count.
fn once_each<'a>(ends: impl Iterator<Item = &'a Endpoint>) -> Vec<&'a Endpoint> {
    let mut seen = HashSet::new();
    ends.filter(|end| seen.insert(*end)).collect()
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::fixtures::*;
    use super::*;

    #[rstest]
    #[case::different("Deployment", "web", "Deployment web")]
    #[case::the_name_repeats_the_kind("Catalog", "catalog", "Catalog")]
    fn joins_a_kind_and_name(#[case] kind: &str, #[case] name: &str, #[case] joined: &str) {
        assert_eq!(kind_and_name(kind, name), joined);
    }

    fn pair(from: Endpoint, to: Endpoint) -> Edge {
        Edge {
            kind: Kind::RunsOn,
            from,
            to,
            guessed: false,
            provenance: Provenance::Kubernetes,
        }
    }

    #[rstest]
    #[case::from_the_namespace(pair(pod("shop", "web"), Endpoint::Machine("alpha".into())), true)]
    #[case::to_the_namespace(pair(Endpoint::Machine("alpha".into()), pod("shop", "web")), true)]
    #[case::another_namespace(pair(pod("blog", "web"), Endpoint::Machine("alpha".into())), false)]
    #[case::a_cluster_scoped_object(
        pair(
            Endpoint::Object(Object::new("", "Node", None, "shop")),
            Endpoint::Machine("shop".into()),
        ),
        false
    )]
    fn finds_the_edges_touching_a_namespace(#[case] edge: Edge, #[case] touches: bool) {
        let graph = Graph::new([vec![edge.clone()]]);
        let found: Vec<&Edge> = graph.touching_namespace("shop").collect();
        let expected: Vec<&Edge> = if touches { vec![&edge] } else { vec![] };
        assert_eq!(found, expected);
    }

    #[rstest]
    fn joins_every_source() {
        let graph = Graph::new([
            vec![runs_on("shop", "web-1", "alpha")],
            vec![runs_on("shop", "web-2", "beta")],
        ]);
        assert_eq!(graph.touching_namespace("shop").count(), 2);
    }

    fn object(kind: &str, name: &str) -> Endpoint {
        Endpoint::Object(Object::new("", kind, Some("shop"), name))
    }

    fn edge(kind: Kind, from: Endpoint, to: Endpoint) -> Edge {
        Edge {
            kind,
            from,
            to,
            guessed: false,
            provenance: Provenance::Kubernetes,
        }
    }

    #[rstest]
    #[case::one(vec![edge(Kind::Selects, object("Service", "web"), pod("shop", "web"))], vec![object("Service", "web")])]
    #[case::each_once(
        vec![
            edge(Kind::Selects, object("Service", "web"), pod("shop", "web")),
            edge(Kind::Selects, object("Service", "web"), pod("shop", "web")),
        ],
        vec![object("Service", "web")]
    )]
    #[case::another_kind(vec![edge(Kind::RoutesTo, object("Ingress", "web"), pod("shop", "web"))], vec![])]
    #[case::another_end(vec![edge(Kind::Selects, object("Service", "web"), pod("shop", "api"))], vec![])]
    fn finds_what_reaches_an_end(#[case] edges: Vec<Edge>, #[case] expected: Vec<Endpoint>) {
        let graph = Graph::new([edges]);
        let found = graph.reaching(Kind::Selects, &pod("shop", "web"));
        assert_eq!(found, expected.iter().collect::<Vec<_>>());
    }

    #[rstest]
    #[case::one(vec![edge(Kind::Selects, object("Service", "web"), pod("shop", "web"))], vec![pod("shop", "web")])]
    #[case::each_once(
        vec![
            edge(Kind::Selects, object("Service", "web"), pod("shop", "web")),
            edge(Kind::Selects, object("Service", "web"), pod("shop", "web")),
        ],
        vec![pod("shop", "web")]
    )]
    #[case::another_kind(vec![edge(Kind::RoutesTo, object("Service", "web"), pod("shop", "web"))], vec![])]
    #[case::another_end(vec![edge(Kind::Selects, object("Service", "api"), pod("shop", "web"))], vec![])]
    fn finds_what_leaves_an_end(#[case] edges: Vec<Edge>, #[case] expected: Vec<Endpoint>) {
        let graph = Graph::new([edges]);
        let found = graph.leaving(Kind::Selects, &object("Service", "web"));
        assert_eq!(found, expected.iter().collect::<Vec<_>>());
    }
}
