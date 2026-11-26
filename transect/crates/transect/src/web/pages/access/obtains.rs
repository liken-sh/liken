//! What a `ServiceAccount` can obtain besides its RBAC: the object that
//! owns it, the pod templates that run as it, the tokens that those
//! templates present and the programs that accept them, and the objects
//! that grant it something from a program.  Each section is present
//! only when its knowledge is loaded, and none of them judges a grant.
//! The page puts them above the drawing of the account's RBAC, so a
//! reader sees what a hook presents beside what its account may do.

use serde_json::Value;

use super::look::Look;
use crate::web::model::Model;
use crate::web::snapshot::Snapshot;
use transect_access::access::drawing::account_page::Obtained;
use transect_access::access::presents::Presented;
use transect_graph::edges::{Object, kind_and_name};
use transect_graph::links::Part;
use transect_resources::knowledge::Names;

/// The sections of the `ServiceAccount` `namespace/name`, in the order
/// the page shows them.
pub fn obtains(model: &Model, namespace: &str, name: &str, look: &Look) -> Vec<Obtained> {
    let snapshot = &model.snapshot;
    let account = Object::new("", "ServiceAccount", Some(namespace), name);
    let owner = snapshot.ownership.owner(&account);
    let link = |object: &Object| Part {
        text: kind_and_name(&object.kind, &object.name),
        href: look.href(object),
    };
    let mut sections = Vec::new();
    if let Some(owner) = &owner {
        let line = vec![link(owner), Part::plain(" owns it.")];
        sections.push(Obtained::new("Owned by", vec![line]));
    }
    let runs: Vec<&Presented> = snapshot
        .presented
        .iter()
        .filter(|template| names(template, &account))
        .collect();
    let lines = runs.iter().map(|template| {
        let used = template.used.as_deref().unwrap_or("pod");
        let at = format!(" from {}.", template.path);
        vec![
            Part::plain(&format!("The {used} pods of ")),
            link(&template.holder),
            Part::plain(&at),
        ]
    });
    let lines: Vec<Vec<Part>> = lines.collect();
    if !lines.is_empty() {
        sections.push(Obtained::new("Runs as it", lines));
    }
    let may: Vec<&Presented> = match &owner {
        Some(owner) => snapshot
            .presented
            .iter()
            .filter(|template| may_run_as(snapshot, template, owner))
            .collect(),
        None => Vec::new(),
    };
    let lines = may_lines(&may, &link);
    if !lines.is_empty() {
        sections.push(Obtained::new("May run as it", lines));
    }
    let audiences = |templates: &[&Presented]| {
        let mut audiences: Vec<String> = templates
            .iter()
            .flat_map(|t| t.audiences.iter().map(|a| a.name.clone()))
            .collect();
        audiences.sort();
        audiences.dedup();
        audiences
    };
    let sure = audiences(&runs);
    let lines = sure.iter().map(|audience| presents(model, audience, look));
    let lines: Vec<Vec<Part>> = lines.collect();
    if !lines.is_empty() {
        sections.push(Obtained::new("Presents", lines));
    }
    let maybe = audiences(&may).into_iter().filter(|a| !sure.contains(a));
    let lines = maybe.map(|audience| presents(model, &audience, look));
    let lines: Vec<Vec<Part>> = lines.collect();
    if !lines.is_empty() {
        sections.push(Obtained::new("May present", lines));
    }
    let granted = granted_by(model, &account, look);
    if !granted.is_empty() {
        sections.push(Obtained::new("Granted by", granted));
    }
    sections
}

/// Whether the knowledge of `template` names `account` as the one that
/// its pods run as.
fn names(template: &Presented, account: &Object) -> bool {
    let runs_as = template.runs_as.as_deref();
    template.holder.namespace == account.namespace && runs_as == Some(account.name.as_str())
}

/// Whether the pods of `template`, whose knowledge names no account, may
/// run as an account that `owner` owns.  An operator gives the pods of
/// such a template an account of their holder's, or of the object whose
/// template the holder merges, so a `Duty`'s hooks may run as an account
/// of the `Duty` or of its `Mandate`.  The operator records the choice
/// nowhere, so the page says "may".
fn may_run_as(snapshot: &Snapshot, template: &Presented, owner: &Object) -> bool {
    if template.runs_as.is_some() {
        return false;
    }
    let merges_owner = snapshot
        .presented
        .iter()
        .filter(|other| other.holder == template.holder)
        .any(|other| other.merges.contains(owner));
    &template.holder == owner || merges_owner
}

/// One line for each field of the templates in `may`, with the holders
/// that have it, so the `Duty`s of a `Mandate` share a line.
fn may_lines(may: &[&Presented], link: &dyn Fn(&Object) -> Part) -> Vec<Vec<Part>> {
    let mut fields: Vec<(&str, &str)> = may
        .iter()
        .map(|t| (t.used.as_deref().unwrap_or("pod"), t.path.as_str()))
        .collect();
    fields.sort();
    fields.dedup();
    let line = |(used, path): (&str, &str)| {
        let mut line = vec![Part::plain(&format!("The {used} pods from {path} of "))];
        let mut holders: Vec<&&Presented> = may
            .iter()
            .filter(|t| t.used.as_deref().unwrap_or("pod") == used && t.path == path)
            .collect();
        holders.sort_by(|a, b| a.holder.cmp(&b.holder));
        for (at, template) in holders.into_iter().enumerate() {
            if at > 0 {
                line.push(Part::plain(", "));
            }
            line.push(link(&template.holder));
        }
        line.push(Part::plain("."));
        line
    };
    fields.into_iter().map(line).collect()
}

/// One line for `audience`: each program that accepts it, with its verb
/// and the workloads that run it, or that no knowledge is loaded for it.
fn presents(model: &Model, audience: &str, look: &Look) -> Vec<Part> {
    let snapshot = &model.snapshot;
    let mut line = vec![Part::plain(&format!("{audience}: "))];
    let accepting: Vec<_> = snapshot.programs.accepting(audience).collect();
    if accepting.is_empty() {
        line.push(Part::plain("no knowledge is loaded for this audience."));
        return line;
    }
    let runners = &snapshot.runners;
    for (at, (program, accepts)) in accepting.into_iter().enumerate() {
        if at > 0 {
            line.push(Part::plain("; "));
        }
        line.push(Part::plain(&format!(
            "{}, to {}",
            accepts.verb, program.name
        )));
        let running = runners
            .iter()
            .filter(|r| r.images.iter().any(|i| program.runs(i)));
        for (at, runner) in running.enumerate() {
            line.push(Part::plain(if at == 0 { ", at " } else { " and " }));
            let workload = &runner.workload;
            let text = format!(
                "{}/{}",
                workload.namespace.as_deref().unwrap_or_default(),
                workload.name
            );
            line.push(Part {
                text,
                href: look.href(workload),
            });
        }
    }
    line.push(Part::plain("."));
    line
}

/// One line for each object whose field, as a program's `grants` names
/// it, names `account`, with the status of its kind's health condition.
fn granted_by(model: &Model, account: &Object, look: &Look) -> Vec<Vec<Part>> {
    let snapshot = &model.snapshot;
    let mut lines = Vec::new();
    for program in snapshot.programs.iter() {
        for accepts in &program.accepts {
            let Some(grants) = &accepts.grants else {
                continue;
            };
            let kind = (&grants.kind.group, &grants.kind.kind);
            let objects = snapshot.relations.iter().filter(|(object, relations)| {
                (&object.group, &object.kind) == kind
                    && relations.iter().any(|relation| {
                        relation.path == grants.path
                            && relation.names == Names::Object(account.clone())
                    })
            });
            for (object, _) in objects {
                let mut line = vec![
                    Part {
                        text: kind_and_name(&object.kind, &object.name),
                        href: look.href(object),
                    },
                    Part::plain(&format!(" names it for {}", program.name)),
                ];
                if let Some(health) = health(model, object) {
                    line.push(Part::plain(&format!(", and its {health}")));
                }
                line.push(Part::plain("."));
                lines.push(line);
            }
        }
    }
    lines
}

/// The health condition of `object` as its kind's knowledge names it,
/// such as "Ready is True".
fn health(model: &Model, object: &Object) -> Option<String> {
    let snapshot = &model.snapshot;
    let condition = snapshot.kinds.of_object(object)?.condition()?;
    let (_, objects) = snapshot.custom_kind(&object.group, &object.kind)?;
    let found = objects.iter().find(|found| {
        found.metadata.name.as_deref() == Some(&object.name)
            && found.metadata.namespace == object.namespace
    })?;
    let conditions = found.data.pointer("/status/conditions")?.as_array()?;
    let status = conditions
        .iter()
        .find(|c| c.get("type").and_then(Value::as_str) == Some(condition))
        .and_then(|c| c.get("status")?.as_str());
    Some(format!("{condition} is {}", status.unwrap_or("Unknown")))
}
