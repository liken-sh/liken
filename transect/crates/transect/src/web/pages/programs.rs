//! The programs that a workload runs, for the rows of its page that say
//! what each program is for and which objects call it.

use super::super::model::Model;
use super::object::refer;
use transect_graph::edges::{Endpoint, Kind, Object};
use transect_graph::links::Reference;
use transect_map::map::ProgramRows;

/// The rows of each program whose image `workload`'s pods run, with the
/// holders of the templates that present a token to it.
pub fn programs(model: &Model, workload: &Object) -> Vec<ProgramRows> {
    let snapshot = &model.snapshot;
    let graph = model.graph();
    let runners = &snapshot.runners;
    let Some(runner) = runners.iter().find(|runner| &runner.workload == workload) else {
        return Vec::new();
    };
    let end = Endpoint::Object(workload.clone());
    let reaching: Vec<_> = graph
        .edges_reaching(Kind::Presents, &end)
        .cloned()
        .collect();
    let here = workload.namespace.as_deref();
    let running = snapshot.programs.iter();
    let running = running.filter(|program| runner.images.iter().any(|image| program.runs(image)));
    let presented = &snapshot.presented;
    let merged = |caller: &Object| {
        let templates = presented.iter().filter(|t| &t.holder == caller);
        templates.flat_map(|t| t.merges.first()).next().cloned()
    };
    let named = |object: &Object| refer(snapshot, &Endpoint::Object(object.clone()), here);
    let short = |object: &Object| Reference {
        text: object.name.clone(),
        href: snapshot.pages().href(object),
    };
    let plural = |object: &Object| {
        let key = (object.group.clone(), object.kind.clone());
        let plural = snapshot.plurals.get(&key).map(String::as_str);
        transect_resources::knowledge::plural_label(&object.kind, plural)
    };
    let rows =
        running.map(|program| ProgramRows::build(program, &reaching, merged, named, short, plural));
    rows.collect()
}
