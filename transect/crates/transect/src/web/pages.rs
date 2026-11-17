//! Each elevation's page, assembled from one reading of the cluster's
//! stores.  Every page reads the same things the same way, so the
//! cluster map and the namespace page agree on counts and health.
//!
//! Every page shows only the edge kinds its view turns on.  The graph
//! drops the other kinds before a page queries it, so the tags, card
//! lines, and loops of a kind that is off all drop out together.  What a
//! page reads apart from the graph, such as the policy summary and the
//! ground's claim counts, each page leaves out in its own code.

use k8s_openapi::jiff::Timestamp;

use super::model::Model;
use transect_graph::edges::{Endpoint, Kind, Object, Shown};
use transect_ground::devices::{self, Devices};
use transect_ground::loss::{self, Lost};
use transect_ground::machines;
use transect_ground::storage::Ground;
use transect_map::map::relation::{self, Relation};
use transect_map::map::{
    ClusterMap, ClusterPage, Contents, EdgeRow, GitOpsPage, NamespacePage, ON_THE_MAP, PodContents,
    PodPage, Sidebar, WorkloadContents, WorkloadPage, has_page, workload_card,
};
use transect_network::entry::Endpoints;
use transect_network::policy::NamespaceReach;
use transect_observe::health;
use transect_observe::usage;
use transect_resources::layers::Layers;
use transect_resources::resources::ResourcesPage;
use transect_supply::changed::Listed;
use transect_supply::findings;
use transect_supply::images::Origins;

/// What the cluster page draws besides the map: a namespace selected,
/// or the loss of a set of machines.
#[derive(Clone, Copy)]
pub enum Focus<'a> {
    Select(Option<&'a str>),
    /// The loss of the machines named, with a namespace selected in it
    /// or none.
    Lose(&'a [String], Option<&'a str>),
}

/// The cluster page, with `focus` selected when it names a namespace on
/// the map, or with its loss when it names machines the cluster has.  A
/// name the map does not hold, such as a namespace deleted since its link
/// was copied, draws the plain cluster.
pub fn cluster_page(
    model: &Model,
    layers: &Layers,
    usage: &usage::Reading,
    focus: Focus,
    shown: &Shown,
    now: Timestamp,
) -> ClusterPage {
    let snapshot = &model.snapshot;
    let minute = model.minute(now);
    let health = &minute.health;
    let health_of = |namespace: &str| health.get(namespace).cloned().unwrap_or_default();
    let mut map = snapshot.map(layers, health.clone());
    let mut machines = snapshot.machines(usage);
    let (select, lose) = match focus {
        Focus::Select(select) => (select, &[][..]),
        Focus::Lose(lose, select) => (select, lose),
    };
    let allowed = model.allowed(shown);
    let graph = model.graph(shown);
    let edge_set = model.edge_set(shown);
    let selected = select.and_then(|namespace| map.select(namespace));
    // When a machine is really lost, the page at `/` draws its loss
    // without a `lose` parameter, and a loss the URL asks for adds to
    // it.  When a namespace is selected, the page shows that namespace's
    // card in place of the loss.
    let gone = loss::not_ready(&snapshot.nodes, now);
    let real = if selected.is_some() {
        &[][..]
    } else {
        &gone[..]
    };
    let asked = lose.iter().map(String::as_str);
    let asked = asked.chain(real.iter().map(|gone| gone.machine.as_str()));
    let names = machines.iter().map(|machine| machine.name.as_str());
    let lost = Lost::of(asked, names);
    let known: Vec<&str> = machines
        .iter()
        .map(|machine| machine.name.as_str())
        .collect();
    let unknown = loss::unknown_text(&loss::unknown(lose.iter().map(String::as_str), &known));
    let loss = (!lost.is_empty()).then(|| {
        let asked = Asked {
            lost,
            real,
            unknown: unknown.clone(),
        };
        let workloads = snapshot.workloads();
        loss_sidebar(snapshot, &graph, &workloads, asked, &mut map, &mut machines)
    });
    let loops = &edge_set.loops;
    map.mark_loops(loops);
    let mut card = None;
    if let Some(selected) = &selected {
        machines::count_here(&mut machines, &graph, &selected.namespace);
        devices::name_here(&mut machines, &graph, &selected.namespace);
        for (namespace, relations) in relation::across(&graph, &selected.namespace) {
            for found in relations {
                map.relate(&namespace, found);
            }
        }
        // Only callers a policy names get a tag.  A rule that admits
        // every namespace is one line on the card instead.
        let everyone = allowed.admits_every_namespace_in(&selected.namespace);
        if let Some(NamespaceReach::Isolated(callers)) = allowed.of_namespace(&selected.namespace)
            && !everyone
        {
            for namespace in callers {
                map.relate(&namespace, Relation::may_call());
            }
        }
        let health = health_of(&selected.namespace);
        let kept = (&edge_set.forges, &minute.logged[..]);
        let found = snapshot.card(&selected.namespace, health, &graph, kept, allowed);
        card = Some(found.with_loops(loops));
    }
    let found = model.workload_findings.iter().cloned();
    let happened = findings::Happened {
        patterns: minute.logged.clone(),
        restarted: minute.restarted.clone(),
    };
    let census = model.census.clone();
    let mut sidebar = Sidebar::build(
        &map,
        findings::find(&machines, found, census, happened, loops.clone()),
        minute.firing.clone(),
        snapshot.alerts.off_line(&snapshot.resources.directory),
        (&snapshot.ownership, &snapshot.plurals),
    );
    let changes = edge_set.recorded.arranged(&minute.firing, now);
    sidebar.changed = Listed::sidebar(&changes, now);
    sidebar.unknown = unknown.filter(|_| loss.is_none());
    // The ground's volume counts for the selection are its stores-on
    // edges, read from the claims themselves.
    let ground = Ground::build(
        &snapshot.storage,
        &graph,
        selected
            .as_ref()
            .map(|selected| selected.namespace.as_str()),
        shown.is_on(Kind::StoresOn),
    )
    .with_about(|name| {
        let outside = snapshot.outsides.titled(name);
        outside.map(|outside| outside.lines()).unwrap_or_default()
    })
    .with_pages(|name| {
        let outsides = &snapshot.outsides;
        (outsides.page_of(name), outsides.link_of(name))
    });
    let here = match (&selected, &loss) {
        (Some(selected), Some(loss)) if !loss.all_real() => {
            format!("/?lose={}&select={}", loss.param(), selected.namespace)
        }
        (Some(selected), _) => format!("/?select={}", selected.namespace),
        (None, Some(loss)) if !loss.all_real() => format!("/?lose={}", loss.param()),
        (None, _) => "/".into(),
    };
    ClusterPage {
        map,
        selected,
        machines,
        ground,
        usage_note: usage.off_line(&snapshot.resources.directory),
        devices_note: snapshot.devices.off.map(devices::Off::note),
        sidebar,
        card,
        loss,
        edge_row: EdgeRow::build(&snapshot.offered(), shown, &here).affecting(ON_THE_MAP),
    }
}

/// The page for `namespace`, or `None` when the map holds no such
/// namespace.
pub fn namespace_page(
    model: &Model,
    layers: &Layers,
    usage: &usage::Reading,
    namespace: &str,
    shown: &Shown,
    now: Timestamp,
) -> Option<NamespacePage> {
    let snapshot = &model.snapshot;
    let objects = snapshot.objects();
    let minute = model.minute(now);
    let mut health = minute.health.clone();
    let namespace_health = health.remove(namespace).unwrap_or_default();
    let map = snapshot.map(layers, health);
    let allowed = model.allowed(shown);
    let graph = model.graph(shown);
    let edge_set = model.edge_set(shown);
    let mut machines = snapshot.machines(usage);
    machines::count_here(&mut machines, &graph, namespace);
    let by_workload = health::by_workload(&namespace_health, namespace, &snapshot.ownership);
    let loops = &edge_set.loops;
    let contents = Contents {
        workloads: snapshot.ownership.workloads(namespace).collect(),
        pods: &snapshot.pods,
        graph: &graph,
        ownership: &snapshot.ownership,
        objects: &objects,
        services: &snapshot.services,
        by_workload: &by_workload,
        storage: &snapshot.storage,
        plurals: &snapshot.plurals,
        loops,
        nodes: &snapshot.nodes,
        resources: snapshot.resources_in(namespace),
        now,
    };
    let kept = (&edge_set.forges, &minute.logged[..]);
    let card = snapshot.card(namespace, namespace_health, &graph, kept, allowed);
    let card = card.with_loops(loops);
    let page = NamespacePage::build(&map, namespace, contents, &machines, card)?;
    let here = format!("/namespaces/{namespace}");
    Some(page.with_edge_row(EdgeRow::build(&snapshot.offered(), shown, &here)))
}

/// A workload a page is asked for: its namespace, the resource of its
/// kind as the URL names it, and its name.
#[derive(Clone, Copy)]
pub struct WorkloadAt<'a> {
    pub namespace: &'a str,
    pub resource: &'a str,
    pub name: &'a str,
}

/// The page for the workload `namespace/name` of the kind `resource`
/// names, or `None` when the namespace holds no such workload.
pub fn workload_page(
    model: &Model,
    layers: &Layers,
    usage: &usage::Reading,
    at: WorkloadAt,
    shown: &Shown,
    now: Timestamp,
) -> Option<WorkloadPage> {
    let WorkloadAt {
        namespace,
        resource,
        name,
    } = at;
    let page = read_workloads(
        model,
        layers,
        usage,
        namespace,
        shown,
        |map, contents| WorkloadPage::build(map, namespace, resource, name, contents),
        now,
    )?;
    let page = page.with_access(has_access(model, namespace, resource, name));
    let row = EdgeRow::build(&model.snapshot.offered(), shown, &page.path);
    Some(page.with_edge_row(row))
}

/// The page of the GitOps object `namespace/name` of the kind
/// `resource` names, or `None` when there is no such object.  It shares
/// the workload page's URL, since a GitOps object's kind is a custom
/// one.
pub fn gitops_page(
    model: &Model,
    layers: &Layers,
    namespace: &str,
    resource: &str,
    name: &str,
    shown: &Shown,
) -> Option<GitOpsPage> {
    let snapshot = &model.snapshot;
    let (gitops, plurals) = (&snapshot.gitops, &snapshot.plurals);
    let object = gitops.objects().find(|object| {
        object.namespace.as_deref() == Some(namespace)
            && object.name == name
            && transect_graph::links::kinds::workload(object, plurals).as_deref() == Some(resource)
    })?;
    let detail = gitops.detail(object)?;
    let path = transect_graph::links::page(object, plurals)?;
    let layer = snapshot.layer_of(namespace, layers);
    let graph = model.graph(shown);
    let end = Endpoint::Object(object.clone());
    let reconciling = graph.reaching(Kind::ReconcilesFrom, &end);
    let reconciling = reconciling.into_iter().filter_map(|end| match end {
        Endpoint::Object(object) => Some(object),
        _ => None,
    });
    let pages = snapshot.pages();
    let href = |object: &Object| pages.href(object);
    let page = GitOpsPage::build(detail, layer, reconciling, href, path)
        .with_siblings(gitops.objects(), |object| {
            transect_graph::links::page(object, plurals)
        });
    let row = EdgeRow::build(&snapshot.offered(), shown, &page.path);
    Some(page.with_edge_row(row))
}

/// Reads what a workload's page and its section draw from, and passes
/// it to `build` with the map.  The two draw one workload's card, so
/// they read it the same way.
fn read_workloads<T>(
    model: &Model,
    layers: &Layers,
    usage: &usage::Reading,
    namespace: &str,
    shown: &Shown,
    build: impl FnOnce(&ClusterMap, WorkloadContents) -> Option<T>,
    now: Timestamp,
) -> Option<T> {
    let snapshot = &model.snapshot;
    let mut health = model.minute(now).health.clone();
    let namespace_health = health.remove(namespace).unwrap_or_default();
    let map = snapshot.map(layers, health);
    let by_workload = health::by_workload(&namespace_health, namespace, &snapshot.ownership);
    let workloads = snapshot.workloads();
    let allowed = model.allowed(shown);
    let graph = model.graph(shown);
    let endpoints = Endpoints::build(&snapshot.endpoint_slices);
    let edge_set = model.edge_set(shown);
    let forges = &edge_set.forges;
    let serving = snapshot.serving();
    let ways_in = model.ways_in();
    let contents = WorkloadContents {
        held: &snapshot.held,
        workloads: &workloads,
        owners: snapshot.ownership.workloads(namespace).collect(),
        ownership: &snapshot.ownership,
        plurals: &snapshot.plurals,
        budgets: &snapshot.budgets,
        graph: &graph,
        gitops: &snapshot.gitops,
        forges,
        images: &snapshot.images,
        addresses: &snapshot.addresses,
        services: &snapshot.services,
        ingresses: &snapshot.ingresses,
        endpoints: &endpoints,
        issuers: &snapshot.issuers,
        storage: &snapshot.storage,
        by_workload: &by_workload,
        usage: usage.usage(),
        usage_note: usage.off_line(&snapshot.resources.directory),
        rbac_missing: &snapshot.grants.missing,
        allowed,
        intents: &snapshot.intents,
        unread: &model.unread,
        kinds: &snapshot.kinds,
        relations: &snapshot.relations,
        definitions: &snapshot.definitions,
        custom: &snapshot.custom,
        serving: &serving,
        ways_in: &ways_in,
        vulnerabilities: &model.vulnerabilities,
        traffic: snapshot.traffic(),
        now,
    };
    build(&map, contents)
}

/// The page for the pod `namespace/name`, or `None` when the cluster
/// holds no such pod.
pub fn pod_page(
    model: &Model,
    layers: &Layers,
    usage: &usage::Reading,
    namespace: &str,
    name: &str,
    shown: &Shown,
    now: Timestamp,
) -> Option<PodPage> {
    let snapshot = &model.snapshot;
    let map = snapshot.map(layers, model.minute(now).health.clone());
    let workloads = snapshot.workloads();
    let machines = snapshot.machines(usage);
    let none_held = Devices::default();
    let edge_set = model.edge_set(shown);
    let secrets = snapshot.secret_intent();
    let contents = PodContents {
        pods: &snapshot.pods,
        workloads: &workloads,
        ownership: &snapshot.ownership,
        plurals: &snapshot.plurals,
        machines: &machines,
        storage: &snapshot.storage,
        devices: if shown.is_on(Kind::Holds) {
            &snapshot.devices
        } else {
            &none_held
        },
        usage: usage.usage(),
        usage_note: usage.off_line(&snapshot.resources.directory),
        origins: Origins {
            known: &snapshot.images,
            forges: &edge_set.forges,
        },
        secrets: &secrets,
        now,
    };
    let page = PodPage::build(&map, namespace, name, contents)?;
    let row = EdgeRow::build(&snapshot.offered(), shown, &page.path);
    Some(page.with_edge_row(row))
}

/// `transect`'s own resources, each with the conditions that loading it
/// returned and those this reading of the cluster returns.
pub fn resources_page(model: &Model, usage: &usage::Reading) -> ResourcesPage {
    let snapshot = &model.snapshot;
    let mut conditions = snapshot.conditions();
    let forges = &model.edge_set(&Shown::default()).forges;
    conditions.extend(snapshot.unmatched(usage, forges));
    ResourcesPage::build(&snapshot.resources.directory, conditions)
}

mod access;
mod changed;
mod gone;
mod ground;
mod kinds;
mod known;
mod logs;
mod loss_view;
mod machine;
mod manages;
mod object;
mod objects;
mod search;
mod section;
mod vulnerabilities;

pub use access::{
    access_page, accounts_page, has_access, namespace_access_page, read_ways, service_account_page,
    subject_page, ways_page,
};
pub use changed::changed_page;
pub use gone::gone_page;
pub use ground::{ground_link, ground_page};
pub use kinds::{kind_page, kinds_page};
pub use known::known_page;
pub use logs::{Lens, logs_page, shown_pod};
use loss_view::{Asked, loss_sidebar};
pub use machine::machine_page;
pub use manages::{manages, workload_at};
pub use object::{ObjectAt, VolumeAt, object_page, read_key, volume_page};
pub use objects::objects_page;
pub use search::search_results;
pub use section::section_page;
pub use vulnerabilities::vulnerabilities_page;

#[cfg(test)]
mod alerted;
#[cfg(test)]
mod tags;
#[cfg(test)]
mod tests;
