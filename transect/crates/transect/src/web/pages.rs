//! Builds the data of each page from one `Model` of the cluster.  Every
//! page reads the same values the same way, so the cluster map and the
//! namespace page agree on counts and health.
//!
//! A view turns some edge kinds on and the rest off.  `model.graph(shown)`
//! drops the kinds that are off, so the tags, card lines, and loops of
//! an off kind disappear together.  Data that does not come from the
//! graph, such as the policy summary and the ground's volume counts, is
//! filtered by `shown` separately.

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
use transect_map::shapes::ShapesPage;
use transect_network::entry::Endpoints;
use transect_network::policy::NamespaceReach;
use transect_observe::health;
use transect_observe::usage;
use transect_resources::layers::Layers;
use transect_resources::resources::ResourcesPage;
use transect_supply::changed::Listed;
use transect_supply::findings;
use transect_supply::images::Origins;

/// What the cluster page shows in addition to the map: a selected
/// namespace, or a set of lost machines.
#[derive(Clone, Copy)]
pub enum Focus<'a> {
    Select(Option<&'a str>),
    /// The machines to draw as lost, and the namespace to select, if
    /// any.
    Lose(&'a [String], Option<&'a str>),
}

/// The cluster page for `focus`.  A namespace that the map does not
/// hold, such as one deleted after its link was copied, draws the plain
/// cluster.  Machine names that the cluster does not have are reported
/// as unknown and are not drawn as lost.
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
    // Machines that are not ready are drawn as lost, together with the
    // machines named in `lose`.  While a namespace is selected, only the
    // machines in `lose` are drawn as lost.
    let not_ready = loss::not_ready(&snapshot.nodes, now);
    let real = if selected.is_some() {
        &[][..]
    } else {
        &not_ready[..]
    };
    let lost_names = lose.iter().map(String::as_str);
    let lost_names = lost_names.chain(real.iter().map(|gone| gone.machine.as_str()));
    let names = machines.iter().map(|machine| machine.name.as_str());
    let lost = Lost::of(lost_names, names);
    let known: Vec<&str> = machines
        .iter()
        .map(|machine| machine.name.as_str())
        .collect();
    let unknown = loss::unknown_text(&loss::unknown(lose.iter().map(String::as_str), &known));
    let loss = (!lost.is_empty()).then(|| {
        let request = LossRequest {
            lost,
            real,
            unknown: unknown.clone(),
        };
        let workloads = snapshot.workloads();
        loss_sidebar(
            snapshot,
            &graph,
            &workloads,
            request,
            &mut map,
            &mut machines,
        )
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
        // Only a namespace that a policy names as a caller gets a tag.
        // When a rule admits every namespace, the card shows one line
        // for it instead.
        let everyone = allowed.admits_every_namespace_in(&selected.namespace);
        if let Some(NamespaceReach::Isolated(callers)) = allowed.of_namespace(&selected.namespace)
            && !everyone
        {
            for namespace in callers {
                map.relate(&namespace, Relation::may_call());
            }
        }
        let health = health_of(&selected.namespace);
        let card_inputs = (&edge_set.forges, &minute.logged[..]);
        let found = snapshot.card(&selected.namespace, health, &graph, card_inputs, allowed);
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
    // The volume counts of the selected namespace on each ground come
    // from the `StoresOn` edges, which the graph builds from the claims.
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
    let card_inputs = (&edge_set.forges, &minute.logged[..]);
    let card = snapshot.card(namespace, namespace_health, &graph, card_inputs, allowed);
    let card = card.with_loops(loops);
    let page = NamespacePage::build(&map, namespace, contents, &machines, card)?;
    let here = format!("/namespaces/{namespace}");
    Some(page.with_edge_row(EdgeRow::build(&snapshot.offered(), shown, &here)))
}

/// Identifies the workload to draw: its namespace, the resource name
/// that its URL uses for its kind, and its name.
#[derive(Clone, Copy)]
pub struct WorkloadAt<'a> {
    pub namespace: &'a str,
    pub resource: &'a str,
    pub name: &'a str,
}

/// The page for the workload `at`, or `None` when its namespace has no
/// such workload.
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

/// The page of the GitOps object `name` in `namespace`, where `resource`
/// is the plural resource name from the URL, or `None` when there is no
/// such object.  A GitOps object is a custom resource, so its URL has
/// the same form as a workload page's URL.
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

/// Collects the data that every page of one workload in `namespace`
/// draws, and passes it with the map to `build`.
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
        with_pages: &snapshot.with_pages,
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

/// The page of every shape that `transect` has loaded, and of each
/// `Shape` under `--resources` that did not load.
pub fn shapes_page(model: &Model) -> ShapesPage {
    let resources = &model.snapshot.resources;
    ShapesPage::build(&resources.shapes(), &resources.directory)
}

/// The resources page: `transect`'s own resources, each with the
/// conditions from loading it and the conditions that this reading of
/// the cluster adds.
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
use loss_view::{LossRequest, loss_sidebar};
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
