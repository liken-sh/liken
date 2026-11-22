//! The page of a custom resource whose kind has a `KindKnowledge`. It
//! reads the resource the way a workload's page does, so the two pages
//! show the same card.

mod core;
mod judged;
mod readings;

use super::super::model::Model;
use super::super::snapshot::Snapshot;
use super::WorkloadAt;
use super::object::card;
use super::read_workloads;
use k8s_openapi::jiff::Timestamp;
use transect_graph::edges::Graph;
use transect_graph::edges::Object;
use transect_map::map::{
    Board, KnownEntry, KnownPage, Standing, Subject, Why, every_chip, has_page, known_chips,
    known_through, workload_card,
};
use transect_map::objects::{self, Center, ObjectCard};
use transect_network::entry::Endpoints;
use transect_observe::health::{Deciding, Level, Reason};
use transect_observe::usage;
use transect_resources::knowledge::role::NamedBy;
use transect_resources::knowledge::{catalog, plural_label};
use transect_resources::layers::Layers;

/// The page for the custom resource `namespace/name` whose kind is
/// served under the URL segment `resource`, drawing the elevation whose
/// slug is `elevation`, or the first when it is none. It is `None` when
/// no watched resource of a kind with a `KindKnowledge` matches.
pub fn known_page(
    model: &Model,
    layers: &Layers,
    usage: &usage::Reading,
    at: WorkloadAt,
    elevation: Option<&str>,
    now: Timestamp,
) -> Option<KnownPage> {
    let WorkloadAt {
        namespace,
        resource,
        name,
    } = at;
    let snapshot = &model.snapshot;
    let plurals = &snapshot.plurals;
    let (object, found) = snapshot.custom.iter().find_map(|(api, objects)| {
        snapshot.kinds.of(&api.group, &api.kind)?;
        let found = objects.iter().find(|found| {
            let meta = &found.metadata;
            meta.namespace.as_deref() == Some(namespace) && meta.name.as_deref() == Some(name)
        })?;
        let object = Object::new(&api.group, &api.kind, Some(namespace), name);
        let served = transect_graph::links::kinds::workload(&object, plurals);
        (served.as_deref() == Some(resource)).then_some((object, found))
    })?;
    let knowledge = snapshot.kinds.of_object(&object)?;
    // A kind with no elevation of its own or of its pattern has no URL
    // for one, so the page of an elevation of its object is not found.
    if elevation.is_some() && knowledge.stated().is_empty() {
        return None;
    }
    let page = read_workloads(
        model,
        layers,
        usage,
        namespace,
        |map, contents| {
            let layer = map.layer_of(namespace)?.to_owned();
            let path = transect_graph::links::page(&object, plurals)?;
            let workload = contents.owners.contains(&&object);
            let level = match workload {
                true => contents.by_workload.get(&object).map(|health| health.level),
                false => {
                    transect_observe::health::condition(&snapshot.kinds, snapshot, &object, found)
                        .map(|r| r.level())
                }
            };
            let siblings = snapshot.custom_objects().into_iter().filter(|other| {
                (&other.group, &other.kind, other.namespace.as_deref())
                    == (&object.group, &object.kind, Some(namespace))
            });
            let mut column: Vec<KnownEntry> = siblings
                .map(|other| KnownEntry {
                    text: transect_graph::edges::kind_and_name(&other.kind, &other.name),
                    href: transect_graph::links::page(&other, plurals),
                    current: other == object,
                })
                .collect();
            column.sort_by(|a, b| a.text.cmp(&b.text));
            let own = plural_label(
                &object.kind,
                plurals
                    .get(&(object.group.clone(), object.kind.clone()))
                    .map(String::as_str),
            );
            let counts = snapshot
                .counts(namespace)
                .into_iter()
                .filter(|c| c.label != own);
            let page_of = |other: &Object| {
                has_page(other, &contents)
                    .then(|| transect_graph::links::page(other, plurals))
                    .flatten()
            };
            let subjects = |other: &Object| subject(snapshot, contents.graph, other);
            let endpoints = Endpoints::build(&snapshot.endpoint_slices);
            let facts = judged::Facts {
                snapshot,
                graph: contents.graph,
                endpoints: &endpoints,
            };
            let judged = catalog::judge(knowledge, &object, &facts);
            let named_by = |named: &Object, by: &NamedBy| named_by(snapshot, named, by);
            let board = Board {
                subject: subject(snapshot, contents.graph, &object)?,
                layer,
                path,
                elevation,
                column,
                counts: counts.collect(),
                chips: known_chips(&found.data, &Deciding::of(&snapshot.kinds, &object)),
                level: level.unwrap_or_default(),
                resting: transect_observe::health::resting(
                    &snapshot.kinds,
                    snapshot,
                    &object,
                    found,
                ),
                plurals,
                page: &page_of,
                ground: &|title| super::ground_link(model, title),
                device: &|device| snapshot.devices.named(device, &snapshot.resources.devices),
                standing: &|other| standing(snapshot, other, now),
                readings: &|other, reads| {
                    readings::readings(snapshot, &endpoints, other, reads, now)
                },
                subjects: &subjects,
                named_by: &named_by,
                ends: snapshot,
                judged: judged.as_ref(),
            };
            // The card is on the object's own page, so its title does not
            // link.
            let card = workload.then(|| workload_card(&object, &contents).with_href(None));
            let known = known_through(&object, &contents)?;
            let deciding = Deciding::of(&snapshot.kinds, &object);
            let resting =
                transect_observe::health::resting(&snapshot.kinds, snapshot, &object, found);
            let reported =
                transect_observe::health::reported(&snapshot.kinds, snapshot, &object, found);
            let object_card = ObjectCard {
                known: Some(known),
                ..card::card(
                    snapshot,
                    &object,
                    Some(&found.metadata),
                    contents.graph,
                    reported,
                    resting.is_some(),
                    now,
                )
            };
            let center = Center {
                kind: object.kind.clone(),
                name: object.name.clone(),
                level: reported,
                resting,
                note: None,
                chips: every_chip(&found.data, &deciding),
                messages: super::object::messages(&objects::conditions(&found.data, &deciding)),
            };
            let found = super::object::Found::of(found);
            let sections = super::object::sections(model, &object, &found, contents.graph, now);
            let nesting = super::object::part_of::nesting(snapshot, contents.graph, &object);
            let page = KnownPage::build(board, card, object_card).with_board(center, sections);
            Some(page.with_nesting(nesting))
        },
        now,
    )?;
    let mut page = page;
    if super::has_access(model, namespace, resource, name) {
        let path = transect_graph::links::page(&object, plurals)?;
        page.tabs = page.tabs.map(|tabs| tabs.with_access(&path));
    }
    Some(page)
}

/// What a sketch reads of `object`, for a part that it draws as an
/// elevation of the part's kind: the knowledge of its kind, its
/// relations, and its conditions. It is none for an object that no store
/// holds or whose kind has no knowledge.
fn subject<'a>(snapshot: &'a Snapshot, graph: &Graph, object: &Object) -> Option<Subject<'a>> {
    let knowledge = snapshot.kinds.of_object(object)?;
    let found = super::object::stored(snapshot, object)??;
    let deciding = Deciding::of(&snapshot.kinds, object);
    Some(Subject {
        object: object.clone(),
        knowledge,
        reached: snapshot.relations_reached(object, graph),
        conditions: objects::conditions(&found.data, &deciding),
    })
}

/// The objects that name `named` at the path of `by`, in the order of
/// their namespaces and names, so the parts of a role stand in the same
/// order on every load.
fn named_by(snapshot: &Snapshot, named: &Object, by: &NamedBy) -> Vec<Object> {
    let relations = snapshot.relations.iter();
    let naming = relations.filter(|(referrer, relations)| by.names(referrer, relations, named));
    let mut found: Vec<Object> = naming.map(|(referrer, _)| referrer.clone()).collect();
    found.sort_by(|a, b| (&a.namespace, &a.name).cmp(&(&b.namespace, &b.name)));
    found
}

/// What the cluster says about `object`, which a part of a sketch names,
/// with the reason of its health condition when it is not healthy. An
/// object of a kind that no store holds is drawn plainly, because the
/// snapshot cannot say whether it exists. So is an object of a kind with
/// no knowledge, because only knowledge says which condition is its
/// health. A `Pod` stands as the rules that mark a `Pod` on every page
/// say.
fn standing(snapshot: &Snapshot, object: &Object, now: Timestamp) -> Standing {
    if let Some(standing) = core::standing(snapshot, object, now) {
        return standing;
    }
    let Some(stored) = super::object::stored(snapshot, object) else {
        return Standing::Plain;
    };
    let Some(found) = stored else {
        return Standing::Missing;
    };
    let kinds = &snapshot.kinds;
    if kinds.of_object(object).is_none() {
        return Standing::Plain;
    }
    let found = found.dynamic();
    if let Some((reason, message)) =
        transect_observe::health::at_rest(kinds, snapshot, object, &found)
    {
        return Standing::Resting(Why { reason, message });
    }
    match transect_observe::health::condition(kinds, snapshot, object, &found) {
        Some(said) => unwell(said),
        None => Standing::Plain,
    }
}

/// How an object stands whose health condition gave `said`. A condition
/// with no reason still gets a short line, so the part says which
/// condition is not `True`. A health through a reference that leads to
/// no object says why.
fn unwell(said: Reason) -> Standing {
    let level = said.level();
    let (condition, status, reason, message) = match said {
        Reason::Condition {
            condition,
            status,
            reason,
            message,
            ..
        } => (condition, status, reason, message),
        Reason::Unfollowed { why, .. } => {
            return Standing::Warning(Why {
                reason: why,
                message: None,
            });
        }
        _ => return Standing::Plain,
    };
    let reason = reason.unwrap_or_else(|| match status {
        Some(status) => format!("{condition} is {status}"),
        None => format!("no {condition} condition"),
    });
    let why = Why { reason, message };
    match level {
        Level::Failing => Standing::Failing(why),
        _ => Standing::Warning(why),
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::super::super::fixture;
    use super::*;

    #[rstest]
    #[case::healthy("stock.example.test", "Room", "acme-ops", "back-room", Standing::Plain)]
    #[case::at_rest(
        "promotions.example.test",
        "Promotion",
        "acme-shop",
        "clearance",
        Standing::Resting(Why {
            reason: "Scheduled".into(),
            message: Some("starts at the end of the month".into()),
        })
    )]
    #[case::no_such_object("stock.example.test", "Room", "acme-ops", "cellar", Standing::Missing)]
    #[case::a_kind_with_no_knowledge(
        "gizmos.example.test",
        "Gizmo",
        "acme-shop",
        "gizmo-blue",
        Standing::Plain
    )]
    #[case::a_kind_no_store_holds("", "Secret", "acme-shop", "storefront-tls", Standing::Plain)]
    #[case::a_kind_not_watched(
        "stock.example.test",
        "Lamp",
        "acme-ops",
        "strip-light",
        Standing::Plain
    )]
    #[case::through_a_reference_to_an_object_at_rest(
        "stock.example.test",
        "Vent",
        "acme-ops",
        "east-vent",
        Standing::Resting(Why {
            reason: "Sleeping".into(),
            message: Some("wakes every ten minutes".into()),
        })
    )]
    #[case::through_a_reference_to_no_object(
        "stock.example.test",
        "Vent",
        "acme-ops",
        "west-vent",
        Standing::Warning(Why {
            reason: "the cluster has no such Thermostat".into(),
            message: None,
        })
    )]
    fn says_how_a_named_object_stands(
        #[case] group: &str,
        #[case] kind: &str,
        #[case] namespace: &str,
        #[case] name: &str,
        #[case] expected: Standing,
    ) {
        let object = Object::new(group, kind, Some(namespace), name);
        let now = Timestamp::UNIX_EPOCH;
        assert_eq!(standing(&fixture::snapshot(), &object, now), expected);
    }

    fn said(status: Option<&str>, reason: Option<&str>, message: Option<&str>) -> Reason {
        Reason::Condition {
            workload: Object::new("stock.example.test", "Room", Some("acme-ops"), "cellar"),
            condition: "Ready".into(),
            status: status.map(String::from),
            reason: reason.map(String::from),
            message: message.map(String::from),
        }
    }

    fn why(reason: &str, message: Option<&str>) -> Why {
        Why {
            reason: reason.into(),
            message: message.map(String::from),
        }
    }

    #[rstest]
    #[case::false_with_a_reason(
        said(Some("False"), Some("DoorOpen"), Some("The bay door is open")),
        Standing::Failing(why("DoorOpen", Some("The bay door is open")))
    )]
    #[case::false_with_no_reason(
        said(Some("False"), None, None),
        Standing::Failing(why("Ready is False", None))
    )]
    #[case::unknown(
        said(Some("Unknown"), None, None),
        Standing::Warning(why("Ready is Unknown", None))
    )]
    #[case::no_condition(
        said(None, None, None),
        Standing::Warning(why("no Ready condition", None))
    )]
    #[case::not_a_condition(
        Reason::LastRunFailed {
            workload: transect_observe::health::Workload {
                kind: transect_observe::health::Kind::CronJob,
                name: "nightly".into(),
            },
        },
        Standing::Plain
    )]
    fn gives_the_reason_of_an_unhealthy_condition(
        #[case] said: Reason,
        #[case] expected: Standing,
    ) {
        assert_eq!(unwell(said), expected);
    }
}
