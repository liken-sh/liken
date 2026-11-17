//! The text of "What happens next".

use super::{duration, list};
use crate::loss::sidebar::Sidebar;
use crate::loss::{Next, Stranded};
use crate::placement::Eviction;
use transect_graph::links::{Piece, listed};

/// The most pods the eviction line names before it counts the rest.
const NAMED: usize = 4;

/// The out-of-service taint, whose value gives the reason the machine
/// is out of service.
const OUT_OF_SERVICE: &str = "node.kubernetes.io/out-of-service=nodeshutdown:NoExecute";

impl Sidebar {
    pub fn next_heading(&self) -> &'static str {
        "What happens next"
    }

    /// The eviction line's tag, "+5m", and its text, "Its pods are
    /// evicted after the 5 minutes their tolerations allow, then the
    /// replicas that can start elsewhere do."  For machines that are
    /// really lost, the time counts from the loss: "+2m" while the wait
    /// runs, and "done" after it ends.
    pub fn eviction(&self) -> Option<(String, String)> {
        if let (Some(elapsed), Some(Eviction::After(seconds))) = (self.elapsed(), self.next.usual) {
            return Some(self.eviction_since(seconds, elapsed));
        }
        let (pronoun, _) = self.machines.pronoun();
        let its = match pronoun {
            "it" => "Its",
            _ => "Their",
        };
        let then = "then the replicas that can start elsewhere do.";
        match self.next.usual? {
            Eviction::After(0) => Some((
                "now".into(),
                format!("{its} pods are evicted at once, {then}"),
            )),
            Eviction::After(seconds) => Some((
                tag(seconds),
                format!(
                    "{its} pods are evicted after the {} their tolerations allow, {then}",
                    duration(seconds)
                ),
            )),
            Eviction::Never => None,
        }
    }

    /// The eviction line for machines lost `elapsed` seconds ago, whose
    /// pods are evicted after `seconds`.
    fn eviction_since(&self, seconds: i64, elapsed: i64) -> (String, String) {
        let its = match self.machines.pronoun() {
            ("it", _) => "Its",
            _ => "Their",
        };
        let allow = duration(seconds);
        match seconds - elapsed {
            left if left > 0 => (
                tag(left),
                format!(
                    "{its} pods are evicted in about {}, when the {allow} their tolerations allow run out, then the replicas that can start elsewhere do.",
                    duration(left)
                ),
            ),
            _ => (
                "done".into(),
                format!(
                    "{its} pods were evicted after the {allow} their tolerations allow, and the replicas that can start elsewhere do."
                ),
            ),
        }
    }

    /// The pods that Kubernetes evicts after a different time: "Not
    /// every pod waits as long: db-0 after 30 seconds, and web-a after
    /// 1 minute."
    pub fn differ_text(&self) -> Option<String> {
        let differ = &self.next.differ;
        if differ.is_empty() {
            return None;
        }
        let mut named: Vec<String> = differ
            .iter()
            .take(NAMED)
            .map(|(pod, eviction)| match eviction {
                Eviction::After(0) => format!("{pod} at once"),
                Eviction::After(seconds) => format!("{pod} after {}", duration(*seconds)),
                Eviction::Never => format!("{pod} never"),
            })
            .collect();
        if differ.len() > NAMED {
            named.push(format!("{} more", differ.len() - NAMED));
        }
        let named = list(named.iter().map(String::as_str));
        Some(format!("Not every pod waits as long: {named}."))
    }

    /// "If alpha is really off, this deletes its pods and detaches
    /// their volumes:".  The sidebar shows it only when the machines are
    /// really down and a pod on them is stuck.
    pub fn out_of_service_text(&self) -> Option<String> {
        if !self.next.really_lost || self.next.stuck.is_empty() {
            return None;
        }
        let machines = &self.machines;
        Some(match self.machines.pronoun() {
            ("it", _) => {
                format!(
                    "If {machines} is really off, this deletes its pods and detaches their volumes:"
                )
            }
            _ => format!(
                "If {machines} are really off, this deletes their pods and detaches their volumes:"
            ),
        })
    }

    /// The command for each lost machine, for the reader to run.
    pub fn out_of_service_commands(&self) -> Vec<String> {
        let machines = self.machines.names();
        machines
            .map(|machine| format!("kubectl taint node {machine} {OUT_OF_SERVICE}"))
            .collect()
    }

    pub fn stuck_tag(&self) -> &'static str {
        "stuck"
    }

    /// One line for each reason pods stay stuck, naming the workloads.
    pub fn stuck_lines(&self) -> Vec<Vec<Piece>> {
        let lost = &self.machines;
        let one = lost.names().count() == 1;
        let theirs = if one {
            format!("{lost}'s")
        } else {
            "the lost machines'".into()
        };
        self.next
            .stuck
            .iter()
            .map(|(reason, names)| {
                let why = match reason {
                    Stranded::Disk { .. } => {
                        format!("Pods whose volume is on {theirs} own disk can't start elsewhere: ")
                    }
                    Stranded::Attached { .. } => format!(
                        "Pods whose volume is still attached to {lost} can't start elsewhere: "
                    ),
                    Stranded::Device { .. } => {
                        format!("Pods that hold a device on {lost} can't start elsewhere: ")
                    }
                    Stranded::NoFit => {
                        "Pods that no other machine fits can't start elsewhere: ".into()
                    }
                    Stranded::StatefulSet { .. } => format!(
                        "A StatefulSet replaces its pod only once {lost} confirms it gone: "
                    ),
                    Stranded::NeverEvicted => {
                        "Pods that tolerate a lost machine are never evicted: ".into()
                    }
                };
                let mut pieces = vec![Piece::Words(why)];
                pieces.extend(listed(names.iter().map(|n| vec![n.piece()]).collect()));
                pieces.push(Piece::words("."));
                pieces
            })
            .collect()
    }
}

impl Next {
    pub fn is_empty(&self) -> bool {
        self.usual.is_none() && self.stuck.is_empty()
    }
}

/// The eviction's tag: "+30s", "+5m".
fn tag(seconds: i64) -> String {
    match seconds {
        s if s % 60 == 0 || s >= 120 => format!("+{}m", (s + 30) / 60),
        s => format!("+{s}s"),
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;
    use crate::loss::Lost;

    fn sidebar(machines: &[&str], next: Next) -> Sidebar {
        Sidebar {
            machines: Lost::of(machines.iter().copied(), machines.iter().copied()),
            pods: 0,
            stuck: Vec::new(),
            back: Vec::new(),
            vital: Vec::new(),
            primary: Vec::new(),
            short: Vec::new(),
            folded: 0,
            daemon_sets: 0,
            cascade: Vec::new(),
            unreconciled: Vec::new(),
            plurals: Default::default(),
            gone: Vec::new(),
            majority: None,
            unknown: None,
            blind: None,
            alerting: None,
            linker: Default::default(),
            next,
        }
    }

    fn next(usual: Option<Eviction>, differ: &[(&str, Eviction)]) -> Next {
        Next {
            usual,
            differ: differ.iter().map(|(p, e)| ((*p).into(), *e)).collect(),
            ..Next::default()
        }
    }

    const FIVE: Option<Eviction> = Some(Eviction::After(300));

    #[rstest]
    #[case::admissions_default(
        &["alpha"],
        next(FIVE, &[]),
        Some(("+5m", "Its pods are evicted after the 5 minutes their tolerations allow, then the replicas that can start elsewhere do.")),
        None
    )]
    #[case::some_differ(
        &["alpha", "beta"],
        next(FIVE, &[("db-0", Eviction::After(30)), ("web-a", Eviction::After(0))]),
        Some(("+5m", "Their pods are evicted after the 5 minutes their tolerations allow, then the replicas that can start elsewhere do.")),
        Some("Not every pod waits as long: db-0 after 30 seconds and web-a at once.")
    )]
    #[case::at_once(
        &["alpha"],
        next(Some(Eviction::After(0)), &[]),
        Some(("now", "Its pods are evicted at once, then the replicas that can start elsewhere do.")),
        None
    )]
    #[case::only_daemon_sets(&["alpha"], next(None, &[]), None, None)]
    fn says_when_the_pods_are_evicted(
        #[case] machines: &[&str],
        #[case] next: Next,
        #[case] eviction: Option<(&str, &str)>,
        #[case] differ: Option<&str>,
    ) {
        let sidebar = sidebar(machines, next);
        let found = sidebar.eviction();
        let found = found.as_ref().map(|(t, s)| (t.as_str(), s.as_str()));
        assert_eq!(
            (found, sidebar.differ_text().as_deref()),
            (eviction, differ)
        );
    }

    #[rstest]
    fn folds_the_pods_that_differ_past_four() {
        let late = Eviction::After(30);
        let pods = [
            ("a", late),
            ("b", late),
            ("c", late),
            ("d", late),
            ("e", late),
            ("f", late),
        ];
        let text = sidebar(&["alpha"], next(FIVE, &pods)).differ_text();
        assert_eq!(
            text.as_deref(),
            Some(
                "Not every pod waits as long: a after 30 seconds, b after 30 seconds, c after 30 seconds, d after 30 seconds, and 2 more."
            )
        );
    }

    fn named(name: &str) -> crate::loss::sidebar::Named {
        crate::loss::sidebar::Named {
            shown: name.into(),
            object: transect_graph::edges::Object::new("apps", "Deployment", Some("shop"), name),
        }
    }

    fn stuck(really_lost: bool) -> Next {
        Next {
            stuck: vec![
                (
                    Stranded::Attached {
                        kind: crate::storage::Type::Iscsi,
                        machine: "alpha".into(),
                    },
                    vec![named("db"), named("wiki")],
                ),
                (
                    Stranded::StatefulSet {
                        machine: "alpha".into(),
                    },
                    vec![named("cache")],
                ),
            ],
            really_lost,
            ..Next::default()
        }
    }

    #[rstest]
    #[case::supposed(false, None)]
    #[case::really(
        true,
        Some("If alpha is really off, this deletes its pods and detaches their volumes:")
    )]
    fn names_the_stuck_and_offers_the_taint_only_when_really_lost(
        #[case] really_lost: bool,
        #[case] out_of_service: Option<&str>,
    ) {
        let sidebar = sidebar(&["alpha"], stuck(really_lost));
        assert_eq!(
            (
                sidebar.stuck_lines().iter().map(|line| transect_graph::links::text(line)).collect::<Vec<_>>(),
                sidebar.out_of_service_text().as_deref()
            ),
            (
                vec![
                    "Pods whose volume is still attached to alpha can't start elsewhere: db and wiki.".to_string(),
                    "A StatefulSet replaces its pod only once alpha confirms it gone: cache.".to_string(),
                ],
                out_of_service
            )
        );
        assert_eq!(
            sidebar.out_of_service_commands(),
            vec![
                "kubectl taint node alpha node.kubernetes.io/out-of-service=nodeshutdown:NoExecute"
            ]
        );
    }

    #[rstest]
    #[case::seconds(30, "+30s")]
    #[case::a_minute(60, "+1m")]
    #[case::ninety_seconds(90, "+90s")]
    #[case::five_minutes(300, "+5m")]
    #[case::rounded(150, "+3m")]
    fn tags_the_eviction(#[case] seconds: i64, #[case] text: &str) {
        assert_eq!(tag(seconds), text);
    }
}
