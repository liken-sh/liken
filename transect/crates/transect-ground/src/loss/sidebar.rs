//! The sidebar of the cluster page, built from a `Loss`. It lists, one
//! line for each namespace, the workloads that stay down until the lost
//! machines return, the ones that come back when their pods start on
//! other machines, the ones without their primary, and the ones left
//! short.

use std::collections::BTreeMap;

use super::cascade::{self, Hop};
use super::{Loss, Lost, Majority, Next, NotReadyMachine, State, Stranded, Vital};
use transect_graph::edges::Object;
use transect_graph::links::{Linker, Part, Piece};

/// The sidebar shows every "Running short" line that is more than one
/// replica short.  It shows lines that are one replica short only until
/// the list has `SHORT_LINES` lines, and counts the rest in `folded`.
const SHORT_LINES: usize = 3;

#[derive(Debug, PartialEq)]
pub struct Sidebar {
    pub machines: Lost,
    pub pods: usize,
    /// The vital workloads that the loss stops, ordered by `Vital`, then
    /// by namespace and name.
    pub vital: Vec<Blind>,
    /// The namespaces with a workload that stays down until the lost
    /// machines return, by name.  Each line gives the reason.
    pub stuck: Vec<Line>,
    /// The namespaces with a workload that comes back when its pods
    /// start on other machines, by name.
    pub back: Vec<Line>,
    /// The namespaces with a workload that loses its primary, by name.
    /// Such a workload accepts no writes until a replica is promoted.
    pub primary: Vec<Line>,
    /// The namespaces with a short workload, with those more than one
    /// replica short first, then by name.
    pub short: Vec<Line>,
    /// How many lines one replica short `build` left out of `short`.
    pub folded: usize,
    pub daemon_sets: usize,
    /// What the dependents of the down workloads lose, one line for
    /// each namespace, by name, with the line of cluster-scoped
    /// dependents last.  Custom resources that a down controller
    /// reconciles are in `unreconciled` instead.
    pub cascade: Vec<Hops>,
    /// The custom resources that nothing reconciles until their
    /// controllers return, one line for each set of lost controllers.
    pub unreconciled: Vec<Unreconciled>,
    /// The plural of each custom kind, which a line uses when it counts
    /// the objects of that kind.
    pub plurals: transect_graph::edges::Plurals,
    pub next: Next,
    /// The machines of the set that are really down, by name.  The other
    /// machines of the set are a prediction: the reader asked what their
    /// loss would do.
    pub gone: Vec<NotReadyMachine>,
    /// What the loss leaves of the control plane, when it takes some of
    /// its machines.
    pub majority: Option<Majority>,
    /// The requested machines that the cluster does not have.
    pub unknown: Option<String>,
    /// What transect cannot read, when the loss takes the machines that
    /// its signals come from.
    pub blind: Option<String>,
    /// Why transect cannot tell whether alerts still work, when no
    /// resource names the alerting path.  Without this line, the reader
    /// would take a sidebar with no alerting line to mean that alerts
    /// still work.
    pub alerting: Option<transect_resources::resources::OffLine>,
    /// The links to the pages of the objects that the lines name.
    pub linker: Linker,
}

/// The dependents in one namespace that a loss reaches one hop out, or
/// the cluster-scoped dependents when `namespace` is `None`.
#[derive(Debug, PartialEq)]
pub struct Hops {
    pub namespace: Option<String>,
    pub hops: Vec<Hop>,
}

/// An object as a line shows it: by its name, or by its kind and name
/// when another object in the line has the same name.  Ordered by
/// `shown`.
#[derive(Clone, Debug, PartialEq, Eq, PartialOrd, Ord)]
pub struct Named {
    pub shown: String,
    pub object: Object,
}

impl Named {
    pub fn piece(&self) -> Piece {
        Piece::Name(self.object.clone(), self.shown.clone())
    }
}

/// The custom resources that the same set of lost controllers
/// reconciles.
#[derive(Debug, PartialEq)]
pub struct Unreconciled {
    pub controllers: Vec<Object>,
    /// By API group, then kind, then name.
    pub objects: Vec<Object>,
}

/// Splits `hops` into the custom resources that lost controllers
/// reconcile, grouped by the set of controllers, and the other hops.
fn unreconciled(hops: Vec<Hop>) -> (Vec<Unreconciled>, Vec<Hop>) {
    let mut by: BTreeMap<Vec<Object>, Vec<Object>> = BTreeMap::new();
    let mut rest = Vec::new();
    for hop in hops {
        match hop.dependent {
            cascade::Dependent::Controlled(object) => {
                let mut controllers = hop.lost;
                controllers.sort();
                by.entry(controllers).or_default().push(object);
            }
            _ => rest.push(hop),
        }
    }
    let lines = by.into_iter().map(|(controllers, mut objects)| {
        objects.sort_by(|a, b| {
            let (a_group, b_group) = (
                transect_graph::dns::key(&a.group),
                transect_graph::dns::key(&b.group),
            );
            (a_group, &a.kind, &a.name).cmp(&(b_group, &b.kind, &b.name))
        });
        objects.dedup();
        Unreconciled {
            controllers,
            objects,
        }
    });
    (lines.collect(), rest)
}

/// One vital workload that a loss stops.
#[derive(Debug, PartialEq)]
pub struct Blind {
    pub namespace: String,
    pub name: Named,
    pub vital: Vital,
}

/// One namespace's workloads in one list of the sidebar, by name.
#[derive(Debug, PartialEq)]
pub struct Line {
    pub namespace: String,
    pub workloads: Vec<(Named, State)>,
    /// Why each stranded workload stays down, by workload name.
    pub stranded: Vec<(String, Stranded)>,
}

impl Line {
    fn one_short(&self) -> bool {
        self.workloads.iter().all(|(_, state)| state.one_short())
    }
}

impl Sidebar {
    pub fn build(loss: &Loss) -> Self {
        let mut stuck: BTreeMap<&str, Vec<(Named, State)>> = BTreeMap::new();
        let mut back: BTreeMap<&str, Vec<(Named, State)>> = BTreeMap::new();
        let mut short: BTreeMap<&str, Vec<(Named, State)>> = BTreeMap::new();
        let mut primary: BTreeMap<&str, Vec<(Named, State)>> = BTreeMap::new();
        let mut reasons: BTreeMap<&str, Vec<(String, Stranded)>> = BTreeMap::new();
        for taken in &loss.workloads {
            let Some(namespace) = taken.object.namespace.as_deref() else {
                continue;
            };
            let name = loss.name_of(taken);
            let list = match (taken.state, &taken.stranded) {
                (State::Down { .. }, Some(reason)) => {
                    let reason = (name.clone(), reason.clone());
                    reasons.entry(namespace).or_default().push(reason);
                    &mut stuck
                }
                (State::Down { .. }, None) => &mut back,
                (State::Short { .. }, _) => &mut short,
                (State::Primary { .. }, _) => &mut primary,
            };
            let named = Named {
                shown: name,
                object: taken.object.clone(),
            };
            list.entry(namespace)
                .or_default()
                .push((named, taken.state));
        }
        let mut vital: Vec<Blind> = loss
            .workloads
            .iter()
            .filter(|taken| taken.state.stops())
            .filter_map(|taken| {
                Some(Blind {
                    namespace: taken.object.namespace.clone()?,
                    name: Named {
                        shown: loss.name_of(taken),
                        object: taken.object.clone(),
                    },
                    vital: taken.vital?,
                })
            })
            .collect();
        vital.sort_by(|a, b| {
            (a.vital, &a.namespace, &a.name).cmp(&(b.vital, &b.namespace, &b.name))
        });
        let mut stuck = lines(stuck);
        for line in &mut stuck {
            line.stranded = reasons.remove(line.namespace.as_str()).unwrap_or_default();
            line.stranded.sort_by(|a, b| a.0.cmp(&b.0));
        }
        let mut short = lines(short);
        // The sort is stable, so each group keeps its name order.
        short.sort_by_key(Line::one_short);
        let shown = short.iter().enumerate();
        let kept = shown.take_while(|(index, line)| *index < SHORT_LINES || !line.one_short());
        let kept = kept.count();
        let folded = short.len() - kept;
        short.truncate(kept);
        let (unreconciled, rest) = unreconciled(cascade::hops(loss.cascade.iter().cloned()));
        Self {
            machines: loss.machines.clone(),
            pods: loss.pods,
            vital,
            stuck,
            back: lines(back),
            primary: lines(primary),
            short,
            folded,
            daemon_sets: loss.daemon_sets,
            cascade: by_namespace(rest),
            unreconciled,
            plurals: Default::default(),
            next: loss.next.clone(),
            gone: Vec::new(),
            majority: None,
            unknown: None,
            blind: None,
            alerting: None,
            linker: Linker::default(),
        }
    }

    /// Links each object of `loss` that the sidebar's lines show to the
    /// page that `href` returns for the object.
    pub fn linked(self, loss: &Loss, href: impl Fn(&Object) -> Option<String>) -> Self {
        let taken = loss.workloads.iter().map(|taken| &taken.object);
        let reached = self.cascade.iter().flat_map(|line| &line.hops);
        let unreconciled = self
            .unreconciled
            .iter()
            .flat_map(|line| line.controllers.iter().chain(&line.objects));
        let objects: Vec<Object> = taken
            .chain(reached.flat_map(Hop::objects))
            .chain(unreconciled)
            .cloned()
            .collect();
        Self {
            linker: Linker::new(objects, href),
            ..self
        }
    }

    pub fn with_plurals(self, plurals: &transect_graph::edges::Plurals) -> Self {
        Self {
            plurals: plurals.clone(),
            ..self
        }
    }

    /// The parts of `pieces`, one line of the sidebar, with a link on
    /// each object that the line names.
    pub fn link(&self, pieces: &[Piece]) -> Vec<Part> {
        self.linker.link(pieces)
    }

    /// Sets the line that lists the requested machines that the cluster
    /// does not have.
    pub fn with_unknown(self, unknown: Option<String>) -> Self {
        Self { unknown, ..self }
    }

    /// Sets the line that says what `transect` cannot read.
    pub fn with_blind(self, blind: Option<String>) -> Self {
        Self { blind, ..self }
    }

    /// Sets the reason that `transect` cannot tell whether alerts still
    /// work.
    pub fn with_alerting(self, alerting: Option<transect_resources::resources::OffLine>) -> Self {
        Self { alerting, ..self }
    }

    /// Keeps the machines of `gone` that are in the sidebar's set.
    /// `gone` lists every machine of the cluster that is really lost.
    pub fn with_gone(self, gone: &[NotReadyMachine]) -> Self {
        let gone = gone.iter().filter(|g| self.machines.contains(&g.machine));
        let gone = gone.cloned().collect();
        Self { gone, ..self }
    }

    /// The sidebar with what the loss leaves of the control plane.  A
    /// control plane without a majority accepts no writes, so no pod is
    /// evicted or rescheduled.  Every down workload then stays down
    /// until the machines return, so `back` moves into `stuck` and
    /// `next` is cleared.
    pub fn with_majority(self, majority: Option<Majority>) -> Self {
        if !majority.is_some_and(|majority| !majority.has_quorum()) {
            return Self { majority, ..self };
        }
        Self {
            stuck: merge(self.stuck, self.back),
            back: Vec::new(),
            next: Next::default(),
            majority,
            ..self
        }
    }

    /// Whether the loss leaves the control plane without a majority.
    pub fn headless(&self) -> bool {
        self.majority.is_some_and(|majority| !majority.has_quorum())
    }

    /// The machines of the set that are only predicted lost, by name.
    pub fn supposed(&self) -> Vec<&str> {
        let gone = |name: &&str| self.gone.iter().any(|g| g.machine == *name);
        self.machines.names().filter(|name| !gone(name)).collect()
    }

    /// Whether some machine of the set is really lost, which makes the
    /// page show "What to know now" below the loss.
    pub fn real(&self) -> bool {
        !self.gone.is_empty()
    }

    /// Whether every machine of the set is really lost.  This is the
    /// case for the page at `/`, where the reader requested no
    /// prediction.
    pub fn all_real(&self) -> bool {
        self.supposed().is_empty()
    }

    /// The value of a URL's `lose` parameter for the predicted
    /// machines.  It leaves out the really lost machines, so a link
    /// stops naming a machine after the machine returns.
    pub fn param(&self) -> String {
        self.supposed().join(",")
    }

    /// How long the longest-lost machine of the set has been lost, in
    /// seconds.
    pub fn longest(&self) -> Option<i64> {
        self.gone.iter().filter_map(|g| g.seconds).max()
    }

    /// How long the whole set has been lost, in seconds, when every
    /// machine of it is really lost.  It counts from the most recent
    /// loss, because the pods of the machine lost last are evicted last.
    /// `None` when a machine of the set has no `Ready` condition.
    pub fn elapsed(&self) -> Option<i64> {
        let seconds = self.gone.iter().map(|g| g.seconds);
        self.all_real().then(|| seconds.min().flatten()).flatten()
    }
}

/// Groups `hops`, which arrive in namespace order, into one line for
/// each namespace, with the line of cluster-scoped hops last.
fn by_namespace(hops: Vec<Hop>) -> Vec<Hops> {
    let mut lines: Vec<Hops> = Vec::new();
    for hop in hops {
        match lines.last_mut() {
            Some(line) if line.namespace == hop.namespace => line.hops.push(hop),
            _ => lines.push(Hops {
                namespace: hop.namespace.clone(),
                hops: vec![hop],
            }),
        }
    }
    lines.sort_by_key(|line| line.namespace.is_none());
    lines
}

/// The lines of `stuck` and `back` as one list, by namespace.  A
/// namespace in both lists gets one merged line.
fn merge(stuck: Vec<Line>, back: Vec<Line>) -> Vec<Line> {
    let mut by_namespace: BTreeMap<String, Line> = BTreeMap::new();
    for line in stuck.into_iter().chain(back) {
        match by_namespace.get_mut(&line.namespace) {
            Some(merged) => {
                merged.workloads.extend(line.workloads);
                merged.workloads.sort_by(|a, b| a.0.cmp(&b.0));
            }
            None => {
                by_namespace.insert(line.namespace.clone(), line);
            }
        }
    }
    by_namespace.into_values().collect()
}

fn lines(by_namespace: BTreeMap<&str, Vec<(Named, State)>>) -> Vec<Line> {
    by_namespace
        .into_iter()
        .map(|(namespace, mut workloads)| {
            workloads.sort_by(|a, b| a.0.cmp(&b.0));
            Line {
                namespace: namespace.into(),
                workloads,
                stranded: Vec::new(),
            }
        })
        .collect()
}
