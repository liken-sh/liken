//! A pod's reason in a few words, for a part of a sketch that already
//! names its pod. The full sentence names the pod and the container,
//! and the part shows it when the reader points at the reason.

use transect_resources::knowledge::age;

use super::Reason;

impl Reason {
    /// The reason without the names of the pod and the container that
    /// the sentence starts with, such as "crash looping after 4
    /// restarts". A reason that is not about one pod is its sentence.
    pub fn brief(&self) -> String {
        match self {
            Self::CrashLooping { restarts, .. } => {
                format!("crash looping after {}", super::times(*restarts, "restart"))
            }
            Self::Restarting {
                minutes_ago, clean, ..
            } => match clean {
                true => format!("exited cleanly and restarted {} ago", age(*minutes_ago)),
                false => format!("restarted {} ago", age(*minutes_ago)),
            },
            Self::PodPending { minutes, .. } => format!("pending for {}", age(*minutes)),
            other => other.to_string(),
        }
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    #[rstest]
    #[case::crash_looping(
        Reason::CrashLooping { pod: "web-1".into(), container: "app".into(), restarts: 4 },
        "crash looping after 4 restarts"
    )]
    #[case::restarting(
        Reason::Restarting { pod: "web-1".into(), container: "app".into(), restarts: 3, minutes_ago: 12, clean: false },
        "restarted 12m ago"
    )]
    #[case::restarting_cleanly(
        Reason::Restarting { pod: "web-1".into(), container: "app".into(), restarts: 1, minutes_ago: 12, clean: true },
        "exited cleanly and restarted 12m ago"
    )]
    #[case::pending(Reason::PodPending { pod: "web-1".into(), minutes: 6 }, "pending for 6m")]
    fn says_a_pods_reason_without_its_names(#[case] reason: Reason, #[case] brief: &str) {
        assert_eq!(reason.brief(), brief);
    }
}
