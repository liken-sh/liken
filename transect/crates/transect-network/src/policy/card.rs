//! A card's "May be called by" section: the callers that each
//! `NetworkPolicy` admits to the card's workload or namespace, read
//! from the allowed-to-call summary.
//!
//! A rule often admits a whole namespace, or anything at all.  A list
//! of three hundred workloads one by one would hide the few rules that
//! matter, so the card groups the callers by the ports they reach and
//! folds each group.  When every workload of a namespace may call, the
//! card shows the namespace, and when every namespace may call, the
//! card shows "any pod".

use std::collections::{BTreeMap, BTreeSet};

use super::notes;
use super::{Allowed, Caller, NamespaceReach, Ports, Reach};
use transect_graph::edges::{Endpoint, Object, Plurals};
use transect_graph::links::{Named, Part, Reference};

#[derive(Debug, Default, PartialEq)]
pub struct Calls {
    /// Why the card lists no caller, such as that nothing isolates it
    /// or that policy admits none.  Each workload the note names is a
    /// link.  The note is empty when the rows list who may call.
    pub note: Vec<Part>,
    pub rows: Vec<CallRow>,
}

/// The callers that reach the same ports.
#[derive(Debug, PartialEq)]
pub struct CallRow {
    pub who: Vec<Reference>,
    /// The ports.  A namespace's card omits them, because each of its
    /// workloads admits its own ports.
    pub ports: Option<String>,
}

/// The most callers a row names before it folds to their count.
const MOST: usize = 6;

impl CallRow {
    /// Whether the row folds to a count that opens to its callers, as a
    /// long list on a board does.
    pub fn folds(&self) -> bool {
        self.who.len() > MOST
    }
}

impl Calls {
    pub fn of_workload(allowed: &Allowed, workload: &Object, plurals: &Plurals) -> Self {
        let callers = match allowed.reach(workload) {
            None => return Self::default(),
            Some(Reach::Open) => return Self::noted(notes::open()),
            Some(Reach::MachineNetwork) => return Self::noted(notes::on_machine_network()),
            Some(Reach::Isolated(callers)) if callers.is_empty() => {
                return Self::noted(notes::no_caller());
            }
            Some(Reach::Isolated(callers)) => callers,
        };
        let calling: BTreeSet<&str> = callers
            .keys()
            .filter_map(|caller| match caller {
                Caller::Workload { namespace, .. } => Some(namespace.as_str()),
                _ => None,
            })
            .collect();
        let mut by_ports: BTreeMap<&Ports, Vec<&Caller>> = BTreeMap::new();
        for (caller, ports) in callers {
            by_ports.entry(ports).or_default().push(caller);
        }
        let rows = by_ports.into_iter().map(|(ports, callers)| CallRow {
            who: Folded::new(allowed, workload, &calling).who(&callers, ports, plurals),
            ports: Some(ports.to_string()),
        });
        Self {
            note: Vec::new(),
            rows: rows.collect(),
        }
    }

    /// The card of `namespace`, where `href` gives the page of each
    /// workload a note names.
    pub fn of_namespace(
        allowed: &Allowed,
        namespace: &str,
        href: impl Fn(&Object) -> Option<String>,
    ) -> Self {
        let callers = match allowed.of_namespace(namespace) {
            None => return Self::default(),
            Some(NamespaceReach::Open) => return Self::noted(notes::open()),
            Some(NamespaceReach::Partly {
                open,
                machine_network,
            }) => return Self::noted(notes::partly_open(&open, &machine_network, href)),
            Some(NamespaceReach::Isolated(callers)) if callers.is_empty() => {
                return Self::noted(notes::no_other_namespace());
            }
            Some(NamespaceReach::Isolated(callers)) => callers,
        };
        // The card shows a rule that admits every namespace as one
        // line, and the map tags no parcel for it, because nearly every
        // namespace may call.
        if allowed.admits_every_namespace_in(namespace) {
            let others = allowed.namespaces().filter(|&other| other != namespace);
            let but: Vec<String> = others
                .filter(|other| !callers.contains(*other))
                .map(Into::into)
                .collect();
            return Self::noted(notes::any_namespace(&but));
        }
        let who = callers
            .iter()
            .map(|name| Reference::to_namespace(name))
            .collect();
        Self {
            note: Vec::new(),
            rows: vec![CallRow { who, ports: None }],
        }
    }

    fn noted(note: Vec<Part>) -> Self {
        Self {
            note,
            rows: Vec::new(),
        }
    }
}

/// Folds one group of a workload's callers into what a card names.
struct Folded<'a> {
    allowed: &'a Allowed,
    target: &'a Object,
    /// The namespaces with a workload that may call on any port, which
    /// "any namespace but" leaves out of its exceptions.
    calling: &'a BTreeSet<&'a str>,
}

impl<'a> Folded<'a> {
    fn new(allowed: &'a Allowed, target: &'a Object, calling: &'a BTreeSet<&'a str>) -> Self {
        Self {
            allowed,
            target,
            calling,
        }
    }

    fn who(&self, callers: &[&Caller], ports: &Ports, plurals: &Plurals) -> Vec<Reference> {
        let refer = |named: Named| Reference {
            text: named.to_string(),
            href: None,
        };
        if callers.contains(&&Caller::Anything) {
            return vec![refer(Named::Anything)];
        }
        let mut by_namespace: BTreeMap<&str, BTreeSet<&Object>> = BTreeMap::new();
        let mut blocks = Vec::new();
        for caller in callers {
            match caller {
                Caller::Workload { namespace, owner } => {
                    by_namespace.entry(namespace).or_default().insert(owner);
                }
                Caller::Addresses { cidr, except } => {
                    blocks.push(refer(Named::Addresses { cidr, except }))
                }
                Caller::Anything => {}
            }
        }
        let whole: Vec<bool> = self
            .allowed
            .namespaces()
            .map(|namespace| self.whole(namespace, by_namespace.get(namespace)))
            .collect();
        let mut who = Vec::new();
        if !whole.is_empty() && whole.iter().all(|&whole| whole) {
            who.push(refer(Named::AnyPod));
        } else if self.allowed.admits_every_namespace(self.target, ports) {
            // The row lists first the callers that a rule names one by
            // one.  The rule that admits every namespace follows them
            // as one entry, which names the namespaces none of whose
            // pods may call.
            let here = self.target.namespace.as_deref();
            let mut but = Vec::new();
            for namespace in self.allowed.namespaces() {
                let owners = by_namespace.get(namespace);
                match owners {
                    Some(owners) if !self.whole(namespace, Some(owners)) => {
                        let ends = owners.iter().map(|&owner| Endpoint::Object(owner.clone()));
                        who.extend(ends.map(|end| Reference::to_end(&end, here, plurals)));
                    }
                    Some(_) => {}
                    None if self.whole(namespace, None) || self.calling.contains(namespace) => {}
                    None => but.push(namespace.to_string()),
                }
            }
            who.push(refer(Named::AnyNamespace {
                pods: true,
                but: &but,
            }));
        } else {
            let here = self.target.namespace.as_deref();
            for (namespace, owners) in &by_namespace {
                if self.whole(namespace, Some(owners)) {
                    who.push(Reference {
                        text: Named::EveryPod(namespace).to_string(),
                        href: Some(format!("/namespaces/{namespace}")),
                    });
                    continue;
                }
                let ends = owners.iter().map(|&owner| Endpoint::Object(owner.clone()));
                who.extend(ends.map(|end| Reference::to_end(&end, here, plurals)));
            }
        }
        who.extend(blocks);
        who
    }

    /// Whether the callers are every pod of `namespace`: a rule admits
    /// the namespace whole, and `callers` contains every workload of it
    /// except the target.  A namespace that contains only the target is
    /// whole, so it never stops the card from showing "any pod".
    fn whole(&self, namespace: &str, callers: Option<&BTreeSet<&Object>>) -> bool {
        let mut others = self
            .allowed
            .workloads(namespace)
            .filter(|&o| o != self.target)
            .peekable();
        if others.peek().is_none() {
            return true;
        }
        let Some(callers) = callers else {
            return false;
        };
        self.allowed.admits_whole(self.target, namespace)
            && others.all(|other| callers.contains(other))
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;
    use k8s_openapi::api::networking::v1::NetworkPolicy;

    use super::super::fixtures::{evaluate, namespaces, pod, pods, policy, to_db, workload};
    use transect_cluster::ownership::Ownership;

    /// The card's rows for the database, as each row's names and ports.
    fn rows_of_db(rule: &str) -> Vec<(Vec<String>, Option<String>)> {
        let allowed = evaluate(&[to_db(rule)]);
        let calls = Calls::of_workload(&allowed, &workload("shop", "db"), &Plurals::default());
        let rows = calls.rows.into_iter();
        rows.map(|row| (row.who.into_iter().map(|end| end.text).collect(), row.ports))
            .collect()
    }

    fn row(who: &[&str], ports: &str) -> (Vec<String>, Option<String>) {
        (who.iter().map(|&w| w.into()).collect(), Some(ports.into()))
    }

    #[rstest]
    #[case::one_workload(
        "{from: [{namespaceSelector: {matchLabels: {team: ops}}, podSelector: {matchLabels: {app: agent}}}]}",
        vec![row(&["Pod watch/agent"], "any port")]
    )]
    #[case::a_whole_namespace(
        "{from: [{namespaceSelector: {matchLabels: {team: ops}}}]}",
        vec![row(&["every pod in watch"], "any port")]
    )]
    #[case::its_own_namespace("{from: [{podSelector: {}}], ports: [{port: 5432}]}", vec![row(&["every pod in shop"], "port 5432")])]
    #[case::every_namespace("{from: [{namespaceSelector: {}}]}", vec![row(&["any pod"], "any port")])]
    #[case::anything("{ports: [{port: 5432}]}", vec![row(&["anything"], "port 5432")])]
    #[case::a_block_and_its_pods(
        "{from: [{ipBlock: {cidr: 10.2.0.0/16}}]}",
        vec![row(&["Pod watch/agent", "addresses in 10.2.0.0/16"], "any port")]
    )]
    fn folds_the_callers(#[case] rule: &str, #[case] expected: Vec<(Vec<String>, Option<String>)>) {
        assert_eq!(rows_of_db(rule), expected);
    }

    /// The callers' names in each of the database's rows, in row order.
    fn names_for_db(policies: &[NetworkPolicy]) -> Vec<Vec<String>> {
        let calls = Calls::of_workload(
            &evaluate(policies),
            &workload("shop", "db"),
            &Plurals::default(),
        );
        let rows = calls.rows.into_iter();
        rows.map(|row| row.who.into_iter().map(|end| end.text).collect())
            .collect()
    }

    const EVERY_NAMESPACE: &str = "{from: [{namespaceSelector: {}}]}";

    #[rstest]
    #[case::every_namespace_admitted(vec![to_db(EVERY_NAMESPACE)], vec![vec!["any pod"]])]
    #[case::one_kept_out_by_its_own_egress(
        vec![to_db(EVERY_NAMESPACE), policy("watch", "{podSelector: {}, policyTypes: [Egress]}")],
        vec![vec!["any pod in any namespace but watch"]]
    )]
    #[case::not_on_the_ports_of_another_rule(
        vec![
            to_db("{from: [{podSelector: {matchLabels: {app: web}}}]}"),
            to_db("{from: [{namespaceSelector: {}}], ports: [{port: 5432}]}"),
        ],
        vec![vec!["every pod in shop"], vec!["any pod in any namespace"]]
    )]
    fn says_any_namespace_only_for_a_rule_that_admits_every_one(
        #[case] policies: Vec<NetworkPolicy>,
        #[case] expected: Vec<Vec<&str>>,
    ) {
        assert_eq!(names_for_db(&policies), expected);
    }

    #[rstest]
    fn names_each_workload_of_a_namespace_partly_admitted() {
        let mut pods = pods();
        pods.push(pod("watch", "probe", "app: probe", "10.2.0.2"));
        let policies = [to_db(
            "{from: [{namespaceSelector: {matchLabels: {team: ops}}, podSelector: {matchLabels: {app: agent}}}]}",
        )];
        let namespaces = namespaces();
        let ownership = Ownership::default();
        let allowed = Allowed::evaluate(&policies, &pods, [], &namespaces, &ownership);
        let calls = Calls::of_workload(&allowed, &workload("shop", "db"), &Plurals::default());
        let names: Vec<&str> = calls.rows[0]
            .who
            .iter()
            .map(|end| end.text.as_str())
            .collect();
        assert_eq!(names, vec!["Pod watch/agent"]);
    }

    #[rstest]
    #[case::open(vec![], "No NetworkPolicy selects its pods, so any pod may call them.", vec![])]
    #[case::partly(
        vec![to_db("{from: [{podSelector: {}}]}")],
        "No NetworkPolicy selects the pods of Pod web, so any pod may call it.",
        vec![]
    )]
    #[case::isolated(
        vec![policy("shop", "{podSelector: {}, policyTypes: [Ingress], ingress: [{from: [{namespaceSelector: {matchLabels: {team: ops}}}]}]}")],
        "",
        vec!["watch"]
    )]
    #[case::isolated_from_everything(
        vec![policy("shop", "{podSelector: {}, policyTypes: [Ingress], ingress: [{from: [{namespaceSelector: {}}]}]}")],
        "Any namespace may call it.",
        vec![]
    )]
    #[case::isolated_from_namespaces_a_label_names(
        vec![policy("shop", "{podSelector: {}, policyTypes: [Ingress], ingress: [{from: [{namespaceSelector: {matchExpressions: [{key: tier, operator: NotIn, values: [none]}]}}]}]}")],
        "",
        vec!["blog", "watch"]
    )]
    fn says_who_may_call_a_namespace(
        #[case] policies: Vec<NetworkPolicy>,
        #[case] expected: &str,
        #[case] callers: Vec<&str>,
    ) {
        let calls = Calls::of_namespace(&evaluate(&policies), "shop", |_| None);
        let names: Vec<&str> = calls
            .rows
            .iter()
            .flat_map(|row| &row.who)
            .map(|end| end.text.as_str())
            .collect();
        let note: String = calls.note.iter().map(|part| part.text.as_str()).collect();
        assert_eq!((note.as_str(), names), (expected, callers));
    }

    #[rstest]
    fn says_no_policy_applies_on_the_machines_network() {
        let mut pods = pods();
        pods[0] = super::super::fixtures::on_machine_network(pods.remove(0));
        let policies = [to_db("{from: [{podSelector: {}}]}")];
        let allowed = Allowed::evaluate(&policies, &pods, [], &namespaces(), &Ownership::default());
        let calls = Calls::of_workload(&allowed, &workload("shop", "db"), &Plurals::default());
        let note: String = calls.note.iter().map(|part| part.text.as_str()).collect();
        assert_eq!(
            (note.as_str(), calls.rows.len()),
            (
                "It is on its machine's network, where no NetworkPolicy applies, so any pod may call it at its machine's address.",
                0
            )
        );
    }

    #[rstest]
    #[case::a_few(6, false)]
    #[case::more_than_a_card_shows(7, true)]
    fn folds_a_long_row_of_callers(#[case] count: usize, #[case] folds: bool) {
        let who = (0..count).map(|n| transect_graph::links::Reference {
            text: format!("caller-{n}"),
            href: None,
        });
        let row = CallRow {
            who: who.collect(),
            ports: None,
        };
        assert_eq!(row.folds(), folds);
    }
}
