//! The machine page: the `Node`, the pods that run on it, the devices
//! that its `ResourceSlice` objects list, its volumes, its logs, and the
//! Underneath box, which shows the custom resource that describes the
//! computer under the `Node`.

use super::super::model::Model;
use k8s_openapi::jiff::Timestamp;
use transect_graph::edges::{Device, Endpoint, Kind, Object, Provenance, Shown};
use transect_graph::links::Reference;
use transect_ground::machines::Facts;
use transect_map::map::{
    DevicesRead, Driver, MachinePage, MachineReads, OwnLogs, Underneath, VolumeLine, by_driver,
    controller, known_chips, machine_legend, runs_here,
};
use transect_observe::health::Deciding;
use transect_observe::usage;
use transect_resources::knowledge::{KnownThrough, Related};

/// `None` when the cluster has no `Node` called `name`.
pub fn machine_page(
    model: &Model,
    usage: &usage::Reading,
    name: &str,
    now: Timestamp,
) -> Option<MachinePage> {
    let snapshot = &model.snapshot;
    let node = snapshot
        .nodes
        .iter()
        .find(|node| node.metadata.name.as_deref() == Some(name))?;
    let machines = snapshot.machines(usage);
    let switches = transect_map::map::machine_switches(&machines, name);
    let machine = machines.into_iter().find(|machine| machine.name == name)?;
    let layers = &snapshot.resources.layers;
    let layer_of = |namespace: &str| {
        let layer = snapshot.layer_of(namespace, layers);
        layer.unwrap_or_else(|| transect_map::map::UNSORTED.to_string())
    };
    let order: Vec<String> = layers.names().map(str::to_string).collect();
    let reads = MachineReads {
        ownership: &snapshot.ownership,
        plurals: &snapshot.plurals,
        usage: usage.usage(),
        now,
        layer_of: &layer_of,
        order: &order,
    };
    let pods = snapshot.pods.iter().map(AsRef::as_ref);
    let bands = runs_here(pods, name, &reads);
    let tiles = bands
        .iter()
        .flat_map(|band| &band.namespaces)
        .flat_map(|namespace| &namespace.rows)
        .map(|row| &row.tile);
    let legend = machine_legend(tiles);
    let pages = snapshot.pages();
    let href = |object: &Object| pages.href(object);
    let devices = devices(model, name, &href);
    let path = format!("/machines/{name}");
    let page = MachinePage {
        name: name.into(),
        facts: Facts::of(node),
        machine,
        switches,
        legend,
        usage_note: usage
            .off_line(&snapshot.resources.directory)
            .filter(|_| !bands.is_empty()),
        bands,
        drivers: devices,
        devices_note: snapshot
            .devices
            .off
            .map(transect_ground::devices::Off::note),
        volumes: snapshot
            .storage
            .on_machine(name)
            .iter()
            .map(|pin| VolumeLine::of(pin, href))
            .collect(),
        logs: OwnLogs::of(&snapshot.logs.searched, name, separated(model), now)
            .with_unloaded(snapshot.resources.directory.unloaded("LogSource")),
        underneath: underneath(model, name),
        underneath_none: underneath_none(snapshot, name),
        path,
    };
    // The page shows no graph edges, so it has no edge kinds to toggle.
    Some(page)
}

/// The kinds of the edges from a device that the page lists with the
/// device.
const DEVICE_EDGE_KINDS: [Kind; 6] = [
    Kind::RunsOn,
    Kind::StoresOn,
    Kind::Holds,
    Kind::DependsOn,
    Kind::ControlledBy,
    Kind::AuthenticatesThrough,
];

/// The devices that the machine `name` publishes, grouped by driver.
/// Each device lists the devices and objects that its attributes refer
/// to, which device knowledge turns into graph edges.
fn devices(model: &Model, name: &str, href: &dyn Fn(&Object) -> Option<String>) -> Vec<Driver> {
    let snapshot = &model.snapshot;
    let knowledge = &snapshot.resources.devices;
    let attributes = |device: &Device| snapshot.devices.attributes(device);
    let graph = model.graph(&Shown::default());
    let names = |device: &Device| -> Vec<(String, Reference)> {
        let from = Endpoint::Device(device.clone());
        let edges = DEVICE_EDGE_KINDS
            .iter()
            .flat_map(|kind| graph.edges_leaving(*kind, &from));
        edges
            .filter_map(|edge| {
                let Provenance::Devices { label, .. } = &edge.provenance else {
                    return None;
                };
                let named = match &edge.to {
                    Endpoint::Device(far) => Reference {
                        text: snapshot.devices.title(far, knowledge),
                        href: transect_graph::links::device(far),
                    },
                    Endpoint::Object(object) => Reference {
                        text: transect_graph::edges::kind_and_name(&object.kind, &object.name),
                        href: href(object),
                    },
                    _ => return None,
                };
                Some((label.clone(), named))
            })
            .collect()
    };
    let read = DevicesRead {
        attributes: &attributes,
        knowledge,
        referenced: &names,
        href,
    };
    by_driver(snapshot.devices.published_on(name), &read)
}

/// Whether the log source has a field that says which machine a log
/// line came from.
fn separated(model: &Model) -> bool {
    let source = model.snapshot.resources.log_source.as_ref();
    source.is_some_and(|source| source.fields.machine.is_some())
}

/// The reason the Underneath box is empty. It is `None` when an object
/// describes the machine, and when no kind's knowledge sets `node`,
/// because then no kind is expected to describe the machine.
fn underneath_none(snapshot: &crate::web::snapshot::Snapshot, name: &str) -> Option<String> {
    if snapshot.underneath(name).is_some() {
        return None;
    }
    let mut kinds: Vec<&str> = snapshot
        .describing_kinds()
        .map(|(_, kind)| kind.as_str())
        .collect();
    kinds.sort();
    match kinds.as_slice() {
        [] => None,
        [one] => Some(format!("no {one} names this Node")),
        [rest @ .., last] => Some(format!("no {} or {last} names this Node", rest.join(", "))),
    }
}

/// The Underneath box: the object that describes the machine `name`,
/// with its conditions and the fields that its kind's knowledge reads.
fn underneath(model: &Model, name: &str) -> Option<Underneath> {
    let snapshot = &model.snapshot;
    let (object, found) = snapshot.underneath(name)?;
    let knowledge = snapshot.kinds.of_object(&object)?;
    let graph = model.graph(&Shown::default());
    let relations = snapshot.relations.get(&object).into_iter().flatten();
    let related = relations.map(|relation| {
        Related::of(relation, None, &snapshot.plurals, |other| {
            let namespace = other.namespace.as_deref().unwrap_or_default();
            snapshot.ownership.workloads(namespace).any(|w| w == other)
        })
    });
    let controller = controller(&graph, &object).map(|controller| Reference {
        href: snapshot.pages().href(&controller),
        text: controller.name,
    });
    let known = KnownThrough::of(knowledge, controller).with_related(related.collect());
    Some(Underneath {
        title: transect_graph::edges::kind_and_name(&object.kind, &object.name),
        href: snapshot.pages().href(&object),
        chips: known_chips(&found.data, &Deciding::of(&snapshot.kinds, &object)),
        known: Some(known),
    })
}
