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
    /// Why the sidebar's "Now" list has no alerts, when it has none.
    pub alert_note: Option<transect_resources::resources::OffLine>,
    pub concerns: Vec<Concern>,
    pub findings: Vec<Finding>,
    /// The newest changes of the last day.
    pub changed: Listed,
    /// The note shown when the page was asked to draw machines as lost
    /// and none of them is a machine of the cluster, so no loss is drawn.
    pub unknown: Option<String>,
}

/// A namespace whose health is a notice, a warning, or a failure, with
/// its reasons.
#[derive(Debug, PartialEq)]
pub struct Concern {
    pub namespace: String,
    pub health: Health,
    pub lines: Vec<Line>,
}

/// One reason, with the page that its link opens.
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
    /// The CSS class of the concern's dot: filled for a failure, outlined
    /// for a warning, and outlined in gray for a notice.  These match how
    /// the map draws the namespace's rectangle.
    pub fn level_class(&self) -> &'static str {
        match self.health.level {
            Level::Failing => "concern-failing",
            Level::Notice => "concern-notice",
            _ => "concern-warning",
        }
    }
}

impl Sidebar {
    /// Builds a concern for each namespace that the map colors, so the
    /// sidebar and the map agree.  The worst come first, then by name.
    /// The sidebar lists the alerts above the concerns, so a concern
    /// leaves out its alert reasons, and its level comes from the reasons
    /// left.  A namespace that only alerts make unhealthy has no concern.
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
            .flat_map(|band| &band.tiles)
            .filter(|tile| tile.health.level > Level::Healthy)
            .filter_map(|tile| {
                let reasons = tile.health.reasons.iter();
                let reasons = reasons.filter(|reason| !matches!(reason, Reason::Alert { .. }));
                let health = Health::from_reasons(reasons.cloned().collect());
                let namespace = &tile.namespace;
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

    use super::super::cluster::{Band, NamespaceTile};
    use super::*;
    use transect_graph::edges::Object;
    use transect_observe::health::Origin;

    fn tile(namespace: &str, reasons: Vec<Reason>) -> NamespaceTile {
        NamespaceTile {
            namespace: namespace.into(),
            workloads: 1,
            health: Health::from_reasons(reasons),
            selected: false,
            relations: Vec::new(),
            looped: false,
            loss: None,
        }
    }

    fn band(tiles: Vec<NamespaceTile>) -> Band {
        Band {
            name: "apps".into(),
            tiles,
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
                band(vec![tile("shop", vec![pending()]), tile("blog", vec![])]),
                band(vec![
                    tile("dns", vec![pending()]),
                    tile("queue", vec![none_ready()]),
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
            bands: vec![band(vec![tile("blog", vec![])])],
        };
        assert_eq!(build(&map).concerns, Vec::new());
    }

    #[rstest]
    fn a_concern_leaves_its_alerts_to_the_list_above() {
        let map = ClusterMap {
            bands: vec![band(vec![
                tile("shop", vec![alert(Level::Failing), pending()]),
                tile("blog", vec![alert(Level::Failing)]),
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
