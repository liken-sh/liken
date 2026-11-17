//! A `StorageClass`'s provisioner and volumes, a CSI driver's classes
//! and where its data is, and a `ResourceClaim`'s device, machine, and
//! pod.

use kube::ResourceExt;
use serde_json::Value;

use super::super::super::snapshot::Snapshot;
use super::refer_object;
use transect_graph::edges::Object;
use transect_ground::storage::Backend;
use transect_map::objects::{Section, Side, words};

pub fn class(snapshot: &Snapshot, object: &Object) -> Vec<Section> {
    let Some(class) = snapshot
        .paged
        .storage_classes
        .iter()
        .find(|class| class.name_any() == object.name)
    else {
        return Vec::new();
    };
    let driver = Object::new("storage.k8s.io", "CSIDriver", None, &class.provisioner);
    let provisioner = match snapshot.pages().has(&driver) {
        true => refer_object(snapshot, &driver, None),
        false => words(&class.provisioner),
    };
    let volumes = snapshot.storage.volumes_of_class(&object.name);
    let mut backends: Vec<String> = volumes
        .iter()
        .map(|volume| backend(&volume.backend))
        .collect();
    backends.sort();
    backends.dedup();
    vec![
        Section::new("StorageClass", Side::Own)
            .fact("provisioner", vec![provisioner])
            .text("volumes", Some(volumes.len().to_string()))
            .fact(
                "their data is on",
                backends.into_iter().map(words).collect(),
            ),
    ]
}

/// Where a backend keeps its data, in words.
fn backend(backend: &Backend) -> String {
    match backend {
        Backend::Address(address) => address.clone(),
        Backend::Driver(driver) => driver.clone(),
        Backend::Machine(Some(machine)) => format!("the disk of {machine}"),
        Backend::Machine(None) => "the disk of the machine that mounts it".into(),
    }
}

pub fn driver(snapshot: &Snapshot, object: &Object) -> Vec<Section> {
    let classes = snapshot.paged.storage_classes.iter();
    let classes = classes.filter(|class| class.provisioner == object.name);
    let mut names: Vec<String> = classes.map(|class| class.name_any()).collect();
    // The store's order changes between two reads of it.
    names.sort();
    let classes = names.iter().map(|name| {
        let class = Object::new("storage.k8s.io", "StorageClass", None, name);
        refer_object(snapshot, &class, None)
    });
    let data_on = snapshot
        .intents
        .of(object)
        .and_then(|said| said.data_on.as_ref());
    let console = snapshot
        .intents
        .of(object)
        .and_then(|said| said.console.as_ref());
    let urls = snapshot.consoles().remove(&object.name).unwrap_or_default();
    let ground = snapshot
        .outsides
        .titled(&object.name)
        .map(|_| words(&object.name));
    vec![
        Section::new("CSI driver", Side::Own)
            .text("keeps its data on", data_on.map(|line| line.text.clone()))
            .external("console", urls)
            .template("console template", console.map(|line| line.text.clone()))
            .fact("StorageClasses", classes.collect())
            .fact("ground box", ground.into_iter().collect()),
    ]
}

pub fn claim(snapshot: &Snapshot, object: &Object, data: &Value) -> Vec<Section> {
    let devices = snapshot.devices.of_claim(object).unwrap_or_default();
    let held = devices.iter().map(|device| words(device.short()));
    let machines = devices
        .iter()
        .filter_map(|device| device.machine.as_ref())
        .map(|machine| refer_object(snapshot, &Object::new("", "Node", None, machine), None));
    let reserved = data
        .pointer("/status/reservedFor")
        .and_then(Value::as_array);
    let pods = reserved.into_iter().flatten().filter_map(|reserved| {
        (reserved.get("resource")?.as_str()? == "pods").then_some(())?;
        let name = reserved.get("name")?.as_str()?;
        let pod = Object::new("", "Pod", object.namespace.as_deref(), name);
        Some(refer_object(snapshot, &pod, object.namespace.as_deref()))
    });
    vec![
        Section::new("Claim", Side::Out)
            .fact("holds", held.collect())
            .fact("on", machines.collect())
            .fact("for", pods.collect())
            .or("It holds no device now."),
    ]
}

#[cfg(test)]
mod tests {
    use std::sync::Arc;

    use k8s_openapi::api::storage::v1::StorageClass;
    use rstest::rstest;

    use super::*;

    fn class(name: &str, provisioner: &str) -> Arc<StorageClass> {
        Arc::new(
            serde_yaml::from_str(&format!(
                "{{metadata: {{name: {name}}}, provisioner: {provisioner}}}"
            ))
            .unwrap(),
        )
    }

    #[rstest]
    fn lists_a_drivers_storage_classes_by_name() {
        let mut snapshot = Snapshot::empty();
        snapshot.paged.storage_classes = vec![
            class("zeta", "csi.example.test"),
            class("other", "csi.elsewhere.test"),
            class("alpha", "csi.example.test"),
        ];
        let driver = Object::new("storage.k8s.io", "CSIDriver", None, "csi.example.test");
        let sections = super::driver(&snapshot, &driver);
        let fact = sections[0]
            .facts
            .iter()
            .find(|f| f.label == "StorageClasses");
        let names: Vec<&str> = fact
            .unwrap()
            .values
            .iter()
            .map(|v| v.text.as_str())
            .collect();
        assert_eq!(names, vec!["StorageClass alpha", "StorageClass zeta"]);
    }
}
