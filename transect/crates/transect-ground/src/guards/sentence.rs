//! The fixed words that the page shows for each guard.  The rest of a
//! guard's text is names and numbers from the cluster.

use std::fmt;

use super::{Budget, Guard, Limit, Spread};

impl fmt::Display for Guard {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::Spread(Spread::AntiAffinity) => write!(f, "anti-affinity by machine"),
            Self::Spread(Spread::TopologySpread) => write!(f, "topology spread by machine"),
            Self::NotSpread => write!(f, "not spread by machine"),
            Self::Budget {
                budget,
                named,
                operator_moves,
            } => {
                match named {
                    true => write!(f, "PDB {}: {}", budget.name, budget_terms(budget))?,
                    false => write!(f, "PDB: {}", budget_terms(budget))?,
                }
                if *operator_moves && budget.allowed <= 0 {
                    write!(f, "; its operator moves the primary first")?;
                }
                Ok(())
            }
            Self::NoBudget => write!(f, "no PodDisruptionBudget"),
            Self::Strategy(strategy) => write!(f, "{strategy}"),
            Self::NoReadinessProbe => write!(f, "no readiness probe"),
            Self::NoCpuLimit => write!(f, "no CPU limit"),
        }
    }
}

impl Guard {
    /// The guard's tooltip, which names a budget, because a workload can
    /// have more than one budget.
    pub fn title(&self) -> Option<String> {
        match self {
            Self::Budget { budget, .. } => Some(format!("PodDisruptionBudget {}", budget.name)),
            _ => None,
        }
    }
}

/// "min 1, 2 may be evicted", "max 1 down, 1 may be evicted", or "min 1,
/// none may be evicted".
fn budget_terms(budget: &Budget) -> String {
    let may_go = match budget.allowed {
        ..=0 => "none may be evicted".to_string(),
        allowed => format!("{allowed} may be evicted"),
    };
    match &budget.limit {
        Limit::MinAvailable(min) => format!("min {min}, {may_go}"),
        Limit::MaxUnavailable(max) => format!("max {max} down, {may_go}"),
        Limit::None => may_go,
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    fn budget(limit: Limit, allowed: i32) -> Guard {
        Guard::Budget {
            budget: Budget {
                name: "web".into(),
                limit,
                allowed,
            },
            named: false,
            operator_moves: false,
        }
    }

    fn moved(guard: Guard) -> Guard {
        match guard {
            Guard::Budget { budget, named, .. } => Guard::Budget {
                budget,
                named,
                operator_moves: true,
            },
            other => other,
        }
    }

    #[rstest]
    #[case::anti_affinity(Guard::Spread(Spread::AntiAffinity), "anti-affinity by machine")]
    #[case::topology(Guard::Spread(Spread::TopologySpread), "topology spread by machine")]
    #[case::not_spread(Guard::NotSpread, "not spread by machine")]
    #[case::min_available(budget(Limit::MinAvailable("1".into()), 2), "PDB: min 1, 2 may be evicted")]
    #[case::max_unavailable(budget(Limit::MaxUnavailable("25%".into()), 1), "PDB: max 25% down, 1 may be evicted")]
    #[case::none_may_be_evicted(budget(Limit::MinAvailable("1".into()), 0), "PDB: min 1, none may be evicted")]
    #[case::no_limit(budget(Limit::None, 1), "PDB: 1 may be evicted")]
    #[case::one_of_several(
        Guard::Budget { budget: Budget { name: "primary".into(), limit: Limit::MinAvailable("1".into()), allowed: 0 }, named: true, operator_moves: false },
        "PDB primary: min 1, none may be evicted"
    )]
    #[case::none_may_be_evicted_by_design(
        moved(budget(Limit::MinAvailable("1".into()), 0)),
        "PDB: min 1, none may be evicted; its operator moves the primary first"
    )]
    #[case::some_may_be_evicted_with_an_operator(
        moved(budget(Limit::MinAvailable("1".into()), 1)),
        "PDB: min 1, 1 may be evicted"
    )]
    #[case::no_budget(Guard::NoBudget, "no PodDisruptionBudget")]
    #[case::strategy(Guard::Strategy("RollingUpdate".into()), "RollingUpdate")]
    #[case::no_probe(Guard::NoReadinessProbe, "no readiness probe")]
    #[case::no_cpu_limit(Guard::NoCpuLimit, "no CPU limit")]
    fn guard_text(#[case] guard: Guard, #[case] text: &str) {
        assert_eq!(guard.to_string(), text);
    }
}
