//! One value of a reference's field as the page names it: a link to
//! the page of what it names, and a note on what it resolves to. The ring
//! of boxes and the sketch name a value the same way.

use super::Board;
use crate::shapes::text::End;
use transect_graph::edges::Endpoint;
use transect_graph::links::Reference;
use transect_resources::knowledge::{Names, Reached, Relation};

/// One value of a reference's field. It links to the page of the object
/// it names when that object has one, and a Node links to its machine's
/// page. `under` is the note under the value: what the value resolves
/// to, or why it resolves to nothing.
pub fn end(relation: &Relation, reached: &Reached, here: Option<&str>, board: &Board) -> End {
    let refer = |end: &Endpoint| refer(end, here, board);
    let (reference, itself) = match &relation.names {
        Names::Object(object) => {
            let end = Endpoint::Object(object.clone());
            (refer(&end), Some(end))
        }
        Names::Address | Names::Value | Names::Ambiguous(_) => (
            Reference {
                text: relation.value.clone(),
                href: None,
            },
            None,
        ),
    };
    let others: Vec<Reference> = reached
        .ends
        .iter()
        .filter(|end| Some(*end) != itself.as_ref())
        .map(refer)
        .collect();
    // An address that resolves to exactly one endpoint links to that
    // endpoint's page, because that is the page a reader who follows the
    // address is looking for.
    let reference = match (&relation.names, &others[..]) {
        (Names::Address, [one]) => Reference {
            href: one.href.clone(),
            ..reference
        },
        _ => reference,
    };
    let texts: Vec<&str> = others.iter().map(|other| other.text.as_str()).collect();
    let under = match (&relation.names, others.is_empty()) {
        (Names::Value, _) => Some("not an object in the cluster".into()),
        (Names::Ambiguous(count), _) => Some(format!("matches {count} objects, so none is drawn")),
        (Names::Address, true) => Some("an address transect does not resolve".into()),
        (_, true) => None,
        (_, false) => Some(format!("reaches {}", texts.join(", "))),
    };
    let reaches = match relation.names {
        Names::Value => Vec::new(),
        _ => others,
    };
    End {
        reference,
        under,
        reaches,
    }
}

/// `end` as a link to its page, when it has one: a Node links to its
/// machine's page, and a device is named as the board names devices.
pub fn refer(end: &Endpoint, here: Option<&str>, board: &Board) -> Reference {
    let mut reference = Reference::to_end(end, here, board.plurals);
    reference.href = match end {
        Endpoint::Object(object)
            if (object.group.as_str(), object.kind.as_str()) == ("", "Node") =>
        {
            Some(format!("/machines/{}", object.name))
        }
        Endpoint::Object(object) => (board.page)(object),
        Endpoint::Machine(machine) => Some(transect_graph::links::machine(machine)),
        Endpoint::Outside(name) => (board.ground)(name),
        Endpoint::Device(device) => transect_graph::links::device(device),
    };
    if let Endpoint::Device(device) = end {
        reference.text = (board.device)(device);
    }
    reference
}
