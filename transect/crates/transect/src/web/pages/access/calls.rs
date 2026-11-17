//! Who may call a workload from inside the cluster, by the rules that
//! admit them, and what it may call: the isolated workloads that admit
//! it, and the policies that limit its calls out, or any address.

use super::dots::{off, on, plain};
use super::look::Look;
use super::served::Served;
use crate::web::model::Model;
use transect_access::access::drawing::Gate;
use transect_access::access::drawing::reach::{Group, Inside, Out};
use transect_access::access::ways::Isolation;
use transect_graph::edges::{Endpoint, Kind, Object, Shown};
use transect_graph::links::Part;
use transect_map::map::section_drawing::SectionBox;
use transect_network::policy::{Admission, Caller, Ports, Reach as Called};

/// The most callers one rule's group draws one by one; past it, one box
/// counts the rest.
const MOST_CALLERS: usize = 5;

/// The most workloads "may call" draws one by one; past it one box
/// counts the rest and opens to them.
const MOST_CALLED: usize = 3;

/// How near a rule of `target`'s comes to naming `me`, nearest first:
/// a rule that selects its pods, then one that admits its namespace
/// whole, then one that admits every pod.  The nearest are drawn first,
/// since a rule for any pod admits it along with every other workload.
fn nearness(admissions: &[Admission], me: &Caller, namespace: &str) -> u8 {
    admissions
        .iter()
        .filter_map(|admission| {
            let whole = admission.whole.iter().any(|name| name == namespace);
            match (admission.everywhere, whole, admission.callers.contains(me)) {
                (true, _, _) => Some(2),
                (false, true, _) => Some(1),
                (false, false, true) => Some(0),
                (false, false, false) => None,
            }
        })
        .min()
        .unwrap_or(2)
}

/// The `NetworkPolicy` gate on a line into it from a `Service`.
pub fn isolation(model: &Model, object: &Object) -> Gate {
    let allowed = model.allowed(&Shown::default());
    let isolation = match allowed.reach(object) {
        Some(Called::Isolated(_)) => Some(Isolation::Isolated),
        Some(Called::MachineNetwork) => Some(Isolation::MachineNetwork),
        _ => None,
    };
    let gate =
        transect_access::access::ways::policy::of_workload(isolation, allowed.admissions(object));
    super::dots::gate(&gate)
}

/// Who may call it from inside the cluster.
pub fn inside(model: &Model, object: &Object, ports: &mut Served, look: &Look) -> Inside {
    let allowed = model.allowed(&Shown::default());
    let any = |line: &str, label: &str, title: &str| Inside::Any {
        boxed: Box::new(plain("any pod".into(), vec![line.into()], None)),
        label: label.into(),
        gate: off(title),
    };
    match allowed.reach(object) {
        Some(Called::MachineNetwork) => any(
            "at its machine's address",
            "no NetworkPolicy applies to a pod on its machine's network",
            "No NetworkPolicy applies to a pod on its machine's network.",
        ),
        Some(Called::Isolated(callers)) => {
            let admitted = allowed.admissions(object);
            let mut groups = admitted
                .iter()
                .filter_map(|admission| group(model, object, admission, callers, ports, look))
                .collect::<Vec<Group>>();
            // Groups in the order of the ports they reach, those on any
            // port first, so their lines run to their ports without
            // crossing; then by policy, since the policies come in no
            // fixed order and each update pushed to the page would
            // otherwise reorder them.
            groups.sort_by(|a, b| {
                let port = |group: &Group| group.ports.first().map_or(-1, |&port| port as i64);
                (port(a), &a.policy).cmp(&(port(b), &b.policy))
            });
            match groups.is_empty() {
                true => Inside::Any {
                    boxed: Box::new(plain(
                        "no pod".into(),
                        vec!["A NetworkPolicy selects its pods and admits no caller.".into()],
                        None,
                    )),
                    label: String::new(),
                    gate: on("A NetworkPolicy selects its pods and admits no caller.".into()),
                },
                false => Inside::Admitted(groups),
            }
        }
        _ => any(
            "in any namespace",
            "no NetworkPolicy selects its pods",
            "No NetworkPolicy selects its pods, so any pod may call them.",
        ),
    }
}

/// The callers one rule admits, as boxes: the namespaces it admits
/// whole, each workload it admits apart from those, the machines'
/// addresses that workloads on their machine's network call from, and
/// address blocks.  A caller that its own egress rules block is left
/// out.
fn group(
    model: &Model,
    object: &Object,
    admission: &Admission,
    callers: &std::collections::BTreeMap<Caller, Ports>,
    ports: &mut Served,
    look: &Look,
) -> Option<Group> {
    let allowed = model.allowed(&Shown::default());
    let namespace = object.namespace.as_deref();
    let mut boxes: Vec<SectionBox> = Vec::new();
    if admission.everywhere {
        boxes.push(plain(
            "every pod".into(),
            vec!["in every namespace".into()],
            None,
        ));
    }
    let mut whole: Vec<&String> = admission.whole.iter().collect();
    whole.sort();
    for whole in whole.into_iter().filter(|_| !admission.everywhere) {
        let href = look.namespace(whole);
        boxes.push(plain(format!("every pod in {whole}"), Vec::new(), href));
    }
    let mut on_machines: Vec<Part> = Vec::new();
    let mut blocks: Vec<String> = Vec::new();
    let mut workloads: Vec<SectionBox> = Vec::new();
    for caller in &admission.callers {
        match caller {
            Caller::Workload {
                namespace: from,
                owner,
            } => {
                if !callers.contains_key(caller) {
                    continue;
                }
                if allowed.reach(owner) == Some(&Called::MachineNetwork) {
                    on_machines.push(Part {
                        text: transect_graph::links::kind_and_name_in(owner, namespace),
                        href: look.href(owner),
                    });
                    continue;
                }
                if admission.everywhere || admission.whole.contains(from) {
                    continue;
                }
                let title = transect_graph::links::kind_and_name_in(owner, namespace);
                workloads.push(plain(title, Vec::new(), look.href(owner)));
            }
            Caller::Addresses { cidr, except } => {
                let text = match except.is_empty() {
                    true => cidr.clone(),
                    false => format!("{cidr} except {}", except.join(", ")),
                };
                blocks.push(text);
            }
            Caller::Anything => boxes.push(plain(
                "anything".into(),
                vec!["any pod or address".into()],
                None,
            )),
        }
    }
    if workloads.len() > MOST_CALLERS + 1 {
        let rest = workloads.split_off(MOST_CALLERS);
        let names: Vec<String> = rest.iter().map(|b| b.title.clone()).collect();
        let more = plain(format!("{} more workloads", rest.len()), Vec::new(), None)
            .with_hint(names.join(", "));
        workloads.push(more);
    }
    boxes.extend(workloads);
    match (on_machines.as_slice(), blocks.is_empty()) {
        ([], _) => {
            for block in blocks {
                boxes.push(plain(block, vec!["addresses".into()], None));
            }
        }
        (names, _) => {
            // Many workloads on their machines' networks would make a
            // tall box of names, so it counts them and names one, from
            // this namespace where one is, with every name on hover.
            let (line, named) = match names {
                [one] => (
                    "where this workload on its machine's network calls from:".to_string(),
                    one.clone(),
                ),
                many => {
                    // A name in this namespace has no slash.
                    let local = many.iter().find(|part| !part.text.contains('/'));
                    let first = local.unwrap_or(&many[0]).clone();
                    let line = format!(
                        "where {} workloads on a machine's network call from, among them:",
                        many.len()
                    );
                    (line, first)
                }
            };
            let all: Vec<&str> = names.iter().map(|part| part.text.as_str()).collect();
            let hint = format!(
                "{}. The policy admits {}.",
                all.join(", "),
                super::look::and(&blocks)
            );
            let boxed = plain(
                "the machines' addresses".into(),
                vec![line, String::new()],
                None,
            );
            boxes.push(boxed.with_names(vec![named]).with_hint(hint));
        }
    }
    if boxes.is_empty() {
        return None;
    }
    let policy = Object::new(
        "networking.k8s.io",
        "NetworkPolicy",
        namespace,
        &admission.policy,
    );
    Some(Group {
        callers: boxes,
        href: model.snapshot.pages().href(&policy),
        policy: admission.policy.clone(),
        gate: on(format!("NetworkPolicy {} admits it.", admission.policy)),
        ports: ports.admitted(&admission.ports),
    })
}

/// The isolated workloads it may call, each with the ports it may call
/// them on: those whose rules name it or its namespace first, and past
/// three, one box that counts the rest and opens to them.
pub fn calls(model: &Model, object: &Object, look: &Look) -> Vec<SectionBox> {
    let allowed = model.allowed(&Shown::default());
    let graph = model.graph(&Shown::default());
    let from = Endpoint::Object(object.clone());
    let me = Caller::Workload {
        namespace: object.namespace.clone().unwrap_or_default(),
        owner: object.clone(),
    };
    let namespace = object.namespace.as_deref().unwrap_or_default();
    let mut boxes: Vec<(u8, SectionBox)> = graph
        .leaving(Kind::AllowedToCall, &from)
        .into_iter()
        .filter_map(|end| {
            let Endpoint::Object(target) = end else {
                return None;
            };
            let Some(Called::Isolated(callers)) = allowed.reach(target) else {
                return None;
            };
            let ports = callers.get(&me)?;
            let title =
                transect_graph::links::kind_and_name_in(target, object.namespace.as_deref());
            let near = nearness(allowed.admissions(target), &me, namespace);
            Some((
                near,
                plain(title, vec![ports.to_string()], look.href(target)),
            ))
        })
        .collect();
    // The edges come in the order the evaluation found them, which
    // changes from one model to the next, so the boxes are sorted by
    // name within their nearness and keep their places as the page
    // updates.
    boxes.sort_by(|a, b| (a.0, &a.1.title).cmp(&(b.0, &b.1.title)));
    fold(boxes.into_iter().map(|(_, boxed)| boxed).collect())
}

/// Past three boxes, one box that counts the rest and opens to them,
/// each with the ports it may be called on.
fn fold(mut boxes: Vec<SectionBox>) -> Vec<SectionBox> {
    if boxes.len() > MOST_CALLED + 1 {
        let rest = boxes.split_off(MOST_CALLED);
        let names: Vec<String> = rest.iter().map(|b| b.title.clone()).collect();
        let folded = rest
            .into_iter()
            .map(|b| Part {
                text: format!("{} · {}", b.title, b.lines.join(", ")),
                href: b.href,
            })
            .collect();
        boxes.push(
            plain(format!("{} more workloads", names.len()), Vec::new(), None)
                .with_hint(names.join(", "))
                .with_folded(folded),
        );
    }
    boxes
}

/// What limits its calls out.
pub fn out(model: &Model, object: &Object) -> Out {
    let allowed = model.allowed(&Shown::default());
    let on_machine = allowed.reach(object) == Some(&Called::MachineNetwork);
    match allowed.limits_out(object) {
        Some(names) if !on_machine => {
            let namespace = object.namespace.as_deref();
            let boxes = names.iter().map(|name| {
                let policy = Object::new("networking.k8s.io", "NetworkPolicy", namespace, name);
                let href = model.snapshot.pages().href(&policy);
                plain(
                    format!("NetworkPolicy {name}"),
                    vec!["limits its calls out".into()],
                    href,
                )
            });
            Out::Limited(
                boxes.collect(),
                on("A NetworkPolicy limits its calls out.".into()),
            )
        }
        _ if on_machine => Out::Any(off(
            "No NetworkPolicy applies to a pod on its machine's network, so it may call any address.",
        )),
        _ => Out::Any(off(
            "No NetworkPolicy limits its calls out, so it may call any address.",
        )),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use rstest::rstest;

    fn admission(everywhere: bool, whole: &[&str], callers: &[Caller]) -> Admission {
        Admission {
            policy: "a-policy".into(),
            ports: Ports::default(),
            callers: callers.iter().cloned().collect(),
            whole: whole.iter().map(|name| name.to_string()).collect(),
            everywhere,
        }
    }

    fn me() -> Caller {
        Caller::Workload {
            namespace: "shop".into(),
            owner: Object::new("apps", "Deployment", Some("shop"), "web"),
        }
    }

    #[rstest]
    #[case::a_rule_that_selects_its_pods(vec![admission(false, &[], &[me()])], 0)]
    #[case::a_rule_for_its_namespace(vec![admission(false, &["shop"], &[me()])], 1)]
    #[case::a_rule_for_every_pod(vec![admission(true, &[], &[me()])], 2)]
    #[case::the_nearest_of_several(
        vec![admission(true, &[], &[me()]), admission(false, &[], &[me()])],
        0
    )]
    fn draws_a_rule_that_names_it_before_one_for_any_pod(
        #[case] admissions: Vec<Admission>,
        #[case] near: u8,
    ) {
        assert_eq!(nearness(&admissions, &me(), "shop"), near);
    }

    #[rstest]
    #[case::four_stay_whole(4, vec!["0", "1", "2", "3"])]
    #[case::five_fold_past_three(5, vec!["0", "1", "2", "2 more workloads"])]
    fn folds_what_it_may_call_past_three(#[case] count: usize, #[case] titles: Vec<&str>) {
        let boxes = (0..count).map(|at| plain(at.to_string(), vec!["port 80".into()], None));
        let folded = fold(boxes.collect());
        let shown: Vec<&str> = folded.iter().map(|b| b.title.as_str()).collect();
        assert_eq!(shown, titles);
    }

    #[test]
    fn opens_the_folded_box_to_each_name_and_its_ports() {
        let boxes =
            (0..5).map(|at| plain(format!("Deployment d{at}"), vec!["port 80".into()], None));
        let folded = fold(boxes.collect());
        let names: Vec<&str> = folded[3].folded.iter().map(|p| p.text.as_str()).collect();
        assert_eq!(
            names,
            ["Deployment d3 · port 80", "Deployment d4 · port 80"]
        );
    }
}
