//! The loss: what the cluster loses if a set of machines goes down.
//! transect computes it from the machines where each workload's pods
//! run now.
//!
//! One rule computes both the prediction and the recovery.  Under a
//! loss, a workload's state is the number of its ready pods on machines
//! outside the lost set, compared with the replicas it declares.  On a
//! healthy day the pods on a lost machine are ready, and the rule does
//! not count them.  When a machine is really down, its pods are already
//! not ready, and the rule counts each replacement that starts on
//! another machine when it becomes ready.  So transect needs no second
//! model of what Kubernetes will do.
//!
//! `assess` returns the loss as data, and `sentence.rs` contains every
//! word that the sidebar and the map show for it.  The reason a down
//! workload waits, the cascade one hop out, and what happens next each
//! add to the same `Loss` from the stores that they read, so each one
//! is a pure function with its own tests.

pub mod blind;
pub mod cascade;
mod next;
mod real;
mod rule;
mod sentence;
mod sidebar;
mod stranded;
#[cfg(test)]
mod tests;
mod unknown;
mod vital;

use std::collections::{BTreeSet, HashMap};
use std::sync::Arc;

use k8s_openapi::api::core::v1::Pod;

pub use cascade::IfDown;
use next::Gather;
pub use next::Next;
pub use real::{Counted, Gone, Majority, not_ready};
pub use rule::State;
use rule::{Role, role};
pub use sidebar::Sidebar;
pub use stranded::{Stranded, Stranding};
use transect_cluster::ownership::Ownership;
use transect_cluster::workloads::Workloads;
use transect_graph::edges::Object;
use transect_graph::pods::finished;
use transect_observe::health::Level;
use transect_resources::intent::Line;
pub use unknown::{text as unknown_text, unknown};
pub use vital::{Vital, vital};

/// The machines a loss takes, each once, in name order.
#[derive(Clone, Debug, Default, PartialEq, Eq, Hash)]
pub struct Lost(BTreeSet<String>);

impl Lost {
    /// The machines named in `names` that are also in `machines`.  The
    /// function leaves out a name that the cluster does not have, such
    /// as a machine removed after someone copied a link, in the same way
    /// that the page leaves out a selected namespace that was deleted.
    pub fn of<'a>(
        names: impl IntoIterator<Item = &'a str>,
        machines: impl IntoIterator<Item = &'a str>,
    ) -> Self {
        let known: BTreeSet<&str> = machines.into_iter().collect();
        let names = names.into_iter().filter(|name| known.contains(name));
        Self(names.map(str::to_owned).collect())
    }

    pub fn is_empty(&self) -> bool {
        self.0.is_empty()
    }

    pub fn contains(&self, machine: &str) -> bool {
        self.0.contains(machine)
    }

    pub fn names(&self) -> impl Iterator<Item = &str> {
        self.0.iter().map(String::as_str)
    }

    /// Whether `pod` runs on one of the lost machines.
    fn holds(&self, pod: &Pod) -> bool {
        let node = pod.spec.as_ref().and_then(|spec| spec.node_name.as_deref());
        node.is_some_and(|node| self.contains(node))
    }
}

/// What the cluster loses when `machines` go down.
#[derive(Debug, PartialEq)]
pub struct Loss {
    pub machines: Lost,
    /// How many pods run on the lost machines, Jobs' pods included.
    pub pods: usize,
    /// Each workload the loss leaves down or short, in namespace and
    /// name order.
    pub workloads: Vec<Taken>,
    /// How many DaemonSets run a pod on the lost machines.  A DaemonSet
    /// runs one pod per machine, so a loss never leaves it short.
    pub daemon_sets: usize,
    pub next: Next,
    /// What the dependents of the down workloads lose, one hop out.  The
    /// list leaves out a dependent that the loss also leaves down,
    /// because that dependent has its own line.
    pub cascade: Vec<cascade::Loss>,
}

/// A workload the loss leaves down or short.
#[derive(Debug, PartialEq)]
pub struct Taken {
    pub object: Object,
    pub state: State,
    /// Why a workload left down stays down until the lost machines
    /// return, or `None` when its pods can start elsewhere.
    pub stranded: Option<Stranded>,
    /// What the workload provides, as its intent records it.  Its parcel
    /// shows this text as gone in place of the workload's name.
    pub provides: Option<Line>,
    /// What it does that the cluster cannot do without.
    pub vital: Option<Vital>,
}

/// The stores that `assess` reads a loss from.
pub struct Reading<'r, 'a> {
    pub pods: &'r [Arc<Pod>],
    pub workloads: &'r Workloads<'a>,
    pub ownership: &'r Ownership,
    pub stranding: Stranding<'r>,
    pub edges: cascade::Edges<'r>,
    /// For each workload whose kind knowledge names a primary, the name
    /// of the pod that accepts writes.
    pub primaries: &'r HashMap<Object, String>,
    /// What each workload the cluster cannot do without does.
    pub vital: &'r HashMap<Object, Vital>,
}

/// Applies the rule to every workload with a pod on `machines`.  A
/// `Job`'s pod on a lost machine is lost, and its `CronJob` runs again
/// on its schedule.  So the function counts that pod in `Loss::pods`
/// and in the eviction times, and in no workload's state.
pub fn assess(machines: Lost, reading: &Reading) -> Loss {
    let Reading {
        pods,
        workloads,
        ownership,
        stranding,
        edges,
        primaries,
        vital,
    } = reading;
    let there = |pod: &&Pod| machines.holds(pod);
    let running = pods.iter().map(AsRef::as_ref).filter(|pod| !finished(pod));
    let count = running.filter(there).count();
    let mut taken = Vec::new();
    let mut daemon_sets = 0;
    let mut next = Gather::default();
    for object in ownership.all_workloads() {
        let workload = workloads.get(object);
        let running = workload.running();
        let lost: Vec<&Pod> = running.iter().copied().filter(there).collect();
        if lost.is_empty() {
            continue;
        }
        let role = role(&workload);
        if role == Role::DaemonSet {
            daemon_sets += 1;
            continue;
        }
        for pod in &lost {
            let name = pod.metadata.name.as_deref().unwrap_or_default();
            if let Some(eviction) = stranding.eviction(pod) {
                next.evicted(name, eviction);
            }
        }
        let Role::Replicas(wanted) = role else {
            continue;
        };
        let primary = primaries.get(object);
        let primary_lost = lost.iter().any(|pod| pod.metadata.name.as_ref() == primary);
        let state = rule::state(wanted, &running, |pod| machines.holds(pod), primary_lost);
        let Some(state) = state else {
            continue;
        };
        let reasons: Vec<Option<Stranded>> = lost
            .iter()
            .map(|pod| stranding.stranded(pod, &machines))
            .collect();
        for reason in reasons.iter().flatten() {
            next.stuck(object, reason);
        }
        let stranded = match state {
            State::Down { .. } => stranded(reasons.into_iter()),
            State::Short { .. } | State::Primary { .. } => None,
        };
        taken.push(Taken {
            object: object.clone(),
            state,
            stranded,
            provides: edges.provides(object),
            vital: vital.get(object).copied(),
        });
    }
    let place = |taken: &Taken| (taken.object.namespace.clone(), taken.object.name.clone());
    taken.sort_by_key(place);
    let really_lost = stranding.really_lost(&machines);
    let cascade = cascade_of(&taken, edges);
    Loss {
        machines,
        pods: count,
        workloads: taken,
        daemon_sets,
        next: next.next(really_lost),
        cascade,
    }
}

/// What the dependents lose of each workload in `taken` that is down,
/// or that accepts no writes until a replica is promoted.  A short
/// workload still serves requests, so its dependents lose nothing.
fn cascade_of(taken: &[Taken], edges: &cascade::Edges) -> Vec<cascade::Loss> {
    let down: Vec<&Object> = taken
        .iter()
        .filter(|taken| taken.state.stops())
        .map(|taken| &taken.object)
        .collect();
    let also_down = |loss: &cascade::Loss| match &loss.dependent {
        cascade::Dependent::Caller(object)
        | cascade::Dependent::SignsIn(object)
        | cascade::Dependent::Controlled(object) => down.contains(&object),
        cascade::Dependent::Host(_) => false,
    };
    let losses = down.iter().flat_map(|object| edges.of(object));
    losses.filter(|loss| !also_down(loss)).collect()
}

/// Why a workload stays down, from the reasons of its pods on the lost
/// machines.  Each pod has a reason, or `None` when it can start on
/// another machine.  The function returns the first pod's reason when
/// every pod has one, and `None` otherwise, because one pod that can
/// start on another machine brings the workload back.
fn stranded(reasons: impl Iterator<Item = Option<Stranded>>) -> Option<Stranded> {
    let mut first = None;
    for reason in reasons {
        let reason = reason?;
        first.get_or_insert(reason);
    }
    first
}

impl Loss {
    /// The name that a line shows for `taken`.  It is the workload's own
    /// name, with its kind before it when another workload of the loss
    /// in the same namespace has the same name, as a player and its
    /// remote often do.
    pub fn name_of(&self, taken: &Taken) -> String {
        let object = &taken.object;
        let shared = self.workloads.iter().any(|other| {
            other.object != *object
                && other.object.namespace == object.namespace
                && other.object.name == object.name
        });
        match shared {
            true => transect_graph::edges::kind_and_name(&object.kind, &object.name),
            false => object.name.clone(),
        }
    }
}

/// What one namespace loses, as the map draws it on the namespace's
/// parcel.
#[derive(Debug, PartialEq)]
pub struct Parcel {
    /// `Level::Failing` when a workload is down or without its primary,
    /// and `Level::Warning` when the workloads are only short.  The map
    /// colors the parcel with this level in the same way as for health.
    pub level: Level,
    /// The name and state of each workload that the namespace loses,
    /// except a down workload that `lacks` names by what it provides.
    pub taken: Vec<(String, State)>,
    /// Whether the namespace has only one workload, so the tag does not
    /// need to name it.
    pub alone: bool,
    /// What the down workloads provide, as their intent records it.  The
    /// list includes the namespace's own down workloads and the ones in
    /// other namespaces that its workloads lose.  Each entry appears
    /// once, in sorted order.
    pub lacks: Vec<String>,
    /// The names of the workloads in other namespaces that its workloads
    /// lose, except one that `lacks` names.  A lost workload in the same
    /// namespace already has its own down tag.
    pub loses: Vec<String>,
}

impl Loss {
    /// What `namespace` loses, where `workloads` is the number of
    /// workloads in it.  Returns `None` when the namespace loses nothing.
    pub fn parcel(&self, namespace: &str, workloads: usize) -> Option<Parcel> {
        let in_namespace = self.workloads.iter();
        let in_namespace: Vec<&Taken> = in_namespace
            .filter(|t| t.object.namespace.as_deref() == Some(namespace))
            .collect();
        let down = |taken: &&Taken| taken.state.stops();
        let level = match in_namespace.iter().any(down) {
            true => Level::Failing,
            false => Level::Warning,
        };
        let mut lacks: Vec<String> = Vec::new();
        let mut taken: Vec<(String, State)> = Vec::new();
        for workload in &in_namespace {
            match (&workload.provides, down(workload)) {
                (Some(provides), true) => lacks.push(provides.text.clone()),
                _ => taken.push((self.name_of(workload), workload.state)),
            }
        }
        let mut loses: Vec<String> = Vec::new();
        let elsewhere = self.cascade.iter().filter(|loss| match &loss.dependent {
            cascade::Dependent::Caller(caller) | cascade::Dependent::SignsIn(caller) => {
                caller.namespace.as_deref() == Some(namespace)
                    && loss.lost.namespace != caller.namespace
            }
            _ => false,
        });
        for loss in elsewhere {
            match (&loss.dependent, &loss.provides) {
                (cascade::Dependent::SignsIn(_), _) => lacks.push(sentence::SIGN_IN.into()),
                (_, Some(provides)) => lacks.push(provides.text.clone()),
                (_, None) => loses.push(loss.lost.name.clone()),
            }
        }
        for list in [&mut lacks, &mut loses] {
            list.sort();
            list.dedup();
        }
        let nothing = taken.is_empty() && lacks.is_empty() && loses.is_empty();
        (!nothing).then_some(Parcel {
            level,
            taken,
            alone: workloads == 1,
            lacks,
            loses,
        })
    }
}
