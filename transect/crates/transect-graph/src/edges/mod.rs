//! The edge graph: how the cluster's parts relate, as edges between
//! objects, machines, and boxes outside the cluster.
//!
//! Each edge kind comes from its own domain module, which turns the
//! cluster's stores into edges, and the graph is built from those once
//! per change of the cluster and shared by every view.  Building it
//! fresh keeps every source a pure function; the graph becomes
//! incremental only if a build gets slow.  Views query the graph, for
//! example for the edges that touch a namespace, and never walk the
//! sources themselves.

mod claim;
mod device;
#[cfg(any(test, feature = "fixtures"))]
pub mod fixtures;
mod index;
mod loops;
pub mod provenance;
mod shown;

use std::collections::HashSet;
use std::sync::Arc;

use index::Index;

pub use claim::Claim;
pub use device::Device;
pub use loops::Loop;
#[cfg(any(test, feature = "fixtures"))]
pub use loops::fixtures as loop_fixtures;
pub use provenance::{Provenance, Stated, Way};
pub use shown::{Shown, TOGGLED};

#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash, PartialOrd, Ord)]
pub enum Kind {
    /// A pod runs on a machine, from its `spec.nodeName`.
    RunsOn,
    /// An object is managed by its top owner, found by walking its
    /// owner references up until an owner has none.
    ManagedBy,
    /// A workload stores on where its claims' volumes keep their data:
    /// a box in the ground band, or the machine a local volume is on.
    StoresOn,
    /// A `Service` selects a workload: its selector matches the labels of
    /// the workload's pods, or of its pod template when none runs.
    Selects,
    /// An `Ingress` routes to a `Service` that one of its backends names.
    RoutesTo,
    /// A workload depends on what an address it is configured with
    /// resolves to: the workload behind a `Service`, or a machine.
    DependsOn,
    /// An object reconciles from what a GitOps tool applied it from: the
    /// applier whose marks it has, as kind knowledge's `applies`
    /// names them, and each applier from its source.
    ReconcilesFrom,
    /// A custom resource is managed by its controller's workload: one
    /// whose `ServiceAccount` may write the kind's status under RBAC,
    /// narrowed by the field manager that writes it.  It is its own
    /// kind, apart from `ManagedBy`, because walks that read owner
    /// references to find what a workload owns must not follow it into
    /// the operator.
    ControlledBy,
    /// A workload is allowed to call another: the target's ingress
    /// NetworkPolicies and the caller's egress ones both admit it.  Only
    /// a target that policy isolates has these edges, because any
    /// workload may call a target that policy leaves open.
    AllowedToCall,
    /// A workload holds a device that Dynamic Resource Allocation gave
    /// one of its pods: the claim's allocation names the device, and the
    /// `ResourceSlice` that publishes it names its machine.
    Holds,
    /// An application signs its users in through an identity provider,
    /// as intent's `authenticates-through` states.  It is its own kind,
    /// apart from `DependsOn`, because the loss of the provider stops
    /// sign-in and leaves the rest of the application running.
    AuthenticatesThrough,
    /// An object is part of another, as kind knowledge declares: a
    /// camera of an optical train, a train of a telescope.  An object's
    /// board draws it as nesting.  Loops do not count it and a cascade
    /// does not follow it, because a part keeps working when the record
    /// of what it is part of is deleted.
    PartOf,
}

impl Kind {
    /// What an edge of the kind means, in one line, for the edge row.
    pub fn meaning(self) -> &'static str {
        match self {
            Self::RunsOn => "A pod runs on a machine.",
            Self::DependsOn => "A workload calls what an address it is configured with reaches.",
            Self::AuthenticatesThrough => "An application signs its users in through a provider.",
            Self::StoresOn => "A workload keeps its data where its volumes are.",
            Self::Holds => "A workload's pods hold a device that a ResourceClaim gave them.",
            Self::ReconcilesFrom => "A GitOps tool applies an object from a source.",
            Self::ControlledBy => "An operator's controller reconciles a custom resource.",
            Self::ManagedBy => "An object belongs to the workload that owns it.",
            Self::AllowedToCall => "NetworkPolicies let one workload call another.",
            Self::Selects => "A Service sends its traffic to a workload's pods.",
            Self::RoutesTo => "An Ingress sends requests to a Service.",
            Self::PartOf => "An object is part of a larger one, as its kind knowledge declares.",
        }
    }

    /// The kind in the words its tags, card lines, and edge row use.
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
        }
    }

    /// The kind in words from the far end, for a list of the objects
    /// an edge of it leaves: an `Ingress`'s page lists under "routes to
    /// it" what routes to it, not what it routes to.
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
        }
    }
}

/// An object in the cluster, by API group, kind, namespace, and name.
/// The group distinguishes two kinds of one name, such as two operators'
/// `Cluster`; the core group is empty.  A cluster-scoped object has no
/// namespace.
#[derive(Clone, Debug, PartialEq, Eq, Hash, PartialOrd, Ord)]
pub struct Object {
    pub group: String,
    pub kind: String,
    pub namespace: Option<String>,
    pub name: String,
}

/// The plural of each custom kind, by group and kind, as discovery
/// gives it.
pub type Plurals = std::collections::HashMap<(String, String), String>;

/// The kinds that are not namespaced, by group and kind, as discovery
/// gives them, so a name that refers to one has no namespace.
pub type ClusterScoped = std::collections::HashSet<(String, String)>;

/// Either end of an edge.
#[derive(Clone, Debug, PartialEq, Eq, Hash, PartialOrd, Ord)]
pub enum Endpoint {
    Object(Object),
    /// A machine, by its `Node`'s name.
    Machine(String),
    /// A box in the ground band, outside the cluster, such as a storage
    /// server, by the address or driver that names it.
    Outside(String),
    /// A device that a driver publishes in a `ResourceSlice`.
    Device(Device),
}

/// A kind and a name as a reader says them: "Deployment web".  A name
/// that only repeats its kind, as a `Catalog` named `catalog` does, is
/// written once, as the kind.
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
    /// Whether the edge is inferred rather than read from the API, so a
    /// view can draw it as less certain.  A guess is its own flag, apart
    /// from `provenance`, because the API and a custom kind's fields can
    /// both give one.
    pub guessed: bool,
    /// Which layer of `transect`'s knowledge the edge came from.
    pub provenance: Provenance,
}

/// The edges, shared by every view that asks about them, and the kinds
/// this view turns on.  A view's graph is a copy of the pointer with its
/// own kinds, so one graph built per change serves every view.
#[derive(Clone, Debug, Default)]
pub struct Graph {
    edges: Arc<Edges>,
    shown: Shown,
}

#[derive(Clone, Debug, Default)]
struct Edges {
    list: Vec<Edge>,
    /// The edges by the end they leave.
    from: Index,
    /// The edges by the end they reach.
    to: Index,
}

impl Graph {
    /// The graph of every edge each source gives.
    pub fn new(sources: impl IntoIterator<Item = Vec<Edge>>) -> Self {
        let mut graph = Self::default();
        for edges in sources {
            graph.add(edges);
        }
        graph
    }

    /// Adds the edges of a source that reads the graph built so far, such
    /// as depends-on, which follows a `Service`'s selects edges to the
    /// workload behind it.  Only a graph no view shares yet is added to,
    /// so this copies nothing.
    pub fn add(&mut self, edges: Vec<Edge>) {
        let all = Arc::make_mut(&mut self.edges);
        for edge in edges {
            let at = all.list.len();
            all.from.add(edge.kind, &edge.from, at);
            all.to.add(edge.kind, &edge.to, at);
            all.list.push(edge);
        }
    }

    /// The edges of the kinds this view shows, in the order the sources
    /// gave them.
    fn iter(&self) -> impl Iterator<Item = &Edge> {
        let shown = &self.shown;
        self.edges.list.iter().filter(|edge| shown.is_on(edge.kind))
    }

    /// The edges with an object in `namespace` at either end.
    pub fn touching_namespace<'a>(&'a self, namespace: &'a str) -> impl Iterator<Item = &'a Edge> {
        self.iter().filter(move |edge| {
            edge.from.in_namespace(namespace) || edge.to.in_namespace(namespace)
        })
    }

    /// The same edges with only the kinds `shown` turns on, so each view
    /// queries only the edges of the kinds that its reader turned on.
    pub fn only(&self, shown: &Shown) -> Self {
        Self {
            edges: self.edges.clone(),
            shown: shown.clone(),
        }
    }

    /// Every `kind` edge, in the order the sources gave them, for a
    /// view that reads whether each is a guess.
    pub fn of_kind(&self, kind: Kind) -> impl Iterator<Item = &Edge> {
        self.iter().filter(move |edge| edge.kind == kind)
    }

    /// The far ends of the `kind` edges that arrive at `to`, each once,
    /// in the order the sources gave them.
    pub fn reaching<'a>(&'a self, kind: Kind, to: &Endpoint) -> Vec<&'a Endpoint> {
        let edges = self.at(&self.edges.to, kind, to);
        once_each(edges.filter(|edge| &edge.to == to).map(|edge| &edge.from))
    }

    /// The far ends of the `kind` edges that leave `from`, each once, in
    /// the order the sources gave them.
    pub fn leaving<'a>(&'a self, kind: Kind, from: &Endpoint) -> Vec<&'a Endpoint> {
        let edges = self.at(&self.edges.from, kind, from);
        once_each(edges.filter(|edge| &edge.from == from).map(|edge| &edge.to))
    }

    /// The `kind` edges that leave `from`, in the order the sources
    /// gave them, for a view that reads where each came from.
    pub fn edges_leaving<'a>(
        &'a self,
        kind: Kind,
        from: &'a Endpoint,
    ) -> impl Iterator<Item = &'a Edge> {
        let edges = self.at(&self.edges.from, kind, from);
        edges.filter(move |edge| &edge.from == from)
    }

    /// The `kind` edges that arrive at `to`, in the order the sources
    /// gave them.
    pub fn edges_reaching<'a>(
        &'a self,
        kind: Kind,
        to: &'a Endpoint,
    ) -> impl Iterator<Item = &'a Edge> {
        let edges = self.at(&self.edges.to, kind, to);
        edges.filter(move |edge| &edge.to == to)
    }

    /// The edges of every kind this view shows that arrive at `to`, in
    /// the order the sources gave them, for a view of everything that
    /// reaches one end.
    pub fn every_edge_reaching<'a>(&'a self, to: &'a Endpoint) -> impl Iterator<Item = &'a Edge> {
        self.iter().filter(move |edge| &edge.to == to)
    }

    /// The `kind` edges `index` may hold at `end`, none when the view
    /// turns `kind` off.
    fn at<'a>(
        &'a self,
        index: &'a Index,
        kind: Kind,
        end: &Endpoint,
    ) -> impl Iterator<Item = &'a Edge> {
        let found = if self.shown.is_on(kind) {
            index.candidates(kind, end)
        } else {
            &[]
        };
        found.iter().map(|&at| &self.edges.list[at])
    }
}

/// Each of `ends` once, in order.  A machine has an edge from each pod
/// on it, so the check uses a set, not a search of the ends found so far.
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
    fn says_a_kind_and_name(#[case] kind: &str, #[case] name: &str, #[case] said: &str) {
        assert_eq!(kind_and_name(kind, name), said);
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

    #[rstest]
    #[case::on(None, 1)]
    #[case::off(Some(""), 0)]
    fn a_kind_turned_off_answers_nothing(#[case] edges: Option<&str>, #[case] count: usize) {
        let all = Graph::new([vec![edge(
            Kind::DependsOn,
            object("Deployment", "web"),
            object("Deployment", "db"),
        )]]);
        let graph = all.only(&Shown::parse(edges));
        assert_eq!(
            graph
                .leaving(Kind::DependsOn, &object("Deployment", "web"))
                .len(),
            count
        );
        assert_eq!(
            graph
                .reaching(Kind::DependsOn, &object("Deployment", "db"))
                .len(),
            count
        );
        assert_eq!(graph.of_kind(Kind::DependsOn).count(), count);
    }
}
