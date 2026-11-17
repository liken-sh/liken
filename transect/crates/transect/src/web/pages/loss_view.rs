//! The cluster page's loss: what losing a set of machines takes, read
//! from the snapshot, marked on the map and the machines band, and
//! described in the sidebar.

use crate::web::snapshot::Snapshot;
use transect_cluster::workloads::Workloads;
use transect_graph::edges::Graph;
use transect_ground::loss::{self, Gone, Lost, Majority, Sidebar as LossSidebar};
use transect_ground::machines::Machine;
use transect_map::map::ClusterMap;
use transect_resources::resources::OffLine;

/// What a loss asks: the machines, which of them are really lost, and
/// the text the page shows for the names it does not know.
pub struct Asked<'a> {
    pub lost: Lost,
    pub real: &'a [Gone],
    pub unknown: Option<String>,
}

/// The loss of `asked`, marked on `map` and `machines`, as the sidebar
/// describes it.
pub fn loss_sidebar(
    snapshot: &Snapshot,
    graph: &Graph,
    workloads: &Workloads,
    asked: Asked,
    map: &mut ClusterMap,
    machines: &mut [Machine],
) -> LossSidebar {
    let primaries = snapshot.primaries();
    let vital = loss::vital(
        &snapshot.pods,
        &snapshot.services,
        &snapshot.ownership,
        snapshot.alerts_service(),
    );
    let reading = loss::Reading {
        pods: &snapshot.pods,
        workloads,
        ownership: &snapshot.ownership,
        stranding: loss::Stranding {
            storage: &snapshot.storage,
            devices: &snapshot.devices,
            nodes: &snapshot.nodes,
        },
        edges: loss::cascade::Edges {
            graph,
            ingresses: &snapshot.ingresses,
            intents: &snapshot.intents,
        },
        primaries: &primaries,
        vital: &vital,
    };
    let loss = loss::assess(asked.lost, &reading);
    map.mark_loss(&loss);
    let members = machines
        .iter()
        .map(|m| (m.name.as_str(), m.control_plane, m.etcd));
    let majority = Majority::of(members, &loss.machines);
    for machine in machines.iter_mut() {
        machine.lost = loss.machines.contains(&machine.name);
        let really = asked.real.iter().find(|gone| gone.machine == machine.name);
        machine.really_lost = really.is_some();
        machine.lost_minutes = really.and_then(|gone| gone.seconds).map(|s| s / 60);
    }
    LossSidebar::build(&loss)
        .with_gone(asked.real)
        .with_majority(majority)
        .with_unknown(asked.unknown)
        .with_blind(blind(snapshot, &loss.machines))
        .with_alerting(alerting(snapshot))
        .with_plurals(&snapshot.plurals)
        .linked(&loss, |object| snapshot.pages().href(object))
}

/// Why the sidebar cannot state whether the loss takes the alerting
/// path: no `MetricsSource` names the `Service` that Alertmanager runs
/// behind.
fn alerting(snapshot: &Snapshot) -> Option<OffLine> {
    match snapshot.alerts_service() {
        Some(_) => None,
        None => Some(snapshot.resources.directory.off(
            "MetricsSource",
            "transect cannot tell whether this loss stops alerts: ",
            "transect cannot tell whether this loss stops alerts: a MetricsSource's alerts.service would name the Service of Alertmanager.",
        )),
    }
}

/// What `transect` itself stops reading when `lost` is lost.
fn blind(snapshot: &Snapshot, lost: &Lost) -> Option<String> {
    let source = snapshot.resources.log_source.as_ref();
    let log_source = source.and_then(|source| {
        let service = source.service.as_ref()?;
        Some((
            source.name.as_str(),
            service.namespace.as_str(),
            service.name.as_str(),
        ))
    });
    let sources = loss::blind::Sources {
        prometheus: snapshot.prometheus_pod.as_deref(),
        log_source,
    };
    let found = loss::blind::blinded(lost, &sources, &snapshot.pods, &snapshot.services);
    loss::blind::text(&found)
}
