//! Guards are the settings that keep a workload available while its pods
//! stop and start: spreading its pods across machines, a
//! `PodDisruptionBudget` that limits evictions, a rolling update
//! strategy, a readiness probe, and a CPU limit.
//!
//! This module reads each guard from the workload's pod templates and
//! from the budgets that select its pods.  `sentence.rs` writes the
//! page's text for them.  The findings of `transect-supply` report a
//! missing readiness probe and a missing budget, through
//! `takes_traffic_unready` and `unbudgeted`.

mod budget;
mod sentence;
mod served;
mod spread;

use std::sync::Arc;

use k8s_openapi::api::core::v1::{PodSpec, Service};
use k8s_openapi::api::policy::v1::PodDisruptionBudget;

pub use budget::{Budget, Limit};
pub use spread::{Spread, spread};

use crate::machines::pod_limits;
use transect_cluster::workloads::Workload;

/// The update strategy that replaces pods a few at a time, so the
/// workload keeps serving during a rollout.
const ROLLING: &str = "RollingUpdate";

#[derive(Clone, Debug, PartialEq)]
pub struct Guards {
    /// Whether the workload runs more than one pod at once.
    pub replicated: bool,
    /// Whether the workload is a `CronJob` or a `Job`.  Their pods run to
    /// completion and serve nothing, so no probe or strategy guards them.
    pub scheduled: bool,
    pub spread: Option<Spread>,
    pub budgets: Vec<Budget>,
    /// The update strategies of the workload's controllers, without
    /// duplicates.
    pub strategies: Vec<String>,
    /// Whether every pod template has a container with a readiness
    /// probe.  A pod whose containers have none is ready, and receives
    /// its `Service`'s traffic, as soon as the containers start.
    pub readiness_probe: bool,
    /// Whether some pod template that a `Service` selects has no
    /// readiness probe.  Without a `Service`, nothing sends the pod
    /// traffic before it is ready, so a pod such as a database member
    /// that its peers reach directly does not need a probe for traffic.
    pub served_unready: bool,
    /// Whether every pod template limits the CPU of every container.
    pub cpu_limit: bool,
    /// Whether the workload's operator moves its work, such as a
    /// database's primary, off a pod before an eviction.  The kind
    /// knowledge of the workload's kind declares it.
    pub operator_moves: bool,
}

/// One guard as the workload page lists it.
#[derive(Clone, Debug, PartialEq)]
pub enum Guard {
    Spread(Spread),
    /// The workload runs several pods, and nothing keeps them on
    /// separate machines.
    NotSpread,
    /// A budget that selects the workload's pods.  `named` is true when
    /// the workload has more than one budget, so the page shows each
    /// budget's name.
    Budget {
        budget: Budget,
        named: bool,
        /// Whether the workload's operator moves work off a pod before
        /// an eviction.
        operator_moves: bool,
    },
    NoBudget,
    Strategy(String),
    NoReadinessProbe,
    NoCpuLimit,
}

/// How the page draws a guard.  `Held` is a guard that is in place.
/// `Open` is a guard that is missing, or a strategy other than rolling,
/// where the workload does not need it.  `Warn` is a guard that is
/// missing, or a budget that blocks every eviction, where the workload
/// needs it.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Hold {
    Held,
    Open,
    Warn,
}

impl Guards {
    /// The guards of `workload`, with the budgets among `budgets` that
    /// select its pods.
    pub fn read(
        workload: &Workload,
        budgets: &[Arc<PodDisruptionBudget>],
        services: &[Arc<Service>],
    ) -> Self {
        let templates = workload.templates();
        let namespace = workload.object.namespace.as_deref().or_else(|| {
            let pod = workload.pods.first()?;
            pod.metadata.namespace.as_deref()
        });
        let budgets = namespace.map_or_else(Vec::new, |namespace| {
            budget::covering(budgets, namespace, &workload.pod_labels())
        });
        let mut strategies: Vec<String> = Vec::new();
        for strategy in workload
            .parts
            .iter()
            .filter_map(|part| part.strategy.clone())
        {
            if !strategies.contains(&strategy) {
                strategies.push(strategy);
            }
        }
        Self {
            replicated: workload.replicated(),
            scheduled: workload.scheduled(),
            spread: templates.iter().find_map(|spec| spread(Some(spec))),
            budgets,
            strategies,
            readiness_probe: templates.iter().all(|spec| has_readiness_probe(spec)),
            served_unready: namespace.is_some_and(|namespace| {
                served::templates(workload, services, namespace)
                    .any(|spec| !has_readiness_probe(spec))
            }),
            cpu_limit: templates.iter().all(|spec| pod_limits(spec).cpu.is_some()),
            operator_moves: false,
        }
    }

    /// Sets `operator_moves` from the `KindKnowledge` of the workload's
    /// kind.
    pub fn with_operator_moves(self, operator_moves: bool) -> Self {
        Self {
            operator_moves,
            ..self
        }
    }

    /// Whether a `Service` can send requests to a pod that is not yet
    /// ready while another pod of the workload could answer them.
    pub fn takes_traffic_unready(&self) -> bool {
        self.replicated && !self.scheduled && self.served_unready
    }

    /// Whether the workload runs more than one pod and no budget limits
    /// evictions, so a drain can remove all of its pods at once.
    pub fn unbudgeted(&self) -> bool {
        self.replicated && self.budgets.is_empty()
    }

    /// The guards in the order the page lists them: spread, budgets,
    /// strategies, then the guards that are missing.  A workload with
    /// one pod has nothing to spread or to budget, so the list has
    /// neither for it.
    pub fn list(&self) -> Vec<Guard> {
        let mut list = Vec::new();
        match (self.replicated, self.spread) {
            (true, Some(spread)) => list.push(Guard::Spread(spread)),
            (true, None) => list.push(Guard::NotSpread),
            (false, _) => {}
        }
        let named = self.budgets.len() > 1;
        let budgets = self.budgets.iter().cloned();
        list.extend(budgets.map(|budget| Guard::Budget {
            budget,
            named,
            operator_moves: self.operator_moves,
        }));
        if self.unbudgeted() {
            list.push(Guard::NoBudget);
        }
        if !self.scheduled {
            list.extend(self.strategies.iter().cloned().map(Guard::Strategy));
            if !self.readiness_probe {
                list.push(Guard::NoReadinessProbe);
            }
        }
        if !self.cpu_limit {
            list.push(Guard::NoCpuLimit);
        }
        list
    }

    /// How the page draws `guard` of this workload.
    pub fn hold(&self, guard: &Guard) -> Hold {
        match guard {
            Guard::Spread(_) => Hold::Held,
            Guard::Budget { budget, .. } if budget.allowed > 0 => Hold::Held,
            // An operator that moves the primary before a drain sets a
            // budget that allows no eviction on purpose, and relaxes it
            // after the move.
            Guard::Budget {
                operator_moves: true,
                ..
            } => Hold::Held,
            // A budget that allows no eviction blocks every drain until
            // a person deletes it or the workload scales up.  With no
            // budget, a drain can evict every pod at once.
            Guard::Budget { .. } | Guard::NoBudget => Hold::Warn,
            Guard::Strategy(strategy) if strategy == ROLLING => Hold::Held,
            Guard::NoReadinessProbe if self.takes_traffic_unready() => Hold::Warn,
            Guard::Strategy(_) | Guard::NotSpread | Guard::NoReadinessProbe | Guard::NoCpuLimit => {
                Hold::Open
            }
        }
    }
}

/// Whether any of `spec`'s app containers has a readiness probe.  The
/// pod is ready only when every container is ready, so one probe keeps
/// the whole pod unready until the probe passes.
fn has_readiness_probe(spec: &PodSpec) -> bool {
    spec.containers.iter().any(|c| c.readiness_probe.is_some())
}

#[cfg(test)]
mod tests;
