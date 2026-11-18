//! The loss view of the cluster page, which shows what happens when a
//! set of machines is lost. It marks the result on the map and the
//! machines band, and describes it in the sidebar.

use crate::web::snapshot::Snapshot;
use transect_cluster::workloads::Workloads;
use transect_graph::edges::Graph;
use transect_ground::loss::{self, Lost, Majority, NotReadyMachine, Sidebar as LossSidebar};
use transect_ground::machines::Machine;
use transect_map::map::ClusterMap;
use transect_resources::resources::OffLine;

/// A request to assess a loss: the machines to treat as lost, the
/// machines that are actually gone, and the text to show for names that
/// match no machine.
pub struct LossRequest<'a> {
    pub lost: Lost,
    pub real: &'a [NotReadyMachine],
    pub unknown: Option<String>,
}

/// Assesses the loss in `request`, marks it on `map` and `machines`, and
/// returns the sidebar that describes it.
pub fn loss_sidebar(
    snapshot: &Snapshot,
    graph: &Graph,
    workloads: &Workloads,
    request: LossRequest,
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
    let loss = loss::assess(request.lost, &reading);
    map.mark_loss(&loss);
    let members = machines
        .iter()
        .map(|m| (m.name.as_str(), m.control_plane, m.etcd));
    let majority = Majority::of(members, &loss.machines);
    for machine in machines.iter_mut() {
        machine.lost = loss.machines.contains(&machine.name);
        let really = request
            .real
            .iter()
            .find(|gone| gone.machine == machine.name);
        machine.really_lost = really.is_some();
        machine.lost_minutes = really.and_then(|gone| gone.seconds).map(|s| s / 60);
    }
    LossSidebar::build(&loss)
        .with_gone(request.real)
        .with_majority(majority)
        .with_unknown(request.unknown)
        .with_blind(blind(snapshot, &loss.machines))
        .with_alerting(alerting(snapshot))
        .with_plurals(&snapshot.plurals)
        .linked(&loss, |object| snapshot.pages().href(object))
}

/// The sidebar's notice that `transect` cannot tell whether the loss
/// stops alerts, or `None` when a `MetricsSource` sets `alerts.service`
/// to the `Service` in front of Alertmanager.
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

/// Describes the data sources that `transect` cannot read when the
/// machines in `lost` are lost, or `None` when it can still read all.
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
