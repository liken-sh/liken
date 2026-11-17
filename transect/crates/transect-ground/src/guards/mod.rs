//! Guards are the settings that keep a workload answering while its pods
//! stop and start: its spread across machines, the `PodDisruptionBudget`
//! that limits evictions, its update strategy, a readiness probe that
//! keeps traffic away from a pod until the pod answers, and a CPU limit.
//!
//! This module reads each guard from the workload's pod templates and
//! from the budgets that select its pods.  The page's words for them are
//! in `sentence.rs`.  The workload findings in `transect-supply`'s
//! `findings/` report two of these guards when they are missing from a
//! workload that runs more than one pod.

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

/// The update strategy that replaces pods a few at a time.  It is the
/// strategy that keeps a workload answering during a rollout.
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
    /// Each update strategy that the workload's controllers use, listed
    /// once.
    pub strategies: Vec<String>,
    /// Whether every pod template has a container with a readiness
    /// probe.  A pod whose containers have none is ready, and receives
    /// its `Service`'s traffic, as soon as they start.
    pub readiness_probe: bool,
    /// Whether a pod template that a `Service` selects has no readiness
    /// probe.  Only a `Service` sends a pod traffic before the pod is
    /// ready, so pods that no `Service` selects, such as the members of
    /// a store that find each other on their own, need no probe for
    /// this reason.
    pub served_unready: bool,
    /// Whether every pod template limits the CPU of every container.
    pub cpu_limit: bool,
    /// Whether the workload's operator moves its work, such as a
    /// database's primary, off a pod before an eviction, as the kind
    /// knowledge of its kind declares.  When it does, a budget that
    /// allows no eviction of its pods is intentional.
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
    /// the workload has more than one budget, so each chip shows which
    /// budget it is.
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

/// How the page draws a guard: in place, open when nothing is wrong with
/// that, or open when the workload needs the guard.
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

    /// The guards with `operator_moves`, which the workload's kind
    /// knowledge gives.
    pub fn with_operator_moves(self, operator_moves: bool) -> Self {
        Self {
            operator_moves,
            ..self
        }
    }

    /// Whether a `Service` can send requests to one of its pods before
    /// the pod answers, while other pods could have answered them.
    pub fn takes_traffic_unready(&self) -> bool {
        self.replicated && !self.scheduled && self.served_unready
    }

    /// Whether nothing prevents evictions from removing all of the
    /// workload's pods at once.
    pub fn unbudgeted(&self) -> bool {
        self.replicated && self.budgets.is_empty()
    }

    /// The guards in the order the page lists them: spread, budgets,
    /// strategy, then the guards that are missing.  A single pod has
    /// nothing to spread, so the list reports neither its spread nor a
    /// missing budget for it.
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

    /// How the page draws `guard`, which is one of this workload's
    /// guards.
    pub fn hold(&self, guard: &Guard) -> Hold {
        match guard {
            Guard::Spread(_) => Hold::Held,
            Guard::Budget { budget, .. } if budget.allowed > 0 => Hold::Held,
            // An operator that switches primaries before a drain sets a
            // budget like this on purpose, and relaxes the budget after
            // it has switched them.
            Guard::Budget {
                operator_moves: true,
                ..
            } => Hold::Held,
            // A budget that lets no pod go blocks every drain until a
            // person deletes it or the workload scales up.
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
