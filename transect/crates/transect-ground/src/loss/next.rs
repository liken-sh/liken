//! What happens to the pods of the lost machines: when Kubernetes
//! evicts them, and which of them stay stuck and why.
//!
//! transect reads each pod's eviction time from its tolerations, so the
//! sidebar shows how long the cluster will wait.  Kubernetes admission
//! gives most pods a toleration of 300 seconds, so the sidebar shows the
//! most common time and names the pods whose time differs.

use std::collections::{BTreeMap, HashMap};

use super::Stranded;
use super::sidebar::Named;
use crate::placement::Eviction;
use transect_graph::edges::Object;

/// What happens to the lost machines' pods after the loss.
#[derive(Clone, Debug, Default, PartialEq)]
pub struct Next {
    /// When Kubernetes evicts most of the pods on the lost machines, or
    /// `None` when every pod there belongs to a `DaemonSet`.
    pub usual: Option<Eviction>,
    /// The pods that Kubernetes evicts after a time other than `usual`.
    /// A pod that is never evicted is stuck instead.
    pub differ: Vec<(String, Eviction)>,
    /// The workloads whose pods stay stuck, grouped by the rank of the
    /// reason, each group in name order.  A name that two namespaces
    /// share is shown as `namespace/name`, and a name that two kinds
    /// share in one namespace as `Kind name`.
    pub stuck: Vec<(Stranded, Vec<Named>)>,
    /// Whether every lost machine is really down, not only selected to
    /// predict the loss.
    pub really_lost: bool,
}

/// Collects the evictions and the stuck workloads while `assess` walks
/// the workloads.
#[derive(Default)]
pub struct Gather {
    evictions: Vec<(String, Eviction)>,
    stuck: BTreeMap<usize, (Stranded, Vec<Object>)>,
}

impl Gather {
    pub fn evicted(&mut self, pod: &str, eviction: Eviction) {
        self.evictions.push((pod.into(), eviction));
    }

    /// Records that a pod of `workload` stays stuck for `reason`.
    pub fn stuck(&mut self, workload: &Object, reason: &Stranded) {
        let group = self.stuck.entry(reason.rank());
        let (_, workloads) = group.or_insert_with(|| (reason.clone(), Vec::new()));
        if !workloads.contains(workload) {
            workloads.push(workload.clone());
        }
    }

    pub fn next(self, really_lost: bool) -> Next {
        let usual = usual(self.evictions.iter().map(|(_, eviction)| *eviction));
        let mut differ: Vec<(String, Eviction)> = self
            .evictions
            .into_iter()
            .filter(|(_, eviction)| Some(*eviction) != usual && *eviction != Eviction::Never)
            .collect();
        differ.sort();
        let stuck = self.stuck.into_values().map(|(reason, workloads)| {
            let mut names: Vec<Named> = workloads
                .iter()
                .map(|workload| {
                    let alike = |w: &&Object| w.name == workload.name;
                    let named = workloads.iter().filter(alike);
                    let here = named.clone().filter(|w| w.namespace == workload.namespace);
                    let shown = match (named.count(), here.count(), &workload.namespace) {
                        (_, 2.., _) => {
                            transect_graph::edges::kind_and_name(&workload.kind, &workload.name)
                        }
                        (2.., _, Some(namespace)) => format!("{namespace}/{}", workload.name),
                        _ => workload.name.clone(),
                    };
                    Named {
                        shown,
                        object: workload.clone(),
                    }
                })
                .collect();
            names.sort();
            (reason, names)
        });
        Next {
            usual,
            differ,
            stuck: stuck.collect(),
            really_lost,
        }
    }
}

/// The most common eviction among the pods.  When two are equally
/// common, it returns the sooner one, because that one happens first.
fn usual(evictions: impl Iterator<Item = Eviction>) -> Option<Eviction> {
    let mut counts: HashMap<Eviction, usize> = HashMap::new();
    for eviction in evictions {
        *counts.entry(eviction).or_default() += 1;
    }
    let most = counts
        .into_iter()
        .max_by(|a, b| a.1.cmp(&b.1).then(b.0.cmp(&a.0)));
    most.map(|(eviction, _)| eviction)
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    const FIVE: Eviction = Eviction::After(300);
    const HALF: Eviction = Eviction::After(30);

    #[rstest]
    #[case::none(vec![], None)]
    #[case::most(vec![FIVE, HALF, FIVE], Some(FIVE))]
    #[case::a_tie_goes_to_the_sooner(vec![FIVE, HALF], Some(HALF))]
    #[case::never_counts_too(vec![Eviction::Never, Eviction::Never, FIVE], Some(Eviction::Never))]
    fn the_usual_eviction_is_the_commonest(
        #[case] evictions: Vec<Eviction>,
        #[case] expected: Option<Eviction>,
    ) {
        assert_eq!(usual(evictions.into_iter()), expected);
    }

    fn named(shown: &str, object: Object) -> Named {
        Named {
            shown: shown.into(),
            object,
        }
    }

    #[rstest]
    fn names_the_pods_that_differ_and_groups_the_stuck() {
        let mut gather = Gather::default();
        gather.evicted("web-a", FIVE);
        gather.evicted("web-b", FIVE);
        gather.evicted("db-0", HALF);
        gather.evicted("agent-x", Eviction::Never);
        let workload =
            |namespace: &str, name: &str| Object::new("apps", "Deployment", Some(namespace), name);
        gather.stuck(&workload("shop", "web"), &Stranded::NoFit);
        gather.stuck(&workload("shop", "db"), &Stranded::NeverEvicted);
        gather.stuck(&workload("shop", "cache"), &Stranded::NoFit);
        gather.stuck(&workload("shop", "cache"), &Stranded::NoFit);
        gather.stuck(&workload("blog", "db"), &Stranded::NeverEvicted);
        let player = Object::new("example.com", "Player", Some("shop"), "web");
        gather.stuck(&player, &Stranded::NoFit);
        assert_eq!(
            gather.next(false),
            Next {
                usual: Some(FIVE),
                differ: vec![("db-0".into(), HALF)],
                stuck: vec![
                    (
                        Stranded::NoFit,
                        vec![
                            named("Deployment web", workload("shop", "web")),
                            named("Player web", player),
                            named("cache", workload("shop", "cache")),
                        ]
                    ),
                    (
                        Stranded::NeverEvicted,
                        vec![
                            named("blog/db", workload("blog", "db")),
                            named("shop/db", workload("shop", "db")),
                        ]
                    ),
                ],
                really_lost: false,
            }
        );
    }
}
