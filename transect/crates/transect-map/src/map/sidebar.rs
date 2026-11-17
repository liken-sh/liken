//! The cluster page's sidebar, "What to know now": the firing alerts,
//! the namespaces whose health needs attention, then the findings worth
//! knowing, and what changed over the last day.

use super::ClusterMap;
use transect_cluster::ownership::Ownership;
use transect_graph::edges::Plurals;
use transect_observe::alerts::Firing;
use transect_observe::health::{Health, Level, Reason};
use transect_supply::changed::Listed;
use transect_supply::findings::Finding;

#[derive(Debug)]
pub struct Sidebar {
    pub alerts: Vec<Firing>,
    /// Why "Now" lists no alerts, when it lists none.
    pub alert_note: Option<transect_resources::resources::OffLine>,
    pub concerns: Vec<Concern>,
    pub findings: Vec<Finding>,
    /// The newest changes of the last day.
    pub changed: Listed,
    /// Why a requested loss draws nothing, which happens when it names no
    /// machine the cluster has.
    pub unknown: Option<String>,
}

/// A namespace that is warning or failing, with its reasons.
#[derive(Debug, PartialEq)]
pub struct Concern {
    pub namespace: String,
    pub health: Health,
    /// Each reason with the page of what it is about.
    pub lines: Vec<Line>,
}

/// One reason, with the page a link to it opens: its workload's or its
/// pod's, or the namespace's when it is about neither.
#[derive(Debug, PartialEq)]
pub struct Line {
    pub reason: Reason,
    pub href: String,
}

/// The page of what `reason`, one of `namespace`'s, is about: the pod's
/// page for a pod, its top owner's for anything a workload owns, and
/// the namespace's otherwise.
fn href(reason: &Reason, namespace: &str, ownership: &Ownership, plurals: &Plurals) -> String {
    let subject = reason.subject(namespace);
    let found = match subject.kind.as_str() {
        "Pod" => transect_graph::links::link(&subject, plurals),
        _ => transect_graph::links::link(&ownership.top_owner(&subject), plurals),
    };
    found.unwrap_or_else(|| format!("/namespaces/{namespace}"))
}

impl Concern {
    /// Fills the dot for a failure and outlines it for a warning, as the
    /// map does a parcel, and outlines a notice in gray, as the map
    /// leaves a notice's parcel gray.
    pub fn level_class(&self) -> &'static str {
        match self.health.level {
            Level::Failing => "concern-failing",
            Level::Notice => "concern-notice",
            _ => "concern-warning",
        }
    }
}

impl Sidebar {
    /// Reads the concerns from the map's parcels, so the sidebar names
    /// the namespaces the map colors.  The worst come first, and those
    /// at one level sort by name.  The sidebar lists the alerts above the
    /// concerns, so a concern leaves its alerts out, and a namespace
    /// colored by its alerts alone has no concern of its own.
    pub fn build(
        map: &ClusterMap,
        findings: Vec<Finding>,
        alerts: Vec<Firing>,
        alert_note: Option<transect_resources::resources::OffLine>,
        (ownership, plurals): (&Ownership, &Plurals),
    ) -> Self {
        let mut concerns: Vec<Concern> = map
            .bands
            .iter()
            .flat_map(|band| &band.parcels)
            .filter(|parcel| parcel.health.level > Level::Healthy)
            .filter_map(|parcel| {
                let reasons = parcel.health.reasons.iter();
                let reasons = reasons.filter(|reason| !matches!(reason, Reason::Alert { .. }));
                let health = Health::from_reasons(reasons.cloned().collect());
                let namespace = &parcel.namespace;
                let lines = health.reasons.iter().map(|reason| Line {
                    href: href(reason, namespace, ownership, plurals),
                    reason: reason.clone(),
                });
                let lines = lines.collect();
                (health.level > Level::Healthy).then(|| Concern {
                    namespace: namespace.clone(),
                    health,
                    lines,
                })
            })
            .collect();
        concerns.sort_by(|a, b| {
            b.health
                .level
                .cmp(&a.health.level)
                .then_with(|| a.namespace.cmp(&b.namespace))
        });
        Self {
            changed: Listed::default(),
            unknown: None,
            alerts,
            alert_note,
            concerns,
            findings,
        }
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::super::cluster::{Band, Parcel};
    use super::*;
    use transect_graph::edges::Object;
    use transect_observe::health::Origin;

    fn parcel(namespace: &str, reasons: Vec<Reason>) -> Parcel {
        Parcel {
            namespace: namespace.into(),
            workloads: 1,
            health: Health::from_reasons(reasons),
            selected: false,
            relations: Vec::new(),
            looped: false,
            loss: None,
        }
    }

    fn band(parcels: Vec<Parcel>) -> Band {
        Band {
            name: "apps".into(),
            parcels,
        }
    }

    fn pending() -> Reason {
        Reason::PodPending {
            pod: "web-0".into(),
            minutes: 9,
        }
    }

    fn none_ready() -> Reason {
        Reason::NoReadyReplicas {
            workload: transect_observe::health::Workload {
                kind: transect_observe::health::Kind::Deployment,
                name: "web".into(),
            },
            desired: 1,
        }
    }

    fn alert(level: Level) -> Reason {
        Reason::Alert {
            subject: transect_graph::edges::Object::new("", "Namespace", None, "shop"),
            container: None,
            name: "WebDown".into(),
            summary: None,
            level,
            minutes_ago: Some(3),
        }
    }

    fn build(map: &ClusterMap) -> Sidebar {
        let plurals = Plurals::default();
        Sidebar::build(
            map,
            Vec::new(),
            Vec::new(),
            None,
            (&Ownership::default(), &plurals),
        )
    }

    #[rstest]
    fn lists_the_worst_namespaces_first_across_bands() {
        let map = ClusterMap {
            bands: vec![
                band(vec![
                    parcel("shop", vec![pending()]),
                    parcel("blog", vec![]),
                ]),
                band(vec![
                    parcel("dns", vec![pending()]),
                    parcel("queue", vec![none_ready()]),
                ]),
            ],
        };
        let sidebar = build(&map);
        let names: Vec<(&str, &str)> = sidebar
            .concerns
            .iter()
            .map(|concern| (concern.namespace.as_str(), concern.level_class()))
            .collect();
        assert_eq!(
            names,
            vec![
                ("queue", "concern-failing"),
                ("dns", "concern-warning"),
                ("shop", "concern-warning"),
            ]
        );
    }

    #[rstest]
    fn a_healthy_map_has_no_concerns() {
        let map = ClusterMap {
            bands: vec![band(vec![parcel("blog", vec![])])],
        };
        assert_eq!(build(&map).concerns, Vec::new());
    }

    /// The sidebar lists the alerts above the concerns, so a concern does
    /// not repeat them, and its level is that of the reasons left.
    #[rstest]
    fn a_concern_leaves_its_alerts_to_the_list_above() {
        let map = ClusterMap {
            bands: vec![band(vec![
                parcel("shop", vec![alert(Level::Failing), pending()]),
                parcel("blog", vec![alert(Level::Failing)]),
            ])],
        };
        let concerns: Vec<(String, Level, usize)> = build(&map)
            .concerns
            .into_iter()
            .map(|c| (c.namespace, c.health.level, c.health.reasons.len()))
            .collect();
        assert_eq!(concerns, vec![("shop".into(), Level::Warning, 1)]);
    }

    fn matched(origin: Origin) -> Reason {
        Reason::Matched {
            origin,
            pattern: "refused".into(),
            meaning: "It cannot connect.".into(),
            matches: 3,
            window: "1h".into(),
            minutes_ago: 1,
            evidence: transect_observe::logs::evidence::Evidence::default(),
            settled: false,
        }
    }

    #[rstest]
    #[case::a_pod(pending(), "/namespaces/shop/pods/web-0")]
    #[case::a_workload(none_ready(), "/namespaces/shop/deployments/web")]
    #[case::a_patterns_workload(
        matched(Origin::Workload(Object::new("apps", "StatefulSet", Some("shop"), "db"))),
        "/namespaces/shop/statefulsets/db"
    )]
    #[case::a_patterns_gone_pod(matched(Origin::GonePod("db-9".into())), "/namespaces/shop")]
    #[case::a_patterns_unnamed_pod(matched(Origin::NoPod), "/namespaces/shop")]
    fn each_reason_links_to_what_it_is_about(#[case] reason: Reason, #[case] expected: &str) {
        let found = href(&reason, "shop", &Ownership::default(), &Plurals::default());
        assert_eq!(found, expected);
    }
}
