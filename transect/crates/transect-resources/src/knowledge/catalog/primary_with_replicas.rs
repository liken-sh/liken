//! The code of the pattern `primary-with-replicas`, whose roles and
//! elevation are in `primary-with-replicas.yaml`: a store with one
//! primary that takes the writes, and replicas that stream from it.
//! Most stores have one instance, so each line that is about replicas
//! does not apply to a store with none, and the parts that serve the
//! replicas are drawn at rest there, not as faults.

use super::{Derive, Derived, Line, Marks, Module, Occurrence, Said};
use transect_graph::edges::Object;
use transect_graph::links::{Piece, listed, text};

pub const MODULE: Module = Module {
    file: include_str!("primary-with-replicas.yaml"),
    derive,
    expect,
};

/// Each source line names who states the line after the fields it read.
const STATED: &str = "as transect's pattern expects";

fn derive(name: &str) -> Option<Derive> {
    match name {
        "instances-but-primary" => Some(instances_but_primary),
        "controller" => Some(controller),
        _ => None,
    }
}

/// The replicas: each instance that `primary` does not name. With none,
/// the reason names the list of instances, so the reader sees which
/// field says that the store has one instance.
fn instances_but_primary(store: &Occurrence) -> Derived {
    let instances = store.filled("instances").unwrap_or_default();
    let primary = store.filled("primary").unwrap_or_default();
    let replicas: Vec<Object> = instances
        .iter()
        .filter(|instance| !primary.contains(instance))
        .cloned()
        .collect();
    if !replicas.is_empty() {
        return Derived::Parts(replicas);
    }
    let path = store.field("instances").unwrap_or_default();
    match instances.is_empty() {
        true => Derived::Nothing(format!("{path} names no instance")),
        false => Derived::Nothing(format!("{path} lists only the primary")),
    }
}

/// The promoter: the object that the store is controlled by, which
/// moves the primary to a replica. When the knowledge names the image of
/// the kind's controller and nothing that runs it controls the store,
/// the promoter is missing. With no image named, `transect` cannot look
/// for one, so the place is at rest.
fn controller(store: &Occurrence) -> Derived {
    let found = store.cluster.controller(store.object);
    match (found, store.knowledge.controller_image.as_deref()) {
        (Some(found), _) => Derived::Parts(vec![found]),
        (None, Some(image)) => Derived::Missing(format!(
            "nothing that runs {image} controls {} {}",
            store.object.kind, store.object.name
        )),
        (None, None) => Derived::Nothing(format!(
            "the KindKnowledge names no controller of {}",
            store.object.kind
        )),
    }
}

fn expect(store: &Occurrence) -> (Vec<Line>, Marks) {
    let replicas = match store.derived.of("replicas") {
        Some(Derived::Parts(replicas)) => Some(replicas.as_slice()),
        _ => None,
    };
    let primary = store.filled("primary").unwrap_or_default();
    let primary = primary.first();
    let mut marks = Marks::default();
    let lines = vec![
        primary_line(store, primary, &mut marks),
        writes_line(store, primary, &mut marks),
        replicas_line(store, replicas, &mut marks),
        reads_line(store, replicas, &mut marks),
    ];
    if replicas.is_none() {
        let read = store.filled("read-service").unwrap_or_default();
        for service in &read {
            marks.rests(service, "selects the replicas, and there is none");
        }
        if let Some(Derived::Parts(promoters)) = store.derived.of("promoter") {
            for promoter in promoters {
                marks.rests(promoter, "no replica to promote");
            }
        }
    }
    (lines, marks)
}

/// A line that is unmet, with the words that mark the part it names.
struct Unmet {
    detail: Vec<Piece>,
    part: Object,
    why: &'static str,
}

impl Unmet {
    fn new(part: &Object, why: &'static str, detail: Vec<Piece>) -> Self {
        Self {
            detail,
            part: part.clone(),
            why,
        }
    }

    /// The line, with its part marked.
    fn line(self, make: impl Fn(Said, Vec<Piece>) -> Line, marks: &mut Marks) -> Line {
        marks.unmet(&self.part, self.why);
        make(Said::Unmet, self.detail)
    }
}

/// The primary is ready, and it is the operator's target.
fn primary_line(store: &Occurrence, primary: Option<&Object>, marks: &mut Marks) -> Line {
    let fields = [
        store.field("primary"),
        store.field("instances"),
        store.field("target"),
    ];
    let make = |said, detail| Line {
        expects: "the primary is ready, and it is the operator's target".into(),
        said,
        detail,
        source: source(&fields),
    };
    let Some(primary) = primary else {
        let path = store.field("primary").unwrap_or_default();
        return make(Said::NoSignal, words(format!("{path} names nothing now")));
    };
    let instances = store.filled("instances").unwrap_or_default();
    let target = store.filled("target");
    let unmet = match store.cluster.ready(primary) {
        _ if !instances.contains(primary) => Some(Unmet::new(
            primary,
            "not one of the instances",
            vec![name(primary), Piece::words(" is not one of the instances")],
        )),
        None => Some(Unmet::new(
            primary,
            "no such Pod",
            vec![Piece::words("the cluster has no Pod "), name(primary)],
        )),
        Some(false) => Some(Unmet::new(
            primary,
            "not ready",
            vec![name(primary), Piece::words(" is not ready")],
        )),
        Some(true) => match target.as_deref() {
            Some([target, ..]) if target != primary => Some(Unmet::new(
                primary,
                "not the operator's target",
                vec![
                    Piece::words("the primary is "),
                    name(primary),
                    Piece::words(", and the operator's target is "),
                    name(target),
                ],
            )),
            _ => None,
        },
    };
    if let Some(unmet) = unmet {
        return unmet.line(make, marks);
    }
    match target {
        None => make(
            Said::Met,
            vec![
                name(primary),
                Piece::words(", and the KindKnowledge names no target"),
            ],
        ),
        Some(_) => make(Said::Met, vec![name(primary)]),
    }
}

/// Writes reach only the primary.
fn writes_line(store: &Occurrence, primary: Option<&Object>, marks: &mut Marks) -> Line {
    let fields = [store.field("write-service"), store.field("primary")];
    let make = |said, detail| Line {
        expects: "writes reach only the primary".into(),
        said,
        detail,
        source: source(&fields),
    };
    let service = match served(store, "write-service", "writes") {
        Ok(service) => service,
        Err(missing) => return make(Said::NoSignal, words(missing)),
    };
    let reached = match reached(store, &service) {
        Ok(reached) => reached,
        Err(unmet) => return (*unmet).line(make, marks),
    };
    let others: Vec<&Object> = reached.iter().filter(|pod| Some(*pod) != primary).collect();
    let unmet = match primary {
        Some(primary) if !reached.contains(primary) => Some(Unmet::new(
            &service,
            "does not reach the primary",
            vec![
                name(&service),
                Piece::words(" does not reach the primary "),
                name(primary),
            ],
        )),
        _ if !others.is_empty() => Some(Unmet::new(
            &service,
            "reaches more than the primary",
            also_reaches(&service, &others),
        )),
        _ => None,
    };
    match unmet {
        Some(unmet) => unmet.line(make, marks),
        None => make(Said::Met, reaches(&service, &reached)),
    }
}

/// Each replica is ready.
fn replicas_line(store: &Occurrence, replicas: Option<&[Object]>, marks: &mut Marks) -> Line {
    let fields = [store.field("instances"), store.field("primary")];
    let make = |said, detail| Line {
        expects: "each replica is ready".into(),
        said,
        detail,
        source: source(&fields),
    };
    let Some(replicas) = replicas else {
        return make(Said::Moot, words("does not apply, no replica".into()));
    };
    let unready: Vec<&Object> = replicas
        .iter()
        .filter(|replica| store.cluster.ready(replica) != Some(true))
        .collect();
    let count = format!("{} of {}", replicas.len() - unready.len(), replicas.len());
    if unready.is_empty() {
        return make(Said::Met, words(count));
    }
    for replica in &unready {
        marks.unmet(replica, "not ready");
    }
    let verb = match unready.len() {
        1 => " is not ready",
        _ => " are not ready",
    };
    let mut detail = vec![Piece::words(format!("{count}, and "))];
    detail.extend(names(&unready));
    detail.push(Piece::words(verb));
    make(Said::Unmet, detail)
}

/// Reads reach only replicas, and at least one.
fn reads_line(store: &Occurrence, replicas: Option<&[Object]>, marks: &mut Marks) -> Line {
    let fields = [store.field("read-service"), store.field("instances")];
    let make = |said, detail| Line {
        expects: "reads reach only replicas".into(),
        said,
        detail,
        source: source(&fields),
    };
    let Some(replicas) = replicas else {
        return make(Said::Moot, words("does not apply, no replica".into()));
    };
    let service = match served(store, "read-service", "reads") {
        Ok(service) => service,
        Err(missing) => return make(Said::NoSignal, words(missing)),
    };
    let reached = match reached(store, &service) {
        Ok(reached) => reached,
        Err(unmet) => return (*unmet).line(make, marks),
    };
    let others: Vec<&Object> = reached
        .iter()
        .filter(|pod| !replicas.contains(pod))
        .collect();
    if !others.is_empty() {
        let unmet = Unmet::new(
            &service,
            "reaches more than the replicas",
            also_reaches(&service, &others),
        );
        return unmet.line(make, marks);
    }
    make(Said::Met, reaches(&service, &reached))
}

/// The `Service` that fills `role`, or what is missing: the kind fills
/// no such role, or its reference names nothing now.
fn served(store: &Occurrence, role: &str, carries: &str) -> Result<Object, String> {
    let Some(filled) = store.filled(role) else {
        return Err(format!("the KindKnowledge names no Service for {carries}"));
    };
    let path = store.field(role).unwrap_or_default();
    filled
        .into_iter()
        .next()
        .ok_or_else(|| format!("{path} names nothing now"))
}

/// The pods that `service` reaches, or the unmet line when it reaches
/// none, boxed because it is larger than the list.
fn reached(store: &Occurrence, service: &Object) -> Result<Vec<Object>, Box<Unmet>> {
    match store.cluster.endpoints(service) {
        None => Err(Box::new(Unmet::new(
            service,
            "no such Service",
            vec![Piece::words("the cluster has no Service "), name(service)],
        ))),
        Some(reached) if reached.is_empty() => Err(Box::new(Unmet::new(
            service,
            "no ready endpoint",
            vec![name(service), Piece::words(" has no ready endpoint")],
        ))),
        Some(reached) => Ok(reached),
    }
}

/// "<service> reaches a and b".
fn reaches(service: &Object, reached: &[Object]) -> Vec<Piece> {
    let reached: Vec<&Object> = reached.iter().collect();
    let mut detail = vec![name(service), Piece::words(" reaches ")];
    detail.extend(names(&reached));
    detail
}

/// "<service> also reaches a and b".
fn also_reaches(service: &Object, others: &[&Object]) -> Vec<Piece> {
    let mut detail = vec![name(service), Piece::words(" also reaches ")];
    detail.extend(names(others));
    detail
}

fn name(object: &Object) -> Piece {
    Piece::Name(object.clone(), object.name.clone())
}

fn words(text: String) -> Vec<Piece> {
    vec![Piece::Words(text)]
}

/// The names of `objects` as a list in a sentence, each an object that
/// the page links.
fn names(objects: &[&Object]) -> Vec<Piece> {
    listed(objects.iter().map(|object| vec![name(object)]).collect())
}

/// "from status.a and status.b, as transect's catalog expects", with
/// each field that the kind fills.
fn source(fields: &[Option<String>]) -> String {
    let read = fields
        .iter()
        .flatten()
        .map(|field| vec![Piece::words(field.as_str())]);
    format!("from {}, {STATED}", text(&listed(read.collect())))
}

#[cfg(test)]
mod tests;
