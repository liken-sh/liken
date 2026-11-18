//! The `Lease` section of an object's page: the holder, the time of the
//! last renewal, the lease duration, and how many times the holder has
//! changed.

use k8s_openapi::jiff::Timestamp;
use serde_json::Value;

use super::super::super::snapshot::Snapshot;
use super::{Found, refer_object};
use transect_graph::edges::Object;
use transect_map::objects::{Section, Side, words};

pub fn lease(snapshot: &Snapshot, object: &Object, found: &Found, now: Timestamp) -> Vec<Section> {
    let spec = found.data.get("spec");
    let field = |name: &str| spec.and_then(|spec| spec.get(name));
    let text = |name: &str| field(name).and_then(Value::as_str);
    let renewed = text("renewTime").and_then(|at| at.parse::<Timestamp>().ok());
    let renewed = renewed.map(|at| format!("{} ago, at {at:.0}", since(at, now)));
    let lasts = field("leaseDurationSeconds").and_then(Value::as_i64);
    let section = Section::new("Lease", Side::Own);
    let section = match text("holderIdentity").filter(|holder| !holder.is_empty()) {
        Some(holder) => section.fact("held by", vec![holder_of(snapshot, object, holder)]),
        None => section.text("held by", Some("no one".into())),
    };
    let section = match renewed {
        Some(renewed) => section.text("renewed", Some(renewed)),
        None => section.unset("renewed"),
    };
    let section = section.text("lasts", lasts.map(|seconds| format!("{seconds}s")));
    let transitions = field("leaseTransitions").and_then(Value::as_i64);
    vec![section.text("holder changes", transitions.map(|n| n.to_string()))]
}

/// The holder as a reference. A leader election often writes a pod's
/// name as the holder, and a kubelet writes its node's name, so the
/// reference links to the page of a pod in the `Lease`'s namespace or of
/// a machine with that name, and is plain text otherwise.
fn holder_of(
    snapshot: &Snapshot,
    object: &Object,
    holder: &str,
) -> transect_graph::links::Reference {
    let here = object.namespace.as_deref();
    let pod = Object::new("", "Pod", here, holder);
    let held = snapshot.pods.iter().any(|p| {
        p.metadata.namespace.as_deref() == here && p.metadata.name.as_deref() == Some(holder)
    });
    if held {
        return refer_object(snapshot, &pod, here);
    }
    let machine = snapshot
        .nodes
        .iter()
        .any(|node| node.metadata.name.as_deref() == Some(holder));
    match machine {
        true => transect_graph::links::Reference {
            text: holder.into(),
            href: Some(transect_graph::links::machine(holder)),
        },
        false => words(holder),
    }
}

/// How long before `now` the time `at` was: in seconds under two
/// minutes, because a `Lease` renews every few seconds, and as a
/// coarser age after that.
fn since(at: Timestamp, now: Timestamp) -> String {
    let seconds = (now.as_second() - at.as_second()).max(0);
    match seconds {
        ..120 => format!("{seconds}s"),
        _ => transect_resources::knowledge::age(seconds / 60),
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    #[rstest]
    #[case::seconds(8, "8s")]
    #[case::under_two_minutes(119, "119s")]
    #[case::minutes(600, "10m")]
    #[case::a_skewed_clock(-3, "0s")]
    fn says_how_long_ago_in_seconds_then_as_an_age(#[case] seconds: i64, #[case] expected: &str) {
        let now: Timestamp = "2026-01-01T12:00:00Z".parse().unwrap();
        let at = Timestamp::from_second(now.as_second() - seconds).unwrap();
        assert_eq!(since(at, now), expected);
    }
}
