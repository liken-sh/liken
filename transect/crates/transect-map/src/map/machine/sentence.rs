//! The text on the machine page that is built from counts and states.

use super::super::workload::tiles::{PodTile, State};
use transect_ground::machines::Facts;
use transect_ground::storage::{Pin, Type};
use transect_resources::knowledge::age;

/// "Control plane, ready" or "Worker, not ready".
pub fn status(facts: &Facts, ready: bool) -> String {
    let role = facts.role();
    let mut role = role.to_string();
    if let Some(first) = role.get_mut(..1) {
        first.make_ascii_uppercase();
    }
    match ready {
        true => format!("{role}, ready"),
        false => format!("{role}, not ready"),
    }
}

/// "12 pods in 4 namespaces run here.", "1 pod in 1 namespace runs
/// here.", or "No pods run here.".
pub fn pods(pods: usize, namespaces: usize) -> String {
    let plural = |count: usize, word: &str| match count {
        1 => format!("1 {word}"),
        _ => format!("{count} {word}s"),
    };
    let (pods_text, namespaces) = (plural(pods, "pod"), plural(namespaces, "namespace"));
    match pods {
        0 => "No pods run here.".into(),
        1 => format!("{pods_text} in {namespaces} runs here."),
        _ => format!("{pods_text} in {namespaces} run here."),
    }
}

/// The text after a pod's usage bars, such as "up 4d · 2 restarts",
/// with "not ready" first when the pod is not ready.
pub fn row(tile: &PodTile) -> String {
    let mut parts = Vec::new();
    if tile.state == State::NotReady {
        parts.push("not ready".to_string());
    }
    if let Some(minutes) = tile.age {
        parts.push(format!("up {}", age(minutes)));
    }
    parts.push(match tile.restarts {
        1 => "1 restart".into(),
        restarts => format!("{restarts} restarts"),
    });
    parts.join(" · ")
}

/// Where a volume is stored, shown after its claim's name: "on its own
/// disk" for a local disk. An attached volume is "on" its StorageClass,
/// as in "on fast", or "over iSCSI", "over NFS", or else "attached".
pub fn how(pin: &Pin) -> String {
    match pin {
        Pin::Disk { .. } => "on its own disk".into(),
        Pin::Attached { kind, .. } => match kind {
            Type::Class(class) => format!("on {class}"),
            Type::Iscsi => "over iSCSI".into(),
            Type::Nfs => "over NFS".into(),
            Type::Unclassed | Type::Local => "attached".into(),
        },
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;
    use transect_graph::edges::Claim;

    #[rstest]
    #[case::none(0, 0, "No pods run here.")]
    #[case::one(1, 1, "1 pod in 1 namespace runs here.")]
    #[case::many(12, 4, "12 pods in 4 namespaces run here.")]
    fn counts_what_runs_here(#[case] count: usize, #[case] namespaces: usize, #[case] text: &str) {
        assert_eq!(pods(count, namespaces), text);
    }

    #[rstest]
    #[case::a_disk(Pin::Disk { claim: Claim::new("shop", "data"), machine: "alpha".into() }, "on its own disk")]
    #[case::a_class(
        Pin::Attached { claim: Claim::new("shop", "data"), kind: Type::Class("fast".into()), machine: "alpha".into() },
        "on fast"
    )]
    fn says_where_a_volume_is_stored(#[case] pin: Pin, #[case] text: &str) {
        assert_eq!(how(&pin), text);
    }

    fn tile(state: State, age: Option<i64>, restarts: i32) -> PodTile {
        let bar = super::super::super::bar::Bar::default();
        PodTile {
            name: "web-1".into(),
            short_name: "web-1".into(),
            machine: Some("alpha".into()),
            age,
            restarts,
            state,
            memory: bar,
            cpu: bar,
        }
    }

    #[rstest]
    #[case::ready(tile(State::Ready, Some(5760), 0), "up 4d · 0 restarts")]
    #[case::not_ready(tile(State::NotReady, Some(30), 1), "not ready · up 30m · 1 restart")]
    #[case::not_started(tile(State::NotReady, None, 0), "not ready · 0 restarts")]
    fn says_how_a_pod_is_doing(#[case] tile: PodTile, #[case] text: &str) {
        assert_eq!(row(&tile), text);
    }

    #[rstest]
    #[case::ready(true, "Worker, ready")]
    #[case::not_ready(false, "Worker, not ready")]
    fn says_its_role_and_readiness(#[case] ready: bool, #[case] text: &str) {
        assert_eq!(status(&Facts::default(), ready), text);
    }
}
