//! The words on the section view's boxes.  Everything else on them is a name, a
//! host, an address, or a number from the cluster.

use transect_ground::machines::{Amount, trimmed};
use transect_ground::storage::{Backend, Type};
use transect_network::entry::{IngressBox, ServiceBox};

use super::spine::Backing;

/// The legend above the section view that explains how its lines are
/// drawn.
pub const LEGEND: &str =
    "Solid lines are structure. Dashed lines are callers. Dotted lines are guesses.";

/// The heading over the section view.  It starts from the workload's
/// entry when it has one, and from the workload itself when nothing
/// reaches it from outside, as in a Player's or a Remote's section.  It
/// ends at the workload's storage when it has any, and at its machines
/// when it has none.
pub fn subtitle(entry: bool, storage: bool, name: &str) -> String {
    let from = match entry {
        true => "entry",
        false => name,
    };
    let to = match storage {
        true => "storage",
        false => "its machines",
    };
    format!("From {from} to {to}")
}

/// "1 replica · ready", "3 replicas · 2 ready", "5 pods · all ready",
/// or, for a workload whose pods finish, "2 pods running".
pub fn workload_line(replicas: Option<i32>, ready: Option<(i32, i32)>, running: usize) -> String {
    match (replicas, ready) {
        (Some(0), _) => "scaled to 0".into(),
        (Some(1), Some((ready, _))) if ready >= 1 => "1 replica · ready".into(),
        (Some(1), Some(_)) => "1 replica · not ready".into(),
        (Some(wanted), Some((ready, _))) if ready >= wanted => {
            format!("{wanted} replicas · all ready")
        }
        (Some(wanted), Some((ready, _))) => format!("{wanted} replicas · {ready} ready"),
        (None, Some((1, 1))) => "1 pod · ready".into(),
        (None, Some((ready, wanted))) if ready >= wanted && wanted > 0 => {
            format!("{wanted} pods · all ready")
        }
        (None, Some((ready, wanted))) => format!("{} · {ready} ready", pods(wanted)),
        (_, None) => match running {
            0 => "no pod running".into(),
            1 => "1 pod running".into(),
            count => format!("{count} pods running"),
        },
    }
}

fn pods(count: i32) -> String {
    match count {
        1 => "1 pod".into(),
        count => format!("{count} pods"),
    }
}

/// The Ingress's hosts, which are the box's title.
pub fn ingress_title(ingress: &IngressBox) -> String {
    ingress.host_lines().join(", ")
}

/// The Ingress's traffic: a line per backend, then the traffic's note,
/// when it has one.
pub fn traffic_lines(ingress: &IngressBox) -> Vec<String> {
    let traffic = &ingress.traffic;
    let lines = traffic.lines(&ingress.namespace, false).into_iter();
    let lines = lines.map(|line| match line.backend {
        Some(backend) => format!("{} · {}", backend.text, line.figures),
        None => line.figures,
    });
    let note = traffic.note().map(|note| note.text);
    lines.chain(note).collect()
}

/// "Ingress web · proxy · TLS from public".
pub fn ingress_line(ingress: &IngressBox) -> String {
    format!("Ingress {} · {}", ingress.name, ingress.serving())
}

/// "ClusterIP :80 · 2 endpoints".
pub fn service_line(service: &ServiceBox) -> String {
    format!("{} · {}", service.kind_line(), service.endpoints_line())
}

/// "1 claim", "3 claims".
pub fn claims_title(count: usize) -> String {
    match count {
        1 => "1 claim".into(),
        count => format!("{count} claims"),
    }
}

/// The line on a claims box: where its claims store their data, the
/// kinds of volume, and the total size, as in "nas.example.com · NFS,
/// iSCSI · 266Gi".  A box whose claims wait for a volume shows "waiting
/// for a volume".
pub fn claims_line(backing: &Backing) -> String {
    let place = match &backing.backend {
        None => return "waiting for a volume".into(),
        Some(Backend::Address(name) | Backend::Driver(name) | Backend::Machine(Some(name))) => {
            name.as_str()
        }
        Some(Backend::Machine(None)) => "its pod's machine",
    };
    let mut parts = vec![place.to_string()];
    let kinds = backing.claim_kinds();
    if !kinds.is_empty() {
        let names: Vec<String> = kinds.into_iter().map(type_name).collect();
        parts.push(names.join(", "));
    }
    if let Some(total) = total(backing.claims.iter().filter_map(|c| c.size.as_deref())) {
        parts.push(total);
    }
    parts.join(" · ")
}

/// One line per kind of volume on a ground box, counted and summed so
/// it fits the box: "iSCSI: 3 claims · 266Gi".  NFS volumes a pod names
/// inline, with no claim, are named: "NFS: movies, series".  The claims
/// box above names the backend, and these lines name what the workload
/// stores on it.
pub fn ground_lines(backing: &Backing) -> Vec<String> {
    let kinds = backing.kinds();
    kinds
        .into_iter()
        .map(|kind| {
            let claims: Vec<_> = backing
                .claims
                .iter()
                .filter(|c| c.kind.as_ref() == Some(kind))
                .collect();
            let mut parts = Vec::new();
            if !claims.is_empty() {
                parts.push(claims_title(claims.len()));
            }
            parts.extend(total(claims.iter().filter_map(|c| c.size.as_deref())));
            if *kind == Type::Nfs && !backing.inline.is_empty() {
                parts.push(backing.inline.join(", "));
            }
            format!("{}: {}", type_name(kind), parts.join(" · "))
        })
        .collect()
}

/// The kinds named on the line down to a ground box: "NFS", "iSCSI and
/// NFS".
pub fn kinds_label(backing: &Backing) -> String {
    let names: Vec<String> = backing.kinds().into_iter().map(type_name).collect();
    match names.as_slice() {
        [] => "stores on".into(),
        [one] => one.clone(),
        [rest @ .., last] => format!("{} and {last}", rest.join(", ")),
    }
}

fn type_name(kind: &Type) -> String {
    match kind {
        Type::Nfs => "NFS".into(),
        Type::Iscsi => "iSCSI".into(),
        Type::Class(class) => class.clone(),
        Type::Unclassed => "CSI".into(),
        Type::Local => "local".into(),
    }
}

/// The title of the box that a column draws in place of the side boxes
/// beyond the most that it draws.
pub fn more_title(count: usize) -> String {
    format!("+{count} more")
}

/// The title of the box drawn in place of every caller beyond the most
/// that the section view draws one by one: "12 callers".
pub fn callers_title(count: usize) -> String {
    format!("{count} callers")
}

/// The namespaces of the callers that the callers box is drawn in place
/// of: "in media", "in media and shop", "in 4 namespaces".
pub fn callers_line(namespaces: &[&str]) -> String {
    match namespaces {
        [] => "cluster-wide".into(),
        [one] => format!("in {one}"),
        [first, second] => format!("in {first} and {second}"),
        many => format!("in {} namespaces", many.len()),
    }
}

/// A side box's line for an address outside the cluster that the
/// workload reaches.
pub fn outside_side_line() -> String {
    "outside the cluster".into()
}

/// A side box's line for a machine the workload is configured to reach.
pub fn machine_side_line() -> String {
    "machine".into()
}

/// "runs the pod" when it is the workload's only one, "runs 2 pods",
/// and "· control plane" after it on a control-plane machine.
pub fn machine_line(pods: usize, of: usize, control_plane: bool) -> String {
    let runs = match (pods, of) {
        (1, 1) => "runs the pod".into(),
        (1, _) => "runs 1 pod".into(),
        (pods, _) => format!("runs {pods} pods"),
    };
    match control_plane {
        true => format!("{runs} · control plane"),
        false => runs,
    }
}

/// The title of the box drawn in place of every machine beyond the most
/// that the row draws.
pub fn machines_title(count: usize) -> String {
    format!("on {count} machines")
}

pub fn machines_line(pods: usize) -> String {
    match pods {
        1 => "runs 1 pod".into(),
        pods => format!("runs {pods} pods"),
    }
}

/// The sum of `sizes` in the largest binary unit that keeps it at one
/// or more: "266Gi", "1.5Ti".  `None` when no size parses.
pub fn total<'a>(sizes: impl IntoIterator<Item = &'a str>) -> Option<String> {
    let amounts: Vec<Amount> = sizes.into_iter().filter_map(Amount::parse).collect();
    if amounts.is_empty() {
        return None;
    }
    let mut value = amounts.into_iter().sum::<Amount>().units();
    let mut unit = "";
    for next in ["Ki", "Mi", "Gi", "Ti", "Pi", "Ei"] {
        if value < 1024.0 {
            break;
        }
        value /= 1024.0;
        unit = next;
    }
    Some(format!("{}{unit}", trimmed(value)))
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    #[rstest]
    #[case::with_an_entry(true, true, "From entry to storage")]
    #[case::with_no_entry(false, true, "From den to storage")]
    #[case::with_no_storage(false, false, "From den to its machines")]
    fn the_heading(#[case] entry: bool, #[case] storage: bool, #[case] expected: &str) {
        assert_eq!(subtitle(entry, storage, "den"), expected);
    }

    #[rstest]
    #[case::one_ready(Some(1), Some((1, 1)), 1, "1 replica · ready")]
    #[case::one_not_ready(Some(1), Some((0, 1)), 1, "1 replica · not ready")]
    #[case::three_all_ready(Some(3), Some((3, 3)), 3, "3 replicas · all ready")]
    #[case::three_two_ready(Some(3), Some((2, 3)), 3, "3 replicas · 2 ready")]
    #[case::scaled_down(Some(0), Some((0, 0)), 0, "scaled to 0")]
    #[case::a_daemon_set(None, Some((5, 5)), 5, "5 pods · all ready")]
    #[case::a_daemon_set_short(None, Some((4, 5)), 5, "5 pods · 4 ready")]
    #[case::a_cron_job(None, None, 0, "no pod running")]
    #[case::a_job_running(None, None, 2, "2 pods running")]
    fn says_how_a_workload_runs(
        #[case] replicas: Option<i32>,
        #[case] ready: Option<(i32, i32)>,
        #[case] running: usize,
        #[case] line: &str,
    ) {
        assert_eq!(workload_line(replicas, ready, running), line);
    }

    #[rstest]
    #[case::cluster_scoped(&[], "cluster-wide")]
    #[case::one_namespace(&["media"], "in media")]
    #[case::two_namespaces(&["media", "shop"], "in media and shop")]
    #[case::many_namespaces(&["jobs", "media", "shop"], "in 3 namespaces")]
    fn says_where_folded_callers_live(#[case] namespaces: &[&str], #[case] line: &str) {
        assert_eq!(callers_line(namespaces), line);
    }

    #[rstest]
    #[case::the_only_pod(1, 1, false, "runs the pod")]
    #[case::one_of_several(1, 3, false, "runs 1 pod")]
    #[case::two(2, 3, true, "runs 2 pods · control plane")]
    fn says_what_a_machine_runs(
        #[case] pods: usize,
        #[case] of: usize,
        #[case] control_plane: bool,
        #[case] line: &str,
    ) {
        assert_eq!(machine_line(pods, of, control_plane), line);
    }

    #[rstest]
    #[case::gibibytes(&["256Gi", "8Gi", "2Gi"], Some("266Gi"))]
    #[case::tebibytes(&["1Ti", "512Gi"], Some("1.5Ti"))]
    #[case::decimal(&["500M"], Some("476.8Mi"))]
    #[case::none_parse(&["lots"], None)]
    #[case::nothing(&[], None)]
    fn sums_sizes(#[case] sizes: &[&str], #[case] expected: Option<&str>) {
        assert_eq!(total(sizes.iter().copied()).as_deref(), expected);
    }
}
