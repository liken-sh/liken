//! What the firing alerts add to a render: a reason in the health of
//! each namespace that an alert is about, a mark on each machine's
//! strip, and the list at the top of "Now".

use std::collections::{BTreeMap, HashMap};

use k8s_openapi::jiff::Timestamp;

use super::{Alert, Known, Target, target};
use crate::health::{Level, Reason, minutes_between, times};
use transect_graph::edges::Object;
use transect_resources::knowledge::age;

/// A reason for each alert about a namespace, paired with the
/// namespace's name.  The reason's subject is the object that the alert
/// names, or the namespace when it names none, so the alert shows on
/// the namespace's card and on the card of the workload that owns the
/// object.
pub fn reasons(alerts: &[Alert], known: &Known, now: Timestamp) -> Vec<(String, Reason)> {
    alerts
        .iter()
        .filter_map(|alert| {
            let Target::Namespace {
                namespace,
                object,
                container,
            } = target(&alert.labels, known)
            else {
                return None;
            };
            let subject = object.unwrap_or_else(|| Object::new("", "Namespace", None, &namespace));
            let reason = Reason::Alert {
                subject,
                container,
                name: alert.name.clone(),
                summary: alert.summary.clone(),
                level: alert.level,
                minutes_ago: alert.starts_at.map(|at| minutes_between(at, now)),
            };
            Some((namespace, reason))
        })
        .collect()
}

/// The alerts about one machine, which mark its strip.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Alerted {
    /// The worst level among them.
    pub level: Level,
    /// Their names, sorted, each once.
    pub names: Vec<String>,
}

impl Alerted {
    pub fn tag(&self) -> String {
        times(self.names.len() as i64, "alert")
    }

    pub fn title(&self) -> String {
        self.names.join(", ")
    }

    /// The tag's class.  A failure fills the tag and a warning outlines
    /// it.  Any other level gets no class, so the tag of a notice stays
    /// faint.
    pub fn tag_class(&self) -> &'static str {
        match self.level {
            Level::Failing => "strip-tag-warm",
            Level::Warning => "strip-tag-alert",
            _ => "",
        }
    }
}

/// The alerts about each machine, keyed by the name of its `Node`.
pub fn on_machines(alerts: &[Alert], known: &Known) -> HashMap<String, Alerted> {
    let mut on: HashMap<String, Alerted> = HashMap::new();
    for alert in alerts {
        let Target::Machine(machine) = target(&alert.labels, known) else {
            continue;
        };
        let alerted = on.entry(machine).or_insert(Alerted {
            level: alert.level,
            names: Vec::new(),
        });
        alerted.level = alerted.level.max(alert.level);
        if !alerted.names.contains(&alert.name) {
            alerted.names.push(alert.name.clone());
            alerted.names.sort();
        }
    }
    on
}

/// The thing that an alert is about, as one line of "Now" shows it.
#[derive(Clone, Debug, PartialEq, Eq, PartialOrd, Ord)]
pub enum About {
    Namespace {
        namespace: String,
        object: Option<String>,
    },
    Machine(String),
    Cluster,
}

impl About {
    fn of(target: Target) -> Self {
        match target {
            Target::Namespace {
                namespace, object, ..
            } => Self::Namespace {
                namespace,
                object: object.map(|object| object.name),
            },
            Target::Machine(machine) => Self::Machine(machine),
            Target::Cluster => Self::Cluster,
        }
    }

    pub fn text(&self) -> String {
        match self {
            Self::Namespace {
                namespace,
                object: Some(object),
            } => format!("{namespace} · {object}"),
            Self::Namespace {
                namespace,
                object: None,
            } => namespace.clone(),
            Self::Machine(machine) => format!("machine {machine}"),
            Self::Cluster => "cluster-wide".into(),
        }
    }

    /// The page that a link to the alert's subject opens: the
    /// namespace's page for a namespace or an object in it, and the
    /// machine's page for a machine.  An alert about the whole cluster
    /// has no link.
    pub fn href(&self) -> Option<String> {
        match self {
            Self::Namespace { namespace, .. } => Some(format!("/namespaces/{namespace}")),
            Self::Machine(machine) => Some(format!("/machines/{machine}")),
            Self::Cluster => None,
        }
    }
}

/// One line of "Now", which groups the alerts that have the same name,
/// level, and subject.  So an alert that fires once for each image or
/// each pod shows as one line with a count.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Firing {
    pub name: String,
    pub severity: Option<String>,
    pub level: Level,
    pub about: About,
    pub count: usize,
    /// The summary and the description, each kept only when every
    /// alert of the line has the same value.
    pub summary: Option<String>,
    pub description: Option<String>,
    /// Minutes since the earliest of them started.
    pub minutes: Option<i64>,
    pub link: Option<String>,
}

impl Firing {
    /// The dot's class: filled for a failure and outlined for a
    /// warning, in the same style that "Now" uses for a namespace's
    /// concern.
    pub fn level_class(&self) -> &'static str {
        match self.level {
            Level::Failing => "concern-failing",
            Level::Notice | Level::Healthy => "concern-notice",
            Level::Warning => "concern-warning",
        }
    }

    pub fn line(&self) -> String {
        let about = self.about.text();
        let count = match self.count {
            1 => String::new(),
            count => format!(", {count} alerts"),
        };
        match self.minutes {
            Some(minutes) => format!("{about}{count}, firing for {}", age(minutes)),
            None => format!("{about}{count}"),
        }
    }
}

/// Every firing alert as the lines of "Now", the worst first, then by
/// name and by what they are about.
pub fn firing(alerts: &[Alert], known: &Known, now: Timestamp) -> Vec<Firing> {
    let mut lines: BTreeMap<(std::cmp::Reverse<Level>, String, About), Firing> = BTreeMap::new();
    for alert in alerts {
        let about = About::of(target(&alert.labels, known));
        let key = (
            std::cmp::Reverse(alert.level),
            alert.name.clone(),
            about.clone(),
        );
        let minutes = alert.starts_at.map(|at| minutes_between(at, now));
        let line = lines.entry(key).or_insert_with(|| Firing {
            name: alert.name.clone(),
            severity: alert.severity.clone(),
            level: alert.level,
            about,
            count: 0,
            summary: alert.summary.clone(),
            description: alert.description.clone(),
            minutes,
            link: alert.link.clone(),
        });
        line.count += 1;
        line.minutes = line.minutes.max(minutes);
        if line.summary != alert.summary {
            line.summary = None;
        }
        if line.description != alert.description {
            line.description = None;
        }
    }
    lines.into_values().collect()
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::super::fixtures::alert;
    use super::*;

    const NOW: &str = "2026-01-01T12:30:00Z";

    fn known() -> Known {
        Known {
            namespaces: ["shop"].map(Into::into).into(),
            nodes: vec![("alpha".into(), vec!["10.0.0.1".into()])],
            source: crate::alerts::source::fixtures::source(),
        }
    }

    fn now() -> Timestamp {
        NOW.parse().unwrap()
    }

    #[rstest]
    fn an_alert_about_a_pod_is_a_reason_in_its_namespace() {
        let alerts = [
            alert(
                "WebCrashing",
                "critical",
                &[
                    ("namespace", "shop"),
                    ("pod", "web-0"),
                    ("container", "app"),
                ],
            ),
            alert("DiskHot", "warning", &[("node", "alpha")]),
        ];
        let reasons = reasons(&alerts, &known(), now());
        let lines: Vec<(String, String, Level)> = reasons
            .iter()
            .map(|(namespace, reason)| (namespace.clone(), reason.to_string(), reason.level()))
            .collect();
        assert_eq!(
            lines,
            vec![(
                "shop".into(),
                "app in web-0: alert WebCrashing, firing for 30m".into(),
                Level::Failing
            )]
        );
        assert_eq!(
            reasons[0].1.subject("shop"),
            Object::new("", "Pod", Some("shop"), "web-0")
        );
    }

    #[rstest]
    fn marks_each_machine_with_its_alerts() {
        let alerts = [
            alert("DiskHot", "warning", &[("node", "alpha")]),
            alert("MemoryLow", "critical", &[("instance", "10.0.0.1:9100")]),
            alert(
                "DiskHot",
                "warning",
                &[("node", "alpha"), ("device", "sdb")],
            ),
            alert("QueueFull", "critical", &[]),
        ];
        let on = on_machines(&alerts, &known());
        let alpha = &on["alpha"];
        assert_eq!(
            (on.len(), alpha.level, alpha.tag(), alpha.title()),
            (
                1,
                Level::Failing,
                "2 alerts".into(),
                "DiskHot, MemoryLow".into()
            )
        );
    }

    #[rstest]
    fn lists_the_worst_first_and_folds_repeats() {
        let mut first = alert("ImageVulnerable", "warning", &[("image", "web")]);
        first.starts_at = "2026-01-01T11:00:00Z".parse().ok();
        let alerts = [
            alert(
                "PodNotReady",
                "warning",
                &[("namespace", "shop"), ("pod", "web-0")],
            ),
            first,
            alert("ImageVulnerable", "warning", &[("image", "db")]),
            alert("Info", "info", &[]),
            alert("MemoryLow", "critical", &[("node", "alpha")]),
        ];
        let lines: Vec<(String, String, Option<String>)> = firing(&alerts, &known(), now())
            .into_iter()
            .map(|line| (line.name.clone(), line.line(), line.summary))
            .collect();
        assert_eq!(
            lines,
            vec![
                (
                    "MemoryLow".into(),
                    "machine alpha, firing for 30m".into(),
                    Some("MemoryLow summary".into())
                ),
                (
                    "ImageVulnerable".into(),
                    "cluster-wide, 2 alerts, firing for 1h".into(),
                    Some("ImageVulnerable summary".into())
                ),
                (
                    "PodNotReady".into(),
                    "shop · web-0, firing for 30m".into(),
                    Some("PodNotReady summary".into())
                ),
                (
                    "Info".into(),
                    "cluster-wide, firing for 30m".into(),
                    Some("Info summary".into())
                ),
            ]
        );
    }

    #[rstest]
    fn a_folded_line_keeps_only_text_they_share() {
        let mut other = alert("ImageVulnerable", "warning", &[("image", "db")]);
        other.summary = Some("db is vulnerable".into());
        let alerts = [alert("ImageVulnerable", "warning", &[]), other];
        let lines = firing(&alerts, &known(), now());
        assert_eq!((lines[0].count, lines[0].summary.clone()), (2, None));
    }
}
