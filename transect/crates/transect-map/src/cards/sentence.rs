//! The text of the namespace card and the workload card. All other text
//! on them is a name or a number from the cluster.

use std::fmt;

use super::chips::{Chip, Label};
use super::namespace::NamespaceCard;
use super::workload::{Idle, WorkloadCard};
use transect_observe::health::Level;
use transect_resources::knowledge::age;

impl NamespaceCard {
    /// A word for the namespace's health.
    pub fn health_word(&self) -> &'static str {
        health_word(self.health.level)
    }

    /// The text for the number of volumes in the namespace, or `None`
    /// when there are none.
    pub fn volumes_text(&self) -> Option<String> {
        (self.volumes > 0).then(|| transect_ground::storage::volumes(self.volumes))
    }
}

impl WorkloadCard {
    /// The text beside the health dot, such as "3 of 3 ready". A
    /// workload with no running pods says so, with how its last job
    /// ended. A workload with no ready count, such as a CronJob, gets a
    /// word for its health.
    pub fn status_text(&self) -> String {
        match self.idle {
            Some(Idle::Unrun) => return "no running pods".into(),
            Some(Idle::Ended { failed, minutes }) => {
                let ended = if failed { "failed" } else { "finished" };
                return format!("no running pods; its last job {ended} {} ago", age(minutes));
            }
            None => {}
        }
        match self.ready {
            Some((0, wanted)) if wanted > 0 => format!("none of {wanted} ready"),
            Some((ready, wanted)) => format!("{ready} of {wanted} ready"),
            None => health_word(self.health.level).into(),
        }
    }

    /// How long ago the workload last rolled out, such as "4d ago". The
    /// Runs box shows it under its "rolled" heading.
    pub fn rolled_ago(&self) -> Option<String> {
        self.rolled.map(|minutes| format!("{} ago", age(minutes)))
    }
}

fn health_word(level: Level) -> &'static str {
    match level {
        Level::Healthy => "healthy",
        Level::Notice => "worth noting",
        Level::Warning => "warning",
        Level::Failing => "failing",
    }
}

impl fmt::Display for Chip {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        self.label.fmt(f)
    }
}

impl fmt::Display for Label {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::Replicated { name, replicas } => write!(f, "{name} ×{replicas}"),
            Self::Single(names) => write!(f, "{}", names.join(" · ")),
            Self::CronJobs(1) => write!(f, "1 CronJob"),
            Self::CronJobs(count) => write!(f, "{count} CronJobs"),
            Self::Jobs(1) => write!(f, "1 Job"),
            Self::Jobs(count) => write!(f, "{count} Jobs"),
            Self::Owned { kind, name, .. } => {
                f.write_str(&transect_graph::edges::kind_and_name(kind, name))
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;
    use crate::cards::workload::fixtures::workload_card;
    use transect_observe::health::Health;

    #[rstest]
    #[case::replicated(Label::Replicated { name: "web".into(), replicas: 3 }, "web ×3")]
    #[case::scaled_to_zero(Label::Replicated { name: "web".into(), replicas: 0 }, "web ×0")]
    #[case::one_single(Label::Single(vec!["cache".into()]), "cache")]
    #[case::two_singles(Label::Single(vec!["auth".into(), "cache".into()]), "auth · cache")]
    #[case::one_cron_job(Label::CronJobs(1), "1 CronJob")]
    #[case::cron_jobs(Label::CronJobs(3), "3 CronJobs")]
    #[case::one_job(Label::Jobs(1), "1 Job")]
    #[case::jobs(Label::Jobs(2), "2 Jobs")]
    #[case::a_custom_kind(Label::Owned { kind: "Cache".into(), name: "sessions".into(), custom: true }, "Cache sessions")]
    fn chip_text(#[case] label: Label, #[case] text: &str) {
        assert_eq!(label.to_string(), text);
    }

    #[rstest]
    #[case::healthy(Level::Healthy, "healthy")]
    #[case::warning(Level::Warning, "warning")]
    #[case::failing(Level::Failing, "failing")]
    fn health_in_a_word(#[case] level: Level, #[case] word: &str) {
        let card = NamespaceCard {
            name: "shop".into(),
            why: None,
            gotcha: None,
            provides: None,
            docs: None,
            health: Health {
                level,
                reasons: Vec::new(),
            },
            reason_pages: Vec::new(),
            hosts: Vec::new(),
            chips: Vec::new(),
            volumes: 0,
            depends: Default::default(),
            code: Default::default(),
            managed: Default::default(),
            loops: Vec::new(),
            calls: Default::default(),
            holds: Default::default(),
            layer_named: None,
            logged: Vec::new(),
        };
        assert_eq!(card.health_word(), word);
    }

    #[rstest]
    #[case::all_ready(Some((3, 3)), "3 of 3 ready")]
    #[case::some_ready(Some((1, 3)), "1 of 3 ready")]
    #[case::none_ready(Some((0, 2)), "none of 2 ready")]
    #[case::scaled_to_zero(Some((0, 0)), "0 of 0 ready")]
    #[case::pods_that_finish(None, "healthy")]
    fn a_workloads_status(#[case] ready: Option<(i32, i32)>, #[case] text: &str) {
        assert_eq!(workload_card(ready, None).status_text(), text);
    }

    #[rstest]
    #[case::never_ran(Idle::Unrun, "no running pods")]
    #[case::last_finished(
        Idle::Ended { failed: false, minutes: 190 },
        "no running pods; its last job finished 3h ago"
    )]
    #[case::last_failed(
        Idle::Ended { failed: true, minutes: 12 },
        "no running pods; its last job failed 12m ago"
    )]
    fn a_workload_whose_pods_have_all_finished(#[case] idle: Idle, #[case] text: &str) {
        let card = WorkloadCard {
            idle: Some(idle),
            ..workload_card(Some((0, 0)), None)
        };
        assert_eq!(card.status_text(), text);
    }

    #[rstest]
    #[case::days(Some(4 * 1440 + 30), Some("4d ago"))]
    #[case::never(None, None)]
    fn when_a_workload_rolled(#[case] rolled: Option<i64>, #[case] text: Option<&str>) {
        assert_eq!(workload_card(None, rolled).rolled_ago().as_deref(), text);
    }

    #[rstest]
    #[case::none(0, None)]
    #[case::one(1, Some("1 volume"))]
    #[case::several(12, Some("12 volumes"))]
    fn counts_the_volumes(#[case] volumes: usize, #[case] text: Option<&str>) {
        let card = NamespaceCard {
            name: "shop".into(),
            why: None,
            gotcha: None,
            provides: None,
            docs: None,
            health: Health::default(),
            reason_pages: Vec::new(),
            hosts: Vec::new(),
            chips: Vec::new(),
            volumes,
            depends: Default::default(),
            code: Default::default(),
            managed: Default::default(),
            loops: Vec::new(),
            calls: Default::default(),
            holds: Default::default(),
            layer_named: None,
            logged: Vec::new(),
        };
        assert_eq!(card.volumes_text().as_deref(), text);
    }
}
