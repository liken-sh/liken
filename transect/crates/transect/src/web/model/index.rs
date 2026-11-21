//! The search index of one model: an entry for each fixed page,
//! namespace, machine, custom kind, outside system, and object that has
//! a page, for each group and user that a binding refers to, and for each
//! host that an `Ingress` serves, which opens the workload behind it.

use std::collections::{BTreeSet, HashMap, HashSet};
use std::sync::Arc;

use kube::ResourceExt;

use super::super::snapshot::Snapshot;
use transect_access::access::Access;
use transect_graph::edges::{Endpoint, Graph, Kind, Object};
use transect_map::search::{Entry, Index, Sort};

mod subjects;

/// The pages that every cluster has, which an empty query lists, as
/// the name, the path, and other names that also find the page.
const FIXED: [(&str, &str, &[&str]); 7] = [
    ("cluster", "/", &[]),
    ("access", "/access", &["ways in"]),
    ("accounts", "/access/accounts", &[]),
    ("changed", "/changed", &[]),
    ("kinds", "/kinds", &[]),
    ("resources", "/resources", &[]),
    ("shapes", "/shapes", &["sketches"]),
];

/// Hosts resolve to workloads through `graph`, `access` supplies the
/// groups and users, and each workload in `serving` gets an access lens.
pub fn index(
    snapshot: &Snapshot,
    graph: &Graph,
    access: &Access,
    serving: &HashSet<Object>,
) -> Index {
    let pages = snapshot.pages();
    let names = kind_names(snapshot);
    let of_kind = |object: &Object| {
        let key = (object.group.clone(), object.kind.clone());
        names.get(&key).cloned().unwrap_or_else(|| Arc::new([]))
    };
    let machine_names = machine_names(snapshot, &names);
    let mut entries: Vec<Entry> = FIXED
        .iter()
        .map(|(name, href, aliases)| Entry {
            aliases: aliases.iter().map(|alias| alias.to_string()).collect(),
            kind_names: Arc::new([]),
            name: (*name).into(),
            kind: "page".into(),
            sort: Sort::Page,
            context: None,
            href: (*href).into(),
            object: None,
            lenses: Vec::new(),
        })
        .collect();
    entries.extend(snapshot.namespaces.iter().map(|namespace| {
        let name = namespace.name_any();
        let href = transect_graph::links::namespace(&name);
        Entry {
            aliases: Vec::new(),
            kind_names: Arc::new([]),
            lenses: vec![("access", format!("{href}/access"))],
            href,
            object: Some(Object::new("", "Namespace", None, &name)),
            name,
            kind: "namespace".into(),
            sort: Sort::Namespace,
            context: None,
        }
    }));
    entries.extend(snapshot.nodes.iter().map(|node| {
        let name = node.name_any();
        Entry {
            aliases: Vec::new(),
            kind_names: machine_names.clone(),
            href: transect_graph::links::machine(&name),
            lenses: vec![("if lost", format!("/?lose={name}"))],
            object: Some(Object::new("", "Node", None, &name)),
            name,
            kind: "machine".into(),
            sort: Sort::Machine,
            context: None,
        }
    }));
    entries.extend(kinds(snapshot, &names));
    entries.extend(outside_systems(snapshot));
    let workloads: BTreeSet<&Object> = snapshot.ownership.all_workloads().collect();
    for &workload in &workloads {
        let Some(href) = pages.href(workload) else {
            continue;
        };
        entries.push(Entry {
            aliases: Vec::new(),
            kind_names: of_kind(workload),
            name: workload.name.clone(),
            kind: workload.kind.clone(),
            sort: Sort::Workload,
            context: workload.namespace.clone(),
            lenses: [
                ("section", format!("{href}/section")),
                ("logs", format!("{href}/logs")),
            ]
            .into_iter()
            .chain(
                serving
                    .contains(workload)
                    .then(|| ("access", format!("{href}/access"))),
            )
            .collect(),
            href,
            object: Some(workload.clone()),
        });
    }
    // An object that describes a machine has no row of its own. The
    // machine's row is found by that object's kind names instead, so a
    // query returns one row per machine.
    let describing = snapshot.describing_machines();
    let describes_a_machine = |object: &Object| describing.contains(object);
    let others: BTreeSet<&Object> = snapshot
        .relations
        .keys()
        .chain(snapshot.gitops.objects())
        .chain(snapshot.with_pages.iter())
        .filter(|object| !workloads.contains(object) && !describes_a_machine(object))
        .filter(|object| {
            !matches!(
                (object.group.as_str(), object.kind.as_str()),
                ("", "Namespace" | "Node")
            )
        })
        .collect();
    for object in others {
        let Some(href) = pages.href(object) else {
            continue;
        };
        entries.push(Entry {
            aliases: Vec::new(),
            kind_names: of_kind(object),
            name: object.name.clone(),
            kind: object.kind.clone(),
            sort: Sort::Object,
            context: object.namespace.clone(),
            href,
            object: Some(object.clone()),
            lenses: Vec::new(),
        });
    }
    entries.extend(snapshot.pods.iter().filter_map(|pod| {
        let namespace = pod.namespace()?;
        let name = pod.name_any();
        let href = transect_graph::links::pod(&namespace, &name);
        Some(Entry {
            aliases: Vec::new(),
            kind_names: Arc::new([]),
            lenses: vec![("logs", format!("{href}/logs"))],
            href,
            object: Some(Object::new("", "Pod", Some(&namespace), &name)),
            name,
            kind: "Pod".into(),
            sort: Sort::Pod,
            context: Some(namespace),
        })
    }));
    entries.extend(hosts(snapshot, graph));
    entries.extend(subjects::subjects(access));
    Index::new(entries)
}

/// The names that find each custom kind, keyed by API group and kind:
/// the lowercase kind and plural, then the names from its CRD.
fn kind_names(snapshot: &Snapshot) -> HashMap<(String, String), Arc<[String]>> {
    let kinds = snapshot.custom.iter().map(|(resource, _)| {
        let defined = snapshot.definitions.names(&resource.group, &resource.kind);
        let own = [resource.kind.to_lowercase(), resource.plural.to_lowercase()];
        let mut names: Vec<String> = Vec::new();
        for name in own.into_iter().chain(defined.iter().cloned()) {
            if !names.contains(&name) {
                names.push(name);
            }
        }
        let key = (resource.group.clone(), resource.kind.clone());
        (key, names.into())
    });
    kinds.collect()
}

/// The names of every kind whose objects describe a machine.
fn machine_names(
    snapshot: &Snapshot,
    names: &HashMap<(String, String), Arc<[String]>>,
) -> Arc<[String]> {
    let mut kinds: Vec<&(String, String)> = snapshot.describing_kinds().collect();
    kinds.sort();
    let mut all: Vec<String> = Vec::new();
    for name in kinds
        .into_iter()
        .filter_map(|key| names.get(key))
        .flat_map(|n| n.iter())
    {
        if !all.contains(name) {
            all.push(name.clone());
        }
    }
    all.into()
}

/// One entry per `Outside`, found by its title, its name, and each
/// address and driver it lists, and one entry for each storage backend
/// whose name is not the title of an `Outside`.
fn outside_systems(snapshot: &Snapshot) -> Vec<Entry> {
    let outsides = &snapshot.outsides;
    let entry = |name: &str, aliases: Vec<String>| Entry {
        name: name.into(),
        aliases,
        kind_names: Arc::new([]),
        kind: "ground".into(),
        sort: Sort::Ground,
        context: None,
        href: outsides.page_of(name),
        object: None,
        lenses: Vec::new(),
    };
    let mut entries: Vec<Entry> = outsides
        .iter()
        .map(|outside| {
            let aliases = std::iter::once(&outside.name)
                .chain(&outside.addresses)
                .chain(&outside.drivers)
                .map(|alias| alias.to_lowercase());
            entry(&outside.title, aliases.collect())
        })
        .collect();
    let titles: BTreeSet<&str> = outsides.iter().map(|o| o.title.as_str()).collect();
    let backends = snapshot.storage.tally(None).into_keys();
    for backend in backends.filter(|backend| !titles.contains(backend)) {
        entries.push(entry(backend, Vec::new()));
    }
    entries
}

/// One entry per custom kind, which opens the page that lists the
/// objects of that kind.
fn kinds(snapshot: &Snapshot, names: &HashMap<(String, String), Arc<[String]>>) -> Vec<Entry> {
    let kinds = snapshot.custom.iter().map(|(resource, _)| {
        let key = (resource.group.clone(), resource.kind.clone());
        Entry {
            name: resource.kind.clone(),
            aliases: names.get(&key).map(|n| n.to_vec()).unwrap_or_default(),
            kind_names: Arc::new([]),
            kind: "kind".into(),
            sort: Sort::Kind,
            context: Some(resource.group.clone()),
            href: transect_graph::links::kind(&resource.group, &resource.plural),
            object: None,
            lenses: Vec::new(),
        }
    });
    kinds.collect()
}

/// One entry per host that an `Ingress` serves, for each workload that
/// a `Service` of the `Ingress` selects.
fn hosts(snapshot: &Snapshot, graph: &Graph) -> Vec<Entry> {
    let pages = snapshot.pages();
    let mut entries = Vec::new();
    for ingress in &snapshot.ingresses {
        let Some(namespace) = ingress.namespace() else {
            continue;
        };
        let object = Object::new(
            "networking.k8s.io",
            "Ingress",
            Some(&namespace),
            &ingress.name_any(),
        );
        let services = graph.leaving(Kind::RoutesTo, &Endpoint::Object(object));
        let behind: BTreeSet<&Object> = services
            .into_iter()
            .flat_map(|service| graph.leaving(Kind::Selects, service))
            .filter_map(|end| match end {
                Endpoint::Object(workload) => Some(workload),
                _ => None,
            })
            .collect();
        let rules = ingress
            .spec
            .iter()
            .flat_map(|spec| spec.rules.iter().flatten());
        let hosts: BTreeSet<&str> = rules.filter_map(|rule| rule.host.as_deref()).collect();
        for host in hosts {
            for &workload in &behind {
                let Some(href) = pages.href(workload) else {
                    continue;
                };
                entries.push(Entry {
                    aliases: Vec::new(),
                    kind_names: Arc::new([]),
                    name: host.into(),
                    kind: "host".into(),
                    sort: Sort::Host,
                    context: Some(transect_graph::links::name_in(workload, None)),
                    href,
                    object: Some(workload.clone()),
                    lenses: Vec::new(),
                });
            }
        }
    }
    entries
}

impl super::Model {
    /// The entries that `query` finds when it is an address, such as a
    /// host or a `Service`'s DNS name: each machine it resolves to, and
    /// the workloads that each `Service` it resolves to selects. A query
    /// with no `.`, `:`, or `/` is not an address.
    pub fn addressed(&self, query: &str) -> Vec<Entry> {
        let query = query.trim();
        if !query.contains(['.', ':', '/']) {
            return Vec::new();
        }
        let snapshot = &self.snapshot;
        let pages = snapshot.pages();
        let mut entries = Vec::new();
        for address in transect_network::addresses::addresses(query) {
            for end in snapshot.addresses.resolve(&address, None) {
                let behind: Vec<&Object> = match end {
                    Endpoint::Machine(name) => {
                        entries.push(Entry {
                            aliases: Vec::new(),
                            kind_names: Arc::new([]),
                            name: query.into(),
                            kind: "address".into(),
                            sort: Sort::Host,
                            context: Some(format!("machine {name}")),
                            href: transect_graph::links::machine(name),
                            object: None,
                            lenses: Vec::new(),
                        });
                        continue;
                    }
                    _ => self
                        .graph
                        .leaving(Kind::Selects, end)
                        .into_iter()
                        .filter_map(|end| match end {
                            Endpoint::Object(workload) => Some(workload),
                            _ => None,
                        })
                        .collect(),
                };
                for workload in behind {
                    let Some(href) = pages.href(workload) else {
                        continue;
                    };
                    entries.push(Entry {
                        aliases: Vec::new(),
                        kind_names: Arc::new([]),
                        name: query.into(),
                        kind: "address".into(),
                        sort: Sort::Host,
                        context: Some(transect_graph::links::name_in(workload, None)),
                        href,
                        object: Some(workload.clone()),
                        lenses: Vec::new(),
                    });
                }
            }
        }
        entries
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use crate::web::fixture;
    use crate::web::pages::search_results;

    /// The link, kind, and context of the first search result for
    /// `query` in the fixture cluster.
    fn first(query: &str) -> Option<(String, String, Option<String>)> {
        let results = search_results(&fixture::model(), query, transect_map::search::DIALOG_LIST);
        let row = results.rows.into_iter().next()?;
        Some((row.href, row.kind, row.context))
    }

    fn row(
        href: &str,
        kind: &str,
        context: Option<&str>,
    ) -> Option<(String, String, Option<String>)> {
        Some((href.into(), kind.into(), context.map(Into::into)))
    }

    #[rstest]
    #[case::a_workload(
        "storefront",
        row(
            "/namespaces/acme-shop/deployments/storefront",
            "Deployment",
            Some("acme-shop")
        )
    )]
    #[case::part_of_a_host(
        "shop.exa",
        row(
            "/namespaces/acme-shop/deployments/storefront",
            "host",
            Some("acme-shop/storefront")
        )
    )]
    #[case::a_services_address(
        "ledger-db-svc.acme-ledger.svc.cluster.local",
        row(
            "/namespaces/acme-ledger/statefulsets/ledger-db",
            "address",
            Some("acme-ledger/ledger-db")
        )
    )]
    #[case::a_machine("node-be", row("/machines/node-beta", "machine", None))]
    #[case::a_pod(
        "log-shipper-bb",
        row("/namespaces/acme-ops/pods/log-shipper-bbbb", "Pod", Some("acme-ops"))
    )]
    #[case::nothing_typed("", row("/", "page", None))]
    #[case::the_lens("access", row("/access", "page", None))]
    #[case::ways_in("ways in", row("/access", "page", None))]
    #[case::the_start_of_ways_in("ways", row("/access", "page", None))]
    #[case::the_accounts("accounts", row("/access/accounts", "page", None))]
    #[case::a_kind(
        "gizmo",
        row(
            "/kinds/gizmos.gizmos.example.test",
            "kind",
            Some("gizmos.example.test")
        )
    )]
    #[case::a_kinds_plural(
        "gizmos",
        row(
            "/kinds/gizmos.gizmos.example.test",
            "kind",
            Some("gizmos.example.test")
        )
    )]
    #[case::an_outside_system_by_its_title("archive", row("/ground/archive-nas", "ground", None))]
    #[case::an_outside_system_by_an_address(
        "nas.example.te",
        row("/ground/archive-nas", "ground", None)
    )]
    #[case::a_volume_by_its_claim(
        "data-ledger",
        row(
            "/namespaces/acme-ledger/persistentvolumeclaims/data-ledger-db-0",
            "PersistentVolumeClaim",
            Some("acme-ledger")
        )
    )]
    #[case::nothing_matches("zzzz", None)]
    fn finds_what_a_query_names(
        #[case] query: &str,
        #[case] expected: Option<(String, String, Option<String>)>,
    ) {
        assert_eq!(first(query), expected);
    }

    /// The link of each search result for `query`.
    fn hrefs(query: &str) -> Vec<String> {
        let results = search_results(&fixture::model(), query, transect_map::search::DIALOG_LIST);
        results.rows.into_iter().map(|row| row.href).collect()
    }

    #[rstest]
    #[case::by_its_name("node-alpha", &["/machines/node-alpha"])]
    #[case::by_the_kind_that_describes_it(
        "machine",
        &["/kinds/machines.liken.sh", "/machines/node-alpha", "/machines/node-beta"]
    )]
    fn finds_a_machine_once_whatever_describes_it(#[case] query: &str, #[case] rows: &[&str]) {
        assert_eq!(hrefs(query), rows);
    }
}
