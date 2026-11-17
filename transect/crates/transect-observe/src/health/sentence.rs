//! The sentence that each reason renders as.  These templates hold the
//! only fixed words that a finding shows, and every other part of the
//! sentence is a name or a number from the reason.

use std::fmt;

use transect_resources::knowledge::age;

use super::{Origin, Reason, Seen};
use crate::logs::evidence::Evidence;

impl fmt::Display for Reason {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::NoReadyReplicas { workload, desired } => {
                write!(f, "{}: none of {desired} ready", workload.name)
            }
            Self::Starting { workload, desired } => {
                write!(f, "{}: none of {desired} ready yet", workload.name)
            }
            Self::NotReady {
                workload,
                ready,
                desired,
            } => write!(f, "{}: {ready} of {desired} ready", workload.name),
            Self::CrashLooping {
                pod,
                container,
                restarts,
            } => write!(
                f,
                "{container} in {pod} is crash looping after {}",
                times(*restarts, "restart")
            ),
            Self::Restarting {
                pod,
                container,
                restarts,
                minutes_ago,
                clean: false,
            } => write!(
                f,
                "{container} in {pod} restarted {} ago, {} in all",
                age(*minutes_ago),
                times(*restarts, "restart")
            ),
            Self::Restarting {
                pod,
                container,
                restarts: ..2,
                minutes_ago,
                clean: true,
            } => write!(
                f,
                "{container} in {pod} exited cleanly and restarted {} ago",
                age(*minutes_ago),
            ),
            Self::Restarting {
                pod,
                container,
                restarts,
                minutes_ago,
                clean: true,
            } => write!(
                f,
                "{container} in {pod} exited cleanly and restarted {} ago, {} in all",
                age(*minutes_ago),
                times(*restarts, "restart")
            ),
            Self::RestartedAcross {
                workload,
                container,
                pods,
                of,
                minutes_ago,
                clean,
                ..
            } => write!(
                f,
                "{container} {} in {pods} of {of} {} pods, {} ago",
                if *clean {
                    "exited cleanly and restarted"
                } else {
                    "restarted"
                },
                workload.name,
                age(*minutes_ago),
            ),
            Self::PodPending { pod, minutes } => {
                write!(f, "{pod} pending for {}", age(*minutes))
            }
            Self::LastRunFailed { workload } => write!(f, "{}: last run failed", workload.name),
            Self::Condition {
                workload,
                condition,
                status: None,
                ..
            } => write!(f, "{}: reports no {condition} condition", workload.name),
            Self::Condition {
                workload,
                condition,
                status: Some(status),
                reason,
                message,
            } => {
                write!(f, "{}: {condition} is {status}", workload.name)?;
                if let Some(reason) = reason {
                    write!(f, " ({reason})")?;
                }
                if let Some(message) = message {
                    write!(f, ": {message}")?;
                }
                Ok(())
            }
            Self::LoggedErrors {
                workload,
                errors,
                minutes_ago,
                seen: Seen::Read { lines, .. },
                ..
            } => write!(
                f,
                "{}: {} seen in the last {lines} lines read {} ago",
                workload.name,
                times(*errors as i64, "error line"),
                age(*minutes_ago),
            ),
            Self::LoggedErrors {
                workload,
                errors,
                minutes_ago,
                seen: Seen::Searched { window, .. },
                ..
            } => write!(
                f,
                "{}: {} in the last {window}, the latest {} ago",
                workload.name,
                times(*errors as i64, "error line"),
                age(*minutes_ago),
            ),
            Self::Matched {
                origin,
                pattern,
                matches,
                window,
                minutes_ago,
                settled,
                ..
            } => {
                if let Origin::Workload(workload) = origin {
                    write!(f, "{}: ", workload.name)?;
                }
                write!(
                    f,
                    "{pattern}: {} in the last {window}, the latest {} ago",
                    times(*matches as i64, "matching line"),
                    age(*minutes_ago),
                )?;
                match origin {
                    Origin::Workload(_) if *settled => write!(f, ", {SETTLED}"),
                    Origin::Workload(_) => Ok(()),
                    Origin::GonePod(pod) => write!(f, ", from {pod}, {GONE_POD}"),
                    Origin::NoPod => write!(f, ", {FROM_NO_POD}"),
                }
            }
            Self::Alert {
                subject,
                container,
                name,
                minutes_ago,
                ..
            } => {
                match (subject.kind.as_str(), container) {
                    ("Namespace", _) => {}
                    ("Pod", Some(container)) => write!(f, "{container} in {}: ", subject.name)?,
                    _ => write!(f, "{}: ", subject.name)?,
                }
                match minutes_ago {
                    Some(minutes) => {
                        write!(f, "alert {name}, firing for {}", age(*minutes))
                    }
                    None => write!(f, "alert {name} is firing"),
                }
            }
        }
    }
}

impl Reason {
    /// Where a log reason's lines came from, with a few of the lines,
    /// when the search returned any.
    pub fn evidence(&self) -> Option<&Evidence> {
        match self {
            Self::Matched { evidence, .. } | Self::LoggedErrors { evidence, .. } => {
                (!evidence.is_empty()).then_some(evidence)
            }
            _ => None,
        }
    }

    /// A second line under the reason: the rollout a container's
    /// restarts followed, how a workload's error lines were counted,
    /// what a pattern's match means, or an alert's summary.
    pub fn note(&self) -> Option<String> {
        if let Self::Matched { meaning, .. } = self {
            return Some(meaning.clone());
        }
        if let Self::Alert { summary, .. } = self {
            return summary.clone();
        }
        if let Self::LoggedErrors {
            seen: Seen::Searched { source, .. },
            ..
        } = self
        {
            return Some(format!(
                "The LogSource {source} counted the lines its pods logged at the error level, across every pod"
            ));
        }
        if let Self::LoggedErrors {
            seen: Seen::Read { read, of, .. },
            ..
        } = self
        {
            let from = match (read, of) {
                (1, 1) => "Read from its only pod".to_string(),
                (read, of) if read == of => format!("Read from all {of} of its pods"),
                (read, of) => format!("Read from {read} of its {of} pods"),
            };
            return Some(format!(
                "{from}; transect reads a pod's logs only when its page opens"
            ));
        }
        let Self::RestartedAcross {
            workload,
            container,
            after: Some(after),
            ..
        } = self
        else {
            return None;
        };
        Some(format!(
            "{}'s {container} restarted on {} right after {} rolled",
            workload.name,
            times(after.machines as i64, "machine"),
            after.rolled
        ))
    }
}

/// The text after a `CronJob`'s matches when its last run succeeded.
pub const SETTLED: &str = "and its last run succeeded";

/// The words for the pod that a log search counted lines from, when
/// that pod is gone.
pub const GONE_POD: &str = "a pod that no longer exists";

/// Where a log search's lines came from, when the source named no pod.
pub const FROM_NO_POD: &str = "from a pod the log source does not name";

/// `count` and `noun`, with an "s" on the noun for any count but one.
pub fn times(count: impl Into<i64>, noun: &str) -> String {
    let count = count.into();
    let plural = count != 1;
    format!("{count} {noun}{}", if plural { "s" } else { "" })
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::super::{AfterRoll, Kind, Workload};
    use super::*;

    fn condition(status: Option<&str>, reason: Option<&str>, message: Option<&str>) -> Reason {
        Reason::Condition {
            workload: transect_graph::edges::Object::new(
                "widgets.example.com",
                "Widget",
                Some("shop"),
                "front-room",
            ),
            condition: "Screen".into(),
            status: status.map(Into::into),
            reason: reason.map(Into::into),
            message: message.map(Into::into),
        }
    }

    fn logged(errors: usize, read: usize, of: usize) -> Reason {
        Reason::LoggedErrors {
            workload: transect_graph::edges::Object::new("apps", "Deployment", Some("shop"), "web"),
            errors,
            minutes_ago: 3,
            seen: Seen::Read {
                lines: 100,
                read,
                of,
            },
            evidence: Evidence::default(),
        }
    }

    fn searched(errors: usize) -> Reason {
        Reason::LoggedErrors {
            workload: transect_graph::edges::Object::new("apps", "Deployment", Some("shop"), "web"),
            errors,
            minutes_ago: 3,
            seen: Seen::Searched {
                source: "logs".into(),
                window: "1h".into(),
            },
            evidence: Evidence::default(),
        }
    }

    fn matched(matches: usize) -> Reason {
        Reason::Matched {
            origin: Origin::GonePod("web-9".into()),
            pattern: "connection-refused".into(),
            meaning: "It cannot connect.".into(),
            matches,
            window: "1h".into(),
            minutes_ago: 2,
            evidence: Evidence::default(),
            settled: false,
        }
    }

    fn web(kind: Kind) -> Workload {
        Workload {
            kind,
            name: "web".into(),
        }
    }

    #[rstest]
    #[case::none_ready(
        Reason::NoReadyReplicas { workload: web(Kind::Deployment), desired: 2 },
        "web: none of 2 ready"
    )]
    #[case::starting(
        Reason::Starting { workload: web(Kind::Deployment), desired: 1 },
        "web: none of 1 ready yet"
    )]
    #[case::not_ready(
        Reason::NotReady { workload: web(Kind::StatefulSet), ready: 1, desired: 2 },
        "web: 1 of 2 ready"
    )]
    #[case::crash_looping(
        Reason::CrashLooping { pod: "web-1".into(), container: "app".into(), restarts: 1 },
        "app in web-1 is crash looping after 1 restart"
    )]
    #[case::restarting(
        Reason::Restarting {
            pod: "web-1".into(),
            container: "app".into(),
            restarts: 3,
            minutes_ago: 12,
            clean: false,
        },
        "app in web-1 restarted 12m ago, 3 restarts in all"
    )]
    #[case::exited_cleanly_once(
        Reason::Restarting {
            pod: "web-1".into(),
            container: "app".into(),
            restarts: 1,
            minutes_ago: 4,
            clean: true,
        },
        "app in web-1 exited cleanly and restarted 4m ago"
    )]
    #[case::exited_cleanly_again(
        Reason::Restarting {
            pod: "web-1".into(),
            container: "app".into(),
            restarts: 5,
            minutes_ago: 4,
            clean: true,
        },
        "app in web-1 exited cleanly and restarted 4m ago, 5 restarts in all"
    )]
    #[case::pending(
        Reason::PodPending { pod: "web-1".into(), minutes: 7 },
        "web-1 pending for 7m"
    )]
    #[case::last_run_failed(
        Reason::LastRunFailed { workload: web(Kind::CronJob) },
        "web: last run failed"
    )]
    #[case::a_condition_false(
        condition(Some("False"), Some("Absent"), Some("no monitor answers")),
        "front-room: Screen is False (Absent): no monitor answers"
    )]
    #[case::a_condition_unknown(
        condition(Some("Unknown"), None, None),
        "front-room: Screen is Unknown"
    )]
    #[case::a_condition_missing(
        condition(None, None, None),
        "front-room: reports no Screen condition"
    )]
    #[case::logged_errors(
        logged(4, 2, 3),
        "web: 4 error lines seen in the last 100 lines read 3m ago"
    )]
    #[case::logged_one_error(
        logged(1, 1, 1),
        "web: 1 error line seen in the last 100 lines read 3m ago"
    )]
    #[case::searched_errors(searched(12), "web: 12 error lines in the last 1h, the latest 3m ago")]
    #[case::a_pattern(
        matched(6),
        "connection-refused: 6 matching lines in the last 1h, the latest 2m ago, from web-9, a pod that no longer exists"
    )]
    #[case::a_pattern_once(
        matched(1),
        "connection-refused: 1 matching line in the last 1h, the latest 2m ago, from web-9, a pod that no longer exists"
    )]
    fn sentence(#[case] reason: Reason, #[case] text: &str) {
        assert_eq!(reason.to_string(), text);
    }

    fn across(after: Option<AfterRoll>) -> Reason {
        Reason::RestartedAcross {
            workload: transect_graph::edges::Object::new(
                "apps",
                "DaemonSet",
                Some("audio"),
                "speaker",
            ),
            container: "mixer".into(),
            pods: 8,
            of: 10,
            minutes_ago: 13,
            clean: false,
            after,
        }
    }

    #[rstest]
    #[case::after_a_rollout(
        across(Some(AfterRoll { rolled: "radio".into(), machines: 8 })),
        Some("speaker's mixer restarted on 8 machines right after radio rolled")
    )]
    #[case::after_nothing_known(across(None), None)]
    #[case::logs_of_some_pods(
        logged(4, 2, 3),
        Some("Read from 2 of its 3 pods; transect reads a pod's logs only when its page opens")
    )]
    #[case::logs_of_every_pod(
        logged(4, 3, 3),
        Some("Read from all 3 of its pods; transect reads a pod's logs only when its page opens")
    )]
    #[case::logs_of_the_only_pod(
        logged(1, 1, 1),
        Some("Read from its only pod; transect reads a pod's logs only when its page opens")
    )]
    #[case::searched_errors(
        searched(12),
        Some(
            "The LogSource logs counted the lines its pods logged at the error level, across every pod"
        )
    )]
    #[case::a_pattern(matched(6), Some("It cannot connect."))]
    #[case::an_alert(alert(("", "Namespace", "shop"), None, Some(5)), Some("WebDown summary"))]
    fn note(#[case] reason: Reason, #[case] text: Option<&str>) {
        assert_eq!(reason.note().as_deref(), text);
    }

    fn alert(
        (group, kind, name): (&str, &str, &str),
        container: Option<&str>,
        minutes_ago: Option<i64>,
    ) -> Reason {
        Reason::Alert {
            subject: transect_graph::edges::Object::new(group, kind, Some("shop"), name),
            container: container.map(Into::into),
            name: "WebDown".into(),
            summary: Some("WebDown summary".into()),
            level: super::super::Level::Warning,
            minutes_ago,
        }
    }

    #[rstest]
    #[case::a_namespace(alert(("", "Namespace", "shop"), None, Some(5)), "alert WebDown, firing for 5m")]
    #[case::a_workload(alert(("apps", "Deployment", "web"), None, Some(1)), "web: alert WebDown, firing for 1m")]
    #[case::a_container(alert(("", "Pod", "web-0"), Some("app"), Some(0)), "app in web-0: alert WebDown, firing for 0m")]
    #[case::no_start(alert(("", "Service", "web"), None, None), "web: alert WebDown is firing")]
    fn an_alert_names_its_subject(#[case] reason: Reason, #[case] text: &str) {
        assert_eq!(reason.to_string(), text);
    }
}
