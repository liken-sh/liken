//! A stock room for the tests of roles whose parts name the page's
//! object, follow one more reference, or are drawn as an elevation of
//! their own kind. A shelf names its room, a bin names its shelf, its
//! room, and the ladder that reaches it, and a keeper names a room. A
//! shelf's elevation draws its room and its bins, and a bin's draws its
//! ladder.

use std::collections::HashMap;

use super::super::super::{Board, KnownPage, Standing, Subject};
use crate::objects::{Condition, ObjectCard};
use transect_graph::edges::{ClusterScoped, Endpoint, Object, Plurals};
use transect_observe::health::Level;
use transect_resources::knowledge::fixtures::knowledge;
use transect_resources::knowledge::role::NamedBy;
use transect_resources::knowledge::{
    self, Followed, KindKnowledge, Kinds, Matches, Names, Reached, ReferenceEnds, Relation,
};

pub fn object(kind: &str, name: &str) -> Object {
    Object::new("stock.example.com", kind, Some("ops"), name)
}

/// The knowledge of each kind of the room's parts, by kind.
fn kinds() -> HashMap<&'static str, KindKnowledge> {
    let shelf = knowledge(
        "shelves.stock.example.com",
        "{references: [{path: spec.room, kind: Room, group: stock.example.com, edge: part of}],
          roles: {room: {reference: spec.room, expected: true},
                  bins: {namedBy: {kind: Bin, path: spec.shelf}, elevation: the bin, expected: true}},
          elevations: [{name: the shelf, question: What is on it?, sketch: room bins}]}",
    );
    let bin = knowledge(
        "bins.stock.example.com",
        "{references: [{path: spec.shelf, kind: Shelf, group: stock.example.com, edge: part of},
                       {path: spec.room, kind: Room, group: stock.example.com},
                       {path: spec.ladder, kind: Ladder, group: stock.example.com}],
          roles: {ladder: {reference: spec.ladder}},
          elevations: [{name: the bin, question: What reaches it?, sketch: ladder}]}",
    );
    let keeper = knowledge(
        "keepers.stock.example.com",
        "{references: [{path: spec.room, kind: Room, group: stock.example.com}]}",
    );
    HashMap::from([("Shelf", shelf), ("Bin", bin), ("Keeper", keeper)])
}

/// The cluster of a test: each object with its spec, the ends of each
/// bin's ladder, and the room's conditions as type, status, and reason.
#[derive(Default)]
pub struct Stock {
    pub objects: Vec<(Object, &'static str)>,
    pub ladders: HashMap<&'static str, Vec<Ladder>>,
    pub conditions: Vec<(&'static str, &'static str, Option<&'static str>)>,
}

/// One end of a bin's ladder, as `knowledge::follow` finds it.
#[derive(Clone, Copy)]
pub enum Ladder {
    Found(&'static str),
    Missing(&'static str),
    Unwatched(&'static str),
    Ambiguous(usize),
}

/// The ends of the ladders of `Stock`, with a ladder that the store
/// holds for each that it finds.
struct Ends<'a> {
    ladders: &'a HashMap<&'static str, Vec<Ladder>>,
    stored: kube::core::DynamicObject,
}

impl ReferenceEnds for Ends<'_> {
    fn ends(&self, from: &Object, path: &str) -> Vec<Followed<'_>> {
        let ends = self
            .ladders
            .get(from.name.as_str())
            .filter(|_| path == "spec.ladder");
        let ladder = |name: &str| object("Ladder", name);
        let followed = ends.into_iter().flatten().map(|end| match *end {
            Ladder::Found(name) => Followed::Found(ladder(name), &self.stored),
            Ladder::Missing(name) => Followed::Missing(ladder(name)),
            Ladder::Unwatched(name) => Followed::Unwatched(ladder(name)),
            Ladder::Ambiguous(count) => Followed::Ambiguous(count),
        });
        followed.collect()
    }
}

fn reach(relation: &Relation) -> Reached {
    let ends = match &relation.names {
        Names::Object(object) => vec![Endpoint::Object(object.clone())],
        _ => Vec::new(),
    };
    Reached {
        ends,
        guessed: false,
    }
}

/// The page of the room `back`, whose roles and elevation `room` writes
/// in YAML, in the cluster `stock`. Each object of `stock` stands as
/// `standing`.
pub fn room_page(room: &str, stock: &Stock, standing: Standing) -> KnownPage {
    let mut known: Vec<KindKnowledge> = kinds().into_values().collect();
    known.push(knowledge("rooms.stock.example.com", room));
    let served = ["Room", "Shelf", "Bin", "Keeper", "Ladder"].map(|kind| {
        let plural = format!("{}s", kind.to_lowercase()).replace("shelfs", "shelves");
        (("stock.example.com".to_string(), kind.to_string()), plural)
    });
    let resolved = Kinds::resolve(&known, &Plurals::from(served), &[]);
    let of = |kind: &str| resolved.of("stock.example.com", kind).unwrap();
    let room = of("Room");
    let scoped = ClusterScoped::default();
    let read = |object: &Object, spec: &str| {
        let known = of(&object.kind);
        let data = serde_yaml_ng::from_str(&format!("{{spec: {spec}}}")).unwrap();
        knowledge::relations(known, object, &data, &scoped, &Matches::default())
    };
    let relations: Vec<(Object, Vec<Relation>)> = stock
        .objects
        .iter()
        .map(|(object, spec)| (object.clone(), read(object, spec)))
        .collect();
    let subject = |object: &Object| -> Option<Subject> {
        let (_, found) = relations.iter().find(|(other, _)| other == object)?;
        Some(Subject {
            object: object.clone(),
            knowledge: of(&object.kind),
            reached: found.iter().map(|r| (r, reach(r))).collect(),
            conditions: Vec::new(),
        })
    };
    let named_by = |named: &Object, by: &NamedBy| {
        let naming = relations
            .iter()
            .filter(|(o, found)| by.names(o, found, named));
        naming.map(|(o, _)| o.clone()).collect()
    };
    let ends = Ends {
        ladders: &stock.ladders,
        stored: serde_yaml_ng::from_str(
            "{apiVersion: stock.example.com/v1, kind: Ladder, metadata: {name: a}}",
        )
        .unwrap(),
    };
    let plurals = Plurals::default();
    let conditions = stock
        .conditions
        .iter()
        .map(|(kind, status, reason)| Condition {
            kind: kind.to_string(),
            status: status.to_string(),
            reason: reason.map(String::from),
            message: None,
            level: None,
        });
    let board = Board {
        subject: Subject {
            object: object("Room", "back"),
            knowledge: room,
            reached: Vec::new(),
            conditions: conditions.collect(),
        },
        layer: "ops".into(),
        path: "/namespaces/ops/rooms.stock.example.com/back".into(),
        elevation: None,
        column: Vec::new(),
        counts: Vec::new(),
        chips: Vec::new(),
        level: Level::Healthy,
        resting: None,
        plurals: &plurals,
        page: &|object| Some(format!("/{}/{}", object.kind, object.name)),
        ground: &|_| None,
        device: &|device| device.short(),
        standing: &|_| standing.clone(),
        readings: &|_, _| Vec::new(),
        subjects: &subject,
        named_by: &named_by,
        ends: &ends,
        judged: None,
    };
    let card = ObjectCard {
        kind: "Room".into(),
        name: "back".into(),
        level: Some(Level::Healthy),
        resting: false,
        why: None,
        controllers: Vec::new(),
        code: Vec::new(),
        known: None,
        facts: Vec::new(),
    };
    KnownPage::build(board, None, card)
}

/// The knowledge of a room with the roles that `roles` writes, placed
/// by `sketch`.
pub fn room(roles: &str, sketch: &str) -> String {
    format!(
        "{{roles: {{{roles}}}, elevations: [{{name: the room, question: What is in it?, sketch: {sketch:?}}}]}}"
    )
}

/// Two shelves, `top` and `low`, in the room, and a bin on `top`.
pub fn shelves() -> Vec<(Object, &'static str)> {
    vec![
        (object("Shelf", "top"), "{room: back}"),
        (object("Shelf", "low"), "{room: back}"),
        (object("Bin", "screw"), "{shelf: top, room: back}"),
    ]
}
