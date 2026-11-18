//! The `/access` page: one row for each way in, in runs under headings.
//! Hosts come first, in a run for each combination of gate states.
//! Then come LoadBalancer Services, Services' external IPs, node ports,
//! host ports, and pods on their machine's network.

use std::collections::BTreeMap;

use askama::Template;

use super::{Gate, State, Through, Way, Ways};
use crate::access::accounts::Named;
use transect_frame::frame::crumbs::Crumb;
use transect_graph::edges::{Object, Provenance};
use transect_graph::links::Part;

/// A gate's dot in a cell of the table.
#[derive(Debug, PartialEq)]
pub struct Dot {
    pub class: &'static str,
    pub title: String,
    /// The objects that put the gate there, beside the dot.
    pub names: Vec<Named>,
    /// The source of the statement that a declared gate rests on, such
    /// as a field of an `IngressController` or stored text.  The cell
    /// shows its mark beside the dot.
    pub declared_at: Option<Provenance>,
}

impl Dot {
    pub fn of(gate: &Gate, look: &dyn Fn(&Object) -> Option<String>) -> Self {
        let names = gate.names.iter().map(|object| Named {
            text: object.name.clone(),
            href: crate::access::link(object, look),
        });
        Self {
            class: class(gate.state),
            title: gate.title.clone(),
            names: names.collect(),
            declared_at: gate.declared_at.clone(),
        }
    }

    /// Whether the cell shows more than the dot.
    pub fn wide(&self) -> bool {
        !self.names.is_empty() || self.declared_at.is_some()
    }
}

pub fn class(state: State) -> &'static str {
    match state {
        State::On => "ax-dot ax-dot-on",
        State::Half => "ax-dot ax-dot-half",
        State::Declared => "ax-dot ax-dot-declared",
        State::Off => "ax-dot ax-dot-off",
    }
}

#[derive(Debug, PartialEq)]
pub struct Row {
    /// The machine whose address this way is, linked to the machine's
    /// page.  `name` is the rest of the way's name.
    pub machine: Option<Named>,
    pub name: String,
    /// The page that the way in's name links to: the page of the first
    /// workload it reaches.
    pub href: Option<String>,
    pub via: Vec<Named>,
    /// How it opens, such as "host port".
    pub how: Option<&'static str>,
    /// One item per port, so the cell wraps only between ports.
    pub ports: Vec<String>,
    pub gates: [Dot; 3],
    pub reaches: Option<Named>,
    pub more: Option<(usize, String)>,
}

#[derive(Debug, PartialEq)]
pub struct Run {
    pub heading: String,
    pub rows: Vec<Row>,
}

#[derive(Template)]
#[template(path = "access/ways/ways.html", blocks = ["main"])]
pub struct WaysPage {
    pub runs: Vec<Run>,
    /// The ingress controllers the hosts enter through, in a line each.
    pub entered: Vec<Vec<Part>>,
    pub callbacks: Vec<Vec<Part>>,
    /// The Ingresses whose class transect finds no ingress controller
    /// workload for, in a line each.
    pub unserved: Vec<Vec<Part>>,
    /// The namespaces that hold pods and no NetworkPolicy that limits
    /// calls in.
    pub open: Vec<Part>,
    /// The route kinds that have objects transect cannot read, because
    /// nothing declares where their hosts and backends are.
    pub unread: Option<String>,
    pub no_gitops: bool,
}

/// The inputs of [`WaysPage::build`] besides the ways, such as the
/// function that returns the link to an object's page.
pub struct Look<'a> {
    pub href: &'a dyn Fn(&Object) -> Option<String>,
    pub open: Vec<String>,
    pub unread: Vec<String>,
    pub no_gitops: bool,
}

impl WaysPage {
    pub fn build(ways: &Ways, look: Look) -> Self {
        let named = |object: &Object| Named {
            text: transect_graph::links::kind_and_name_in(object, None),
            href: crate::access::link(object, look.href),
        };
        let mut runs: BTreeMap<(Through, u8, String), Vec<Row>> = BTreeMap::new();
        let mut rows = ways.rows.clone();
        rows.sort_by(|a, b| a.order(b));
        for way in rows {
            let (rank, heading) = heading(&way);
            let reaches = way.reaches.first().map(named);
            let more = (way.reaches.len() > 1).then(|| {
                let others: Vec<String> = way.reaches[1..].iter().map(|o| named(o).text).collect();
                (others.len(), others.join(", "))
            });
            let how = match way.through {
                Through::HostPort => Some("host port"),
                Through::MachineNetwork => Some("on its machine's network"),
                Through::LoadBalancer => Some("LoadBalancer"),
                Through::ExternalIp => Some("external IP"),
                Through::NodePort => Some("node port"),
                Through::Host => None,
            };
            // A host-port or machine-network row shows no `via`, because
            // the workload that listens is the one in the "reaches" cell.
            let via = match way.through {
                Through::HostPort | Through::MachineNetwork => &Vec::new(),
                _ => &way.via,
            };
            let (machine, name) = way.on_machine();
            let row = Row {
                machine,
                href: way
                    .reaches
                    .first()
                    .and_then(|o| crate::access::link(o, look.href)),
                via: via.iter().map(&named).collect(),
                name,
                how,
                ports: way.ports,
                gates: [
                    Dot::of(&way.tls, look.href),
                    Dot::of(&way.sign_in, look.href),
                    Dot::of(&way.policy, look.href),
                ],
                reaches,
                more,
            };
            runs.entry((way.through, rank, heading))
                .or_default()
                .push(row);
        }
        let entered = ways.entered.iter().map(|entered| {
            let hosts = plural(entered.hosts, "host enters", "hosts enter");
            let mut parts = vec![Part::plain(&format!("{} {hosts} through ", entered.hosts))];
            let workload = named(&entered.workload);
            parts.push(Part {
                text: workload.text,
                href: workload.href,
            });
            if let Some((service, ports, addresses)) = &entered.service {
                let of = match addresses.as_str() {
                    super::addresses::UNADDRESSED => String::new(),
                    addresses => format!(" of {addresses}"),
                };
                let ports = format!("{} {}", plural(ports.len(), "port", "ports"), and(ports));
                parts.push(Part::plain(&format!(
                    ", which answers on {ports}{of} through "
                )));
                let service = named(service);
                parts.push(Part {
                    text: service.text,
                    href: service.href,
                });
            }
            parts.push(Part::plain("."));
            parts
        });
        let callbacks = ways.callbacks.iter().map(|(ingress, hosts)| {
            let ingress = named(ingress);
            vec![
                Part::plain("The sign-in's own callback, "),
                Part {
                    text: ingress.text,
                    href: ingress.href,
                },
                Part::plain(&format!(
                    ", routes a path on {} of these {} to what they sign in through, and is not a row.",
                    hosts,
                    plural(*hosts, "host", "hosts")
                )),
            ]
        });
        let unserved = ways.unserved.iter().map(|ingress| {
            let ingress = named(ingress);
            vec![
                Part {
                    text: ingress.text,
                    href: ingress.href,
                },
                Part::plain(
                    " names an IngressClass whose controller transect does not find among the cluster's workloads, and is not a row.",
                ),
            ]
        });
        Self {
            runs: runs.into_iter().map(|((_, _, heading), rows)| Run { heading, rows }).collect(),
            entered: entered.collect(),
            callbacks: callbacks.collect(),
            unserved: unserved.collect(),
            open: open(&look.open, look.href),
            unread: (!look.unread.is_empty()).then(|| {
                format!("{} {} objects that transect does not read: no kind knowledge declares where their hosts and backends are.", and(&look.unread), plural(look.unread.len(), "has", "have"))
            }),
            no_gitops: look.no_gitops,
        }
    }

    pub fn count(&self) -> usize {
        self.runs.iter().map(|run| run.rows.len()).sum()
    }

    pub fn crumbs(&self) -> Vec<Crumb> {
        vec![Crumb::home(), Crumb::current("access")]
    }

    pub fn events_url(&self) -> String {
        "/events/access".into()
    }
}

/// The line that names the namespaces with pods and no NetworkPolicy
/// that limits calls in.
fn open(namespaces: &[String], href: &dyn Fn(&Object) -> Option<String>) -> Vec<Part> {
    if namespaces.is_empty() {
        return vec![Part::plain(
            "Every namespace that has pods has a NetworkPolicy that limits calls in.",
        )];
    }
    let count = namespaces.len();
    let mut parts = vec![Part::plain(&format!(
        "{count} {} pods and no NetworkPolicy that limits calls in: ",
        plural(count, "namespace has", "namespaces have")
    ))];
    for (at, namespace) in namespaces.iter().enumerate() {
        if at > 0 {
            parts.push(Part::plain(", "));
        }
        parts.push(Part {
            text: namespace.clone(),
            href: href(&Object::new("", "Namespace", None, namespace)),
        });
    }
    parts.push(Part::plain("."));
    parts
}

fn plural<'a>(count: usize, one: &'a str, many: &'a str) -> &'a str {
    match count {
        1 => one,
        _ => many,
    }
}

/// `items` joined as an English list: "80 and 443", "a, b, and c".
fn and(items: &[String]) -> String {
    match items {
        [] => String::new(),
        [one] => one.clone(),
        [a, b] => format!("{a} and {b}"),
        [rest @ .., last] => format!("{}, and {last}", rest.join(", ")),
    }
}

/// The run that a row belongs to, as a rank and a heading.  The rank
/// orders the runs of hosts, those with more gates on first.  The
/// heading of a run of hosts lists their gates, and the heading of any
/// other run names the kind of way in.
fn heading(way: &Way) -> (u8, String) {
    match way.through {
        Through::LoadBalancer => (0, "LoadBalancer Services".into()),
        Through::ExternalIp => (0, "Services' external IPs".into()),
        Through::NodePort => (0, "Node ports".into()),
        Through::HostPort => (0, "Host ports".into()),
        Through::MachineNetwork => (0, "Pods on their machine's network".into()),
        Through::Host => {
            let gates = [
                (
                    way.tls.state,
                    "TLS",
                    "TLS on some routes",
                    "a redirect to TLS the ingress controller declares",
                ),
                (
                    way.sign_in.state,
                    "sign-in",
                    "sign-in on some routes",
                    "sign-in the workload declares",
                ),
                (
                    way.policy.state,
                    "a NetworkPolicy",
                    "a NetworkPolicy on some pods",
                    "a NetworkPolicy",
                ),
            ];
            let words = ["TLS", "sign-in", "NetworkPolicy"];
            let on: Vec<String> = gates
                .iter()
                .filter(|g| g.0 == State::On)
                .map(|g| g.1.to_string())
                .collect();
            let off: Vec<String> = gates
                .iter()
                .zip(words)
                .filter(|(g, _)| g.0 == State::Off)
                .map(|(_, word)| word.to_string())
                .collect();
            let rank = (3 - on.len()) as u8 * 4 + off.len() as u8;
            let mut clauses: Vec<String> = Vec::new();
            if !on.is_empty() {
                clauses.push(and(&on));
            }
            clauses.extend(
                gates
                    .iter()
                    .filter(|g| g.0 == State::Half)
                    .map(|g| g.2.to_string()),
            );
            clauses.extend(
                gates
                    .iter()
                    .filter(|g| g.0 == State::Declared)
                    .map(|g| g.3.to_string()),
            );
            if !off.is_empty() {
                let or = and(&off)
                    .replace(" and ", " or ")
                    .replace(", and ", ", or ");
                clauses.push(format!("no {or}"));
            }
            let text = match clauses.split_last() {
                Some((last, [])) => last.clone(),
                Some((last, rest)) => format!("{}, and {last}", rest.join(", ")),
                None => String::new(),
            };
            (rank, format!("Hosts with {text}"))
        }
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::super::Gate;
    use super::*;

    fn way(states: [State; 3]) -> Way {
        let gate = |state| Gate::of(state, "");
        Way {
            through: Through::Host,
            name: "shop.example.io".into(),
            via: Vec::new(),
            ports: vec!["443".into()],
            tls: gate(states[0]),
            sign_in: gate(states[1]),
            policy: gate(states[2]),
            reaches: Vec::new(),
            backends: Vec::new(),
            machine: None,
        }
    }

    #[rstest]
    #[case::a_host_port(Through::HostPort, "")]
    #[case::a_pod_on_its_machines_network(Through::MachineNetwork, "")]
    #[case::a_host(Through::Host, "")]
    fn names_the_workload_that_listens_only_where_it_reaches(
        #[case] through: Through,
        #[case] via: &str,
    ) {
        let relay = Object::new("apps", "DaemonSet", Some("acme"), "relay");
        let ways = Ways {
            rows: vec![Way {
                through,
                ports: vec!["4317".into(), "14268".into()],
                reaches: vec![relay],
                ..way([State::Off; 3])
            }],
            ..Ways::default()
        };
        let look = Look {
            href: &|_| None,
            open: Vec::new(),
            unread: Vec::new(),
            no_gitops: false,
        };
        let page = WaysPage::build(&ways, look);
        let row = &page.runs[0].rows[0];
        let named: Vec<&str> = row.via.iter().map(|n| n.text.as_str()).collect();
        assert_eq!(named.join(", "), via);
        assert_eq!(row.ports, ["4317", "14268"]);
    }

    #[rstest]
    #[case::all([State::On, State::On, State::On], "Hosts with TLS, sign-in, and a NetworkPolicy")]
    #[case::some_sign_in([State::On, State::Half, State::On], "Hosts with TLS and a NetworkPolicy, and sign-in on some routes")]
    #[case::declared_sign_in([State::On, State::Declared, State::On], "Hosts with TLS and a NetworkPolicy, and sign-in the workload declares")]
    #[case::declared_tls([State::Declared, State::Declared, State::Off], "Hosts with a redirect to TLS the ingress controller declares, sign-in the workload declares, and no NetworkPolicy")]
    #[case::no_sign_in([State::On, State::Off, State::On], "Hosts with TLS and a NetworkPolicy, and no sign-in")]
    #[case::tls_alone([State::On, State::Off, State::Off], "Hosts with TLS, and no sign-in or NetworkPolicy")]
    #[case::policy_alone([State::Off, State::Off, State::On], "Hosts with a NetworkPolicy, and no TLS or sign-in")]
    #[case::none([State::Off, State::Off, State::Off], "Hosts with no TLS, sign-in, or NetworkPolicy")]
    #[case::only_some([State::Off, State::Half, State::Off], "Hosts with sign-in on some routes, and no TLS or NetworkPolicy")]
    fn says_a_runs_gates_in_words(#[case] states: [State; 3], #[case] expected: &str) {
        assert_eq!(heading(&way(states)).1, expected);
    }
}
