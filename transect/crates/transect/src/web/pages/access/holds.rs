//! What a workload runs as and what it holds: the `ServiceAccount` its
//! pods name, with what its bindings grant, and the claims, `Secret`
//! objects by name, devices, host paths, and machine access its pods'
//! specs give.  A `Secret` shows by its name alone, from the pod spec.

use k8s_openapi::api::core::v1::PodSpec;

use super::look::{Look, and};
use crate::web::model::Model;
use transect_access::access::Account;
use transect_graph::edges::Object;
use transect_map::map::WorkloadContents;
use transect_map::map::section_drawing::{Look as BoxLook, SectionBox};

/// The specs a workload's pods run: the running pods', which name the
/// claims a `StatefulSet` makes, or its templates' when none runs.
fn specs<'a>(object: &Object, contents: &'a WorkloadContents) -> Vec<&'a PodSpec> {
    let workload = contents.workloads.get(object);
    let running = workload.running();
    match running.is_empty() {
        true => workload.templates(),
        false => running.iter().filter_map(|pod| pod.spec.as_ref()).collect(),
    }
}

/// The box of the `ServiceAccount` its pods run as, with one line on
/// what its bindings grant.
pub fn runs_as(
    model: &Model,
    object: &Object,
    contents: &WorkloadContents,
    look: &Look,
) -> SectionBox {
    let namespace = object.namespace.clone().unwrap_or_default();
    let specs = specs(object, contents);
    let mut names: Vec<String> = specs
        .iter()
        .map(|spec| {
            spec.service_account_name
                .clone()
                .unwrap_or_else(|| "default".into())
        })
        .collect();
    names.sort();
    names.dedup();
    let name = names.first().cloned().unwrap_or_else(|| "default".into());
    let account = Account::service_account(&namespace, &name);
    let held = model.access.of(&account);
    let mut granted: Vec<&'static str> = Vec::new();
    let mut by_name = true;
    for (capability, cell) in held.iter().flat_map(|held| &held.cells) {
        if !cell.granted.is_empty() {
            granted.push(capability.words());
            by_name &= cell
                .granted
                .values()
                .flatten()
                .all(|via| !via.names.is_empty());
        }
    }
    let bound = held.map(|held| held.bound.as_slice()).unwrap_or_default();
    let line = match (bound, granted.is_empty()) {
        ([], true) => "no binding names it".to_string(),
        ([one], true) => format!(
            "{} {}: grants none of the six",
            one.role.kind, one.role.name
        ),
        (_, true) => format!("{} bindings, which grant none of the six", bound.len()),
        (bindings, false) => {
            let words: Vec<String> = granted.iter().map(|w| w.to_string()).collect();
            let mut line = format!("may {}", and(&words));
            if by_name {
                line.push_str(", by name");
            }
            if let [one] = bindings {
                line = format!("{} {}: {line}", one.role.kind, one.role.name);
            }
            line
        }
    };
    let mut lines = vec![line];
    if names.len() > 1 {
        lines.push(format!("and {}", and(&names[1..])));
    }
    let object = account
        .object()
        .expect("a ServiceAccount stands for an object");
    let mut boxed = SectionBox::new(format!("ServiceAccount {name}"), lines, BoxLook::Plain)
        .with_href(look.href(&object));
    if let Some(why) = model
        .snapshot
        .intents
        .of(&object)
        .and_then(|said| said.why.clone())
    {
        boxed.lines.insert(0, why.text);
        boxed = boxed.marked(why.provenance);
    }
    boxed
}

/// What its pods hold: a box per claim and device claim, one for the
/// Secrets they mount or read by name, and one each for host
/// paths, the machine's network and processes, and privileged or added
/// capabilities, the first of those with the `host-access` sentence.
pub fn holds(
    model: &Model,
    object: &Object,
    contents: &WorkloadContents,
    look: &Look,
) -> Vec<SectionBox> {
    let namespace = object.namespace.as_deref();
    let specs = specs(object, contents);
    let mut claims: Vec<String> = Vec::new();
    let mut devices: Vec<String> = Vec::new();
    let mut secrets: Vec<String> = Vec::new();
    let mut paths: Vec<String> = Vec::new();
    let (mut network, mut processes) = (false, false);
    let mut privileged: Vec<String> = Vec::new();
    let mut added: Vec<String> = Vec::new();
    for spec in &specs {
        network |= spec.host_network == Some(true);
        processes |= spec.host_pid == Some(true);
        for volume in spec.volumes.iter().flatten() {
            if let Some(claim) = &volume.persistent_volume_claim {
                claims.push(claim.claim_name.clone());
            }
            if let Some(secret) = volume.secret.as_ref().and_then(|s| s.secret_name.clone()) {
                secrets.push(secret);
            }
            let sources = volume
                .projected
                .iter()
                .flat_map(|p| p.sources.iter().flatten());
            secrets.extend(sources.filter_map(|s| Some(s.secret.as_ref()?.name.clone())));
            if let Some(path) = &volume.host_path {
                paths.push(path.path.clone());
            }
        }
        for claim in spec.resource_claims.iter().flatten() {
            devices.push(claim.name.clone());
        }
        let containers = spec
            .containers
            .iter()
            .chain(spec.init_containers.iter().flatten());
        for container in containers {
            for var in container.env.iter().flatten() {
                let key = var
                    .value_from
                    .as_ref()
                    .and_then(|from| from.secret_key_ref.as_ref());
                secrets.extend(key.map(|key| key.name.clone()));
            }
            for from in container.env_from.iter().flatten() {
                secrets.extend(from.secret_ref.as_ref().map(|r| r.name.clone()));
            }
            let context = container.security_context.as_ref();
            if context.and_then(|c| c.privileged) == Some(true) {
                privileged.push(container.name.clone());
            }
            let adds = context.and_then(|c| c.capabilities.as_ref()?.add.clone());
            for capability in adds.into_iter().flatten() {
                added.push(format!("{capability} in {}", container.name));
            }
        }
    }
    for list in [
        &mut claims,
        &mut devices,
        &mut secrets,
        &mut paths,
        &mut privileged,
        &mut added,
    ] {
        list.sort();
        list.dedup();
    }
    let mut boxes: Vec<SectionBox> = Vec::new();
    for claim in claims {
        let object = Object::new("", "PersistentVolumeClaim", namespace, &claim);
        let href = look.href(&object);
        boxes.push(
            SectionBox::new(claim, vec!["PersistentVolumeClaim".into()], BoxLook::Plain)
                .with_href(href),
        );
    }
    for device in devices {
        boxes.push(SectionBox::new(
            device,
            vec!["a device it claims".into()],
            BoxLook::Plain,
        ));
    }
    match secrets.as_slice() {
        [] => {}
        [one] => boxes.push(SectionBox::new(
            format!("Secret {one}"),
            vec!["by name, from its pod spec".into()],
            BoxLook::Plain,
        )),
        many => boxes.push(
            SectionBox::new(
                format!("{} Secrets", many.len()),
                vec![and(many)],
                BoxLook::Plain,
            )
            .with_hint("By name, from its pod spec.".into()),
        ),
    }
    let nodes = nodes(object, contents);
    let machine = match nodes.as_slice() {
        [one] => format!("{one}'s"),
        _ => "its machine's".into(),
    };
    let mut host: Vec<SectionBox> = Vec::new();
    if network {
        let href = match nodes.as_slice() {
            [one] => Some(transect_graph::links::machine(one)),
            _ => None,
        };
        host.push(
            SectionBox::new(format!("{machine} network"), Vec::new(), BoxLook::Machine)
                .with_href(href)
                .with_hint("hostNetwork: true in its pod spec".into()),
        );
    }
    if !paths.is_empty() {
        host.push(SectionBox::new(
            "host paths".into(),
            vec![and(&paths)],
            BoxLook::Machine,
        ));
    }
    if processes {
        host.push(SectionBox::new(
            format!("{machine} processes"),
            vec!["hostPID".into()],
            BoxLook::Machine,
        ));
    }
    if !privileged.is_empty() {
        let title = match privileged.as_slice() {
            [one] => format!("privileged container {one}"),
            many => format!("{} privileged containers", many.len()),
        };
        host.push(SectionBox::new(
            title,
            vec![and(&privileged)],
            BoxLook::Machine,
        ));
    }
    if !added.is_empty() {
        host.push(SectionBox::new(
            "added capabilities".into(),
            vec![and(&added)],
            BoxLook::Machine,
        ));
    }
    let said = model
        .snapshot
        .intents
        .of(object)
        .and_then(|said| said.host_access.clone());
    if let (Some(first), Some(line)) = (host.first_mut(), said) {
        first.lines.insert(0, line.text);
        first.mark = Some(line.provenance);
    }
    boxes.extend(host);
    boxes
}

fn nodes(object: &Object, contents: &WorkloadContents) -> Vec<String> {
    let running = contents.workloads.get(object).running();
    let mut nodes: Vec<String> = running
        .iter()
        .filter_map(|pod| pod.spec.as_ref()?.node_name.clone())
        .collect();
    nodes.sort();
    nodes.dedup();
    nodes
}
