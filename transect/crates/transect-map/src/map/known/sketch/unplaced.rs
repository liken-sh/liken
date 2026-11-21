//! The line under a sketch that names the parts of the roles that the
//! elevation does not place, as the drawing would name them, and the
//! elevations that draw them. A reader of one elevation sees what it
//! leaves out without leaving the page.

use super::super::board::Board;
use super::fill;
use super::head::href;
use super::part::end_of;
use super::wires::listed;
use transect_graph::links::Reference;
use transect_resources::knowledge::Elevation;

/// The line as words and links: "Not in the wiring: controlled by
/// Handset a, and holds Claim b. The room draws them." It is none
/// when every role with a part is placed.
pub fn line(board: &Board, elevation: &Elevation) -> Option<Vec<Reference>> {
    let word = |text: &str| Reference {
        text: text.into(),
        href: None,
    };
    let placed = elevation.grid.names();
    let subject = &board.subject;
    let mut roles = Vec::new();
    for role in &subject.knowledge.roles {
        if placed.contains(&role.name.as_str()) {
            continue;
        }
        let found = fill::found(board, subject, role);
        let names: Vec<Reference> = found
            .iter()
            .map(|found| end_of(board, &subject.object, found).reference)
            .collect();
        if !names.is_empty() {
            let label = role.label(&subject.knowledge.references);
            roles.push((role.name.as_str(), label, names));
        }
    }
    if roles.is_empty() {
        return None;
    }
    let mut pieces = vec![word(&format!("Not in {}: ", elevation.name))];
    let last = roles.len() - 1;
    for (at, (_, label, names)) in roles.iter().enumerate() {
        if at > 0 {
            pieces.push(word(match (at == last, last) {
                (true, 1) => " and ",
                (true, _) => ", and ",
                (false, _) => ", ",
            }));
        }
        pieces.push(word(&format!("{label} ")));
        pieces.extend(listed(names));
    }
    pieces.push(word("."));
    let elevations = subject.knowledge.elevations.iter().filter(|other| {
        let names = other.grid.names();
        other.slug != elevation.slug && roles.iter().any(|(name, ..)| names.contains(name))
    });
    let drawn_in: Vec<Reference> = elevations
        .map(|other| Reference {
            text: other.name.clone(),
            href: Some(href(&board.path, &other.slug)),
        })
        .collect();
    if let Some(first) = drawn_in.first() {
        let mut listed = listed(&drawn_in);
        listed[0] = Reference {
            text: capitalized(&first.text),
            ..first.clone()
        };
        pieces.push(word(" "));
        pieces.extend(listed);
        pieces.push(word(match drawn_in.len() {
            1 => " draws them.",
            _ => " draw them.",
        }));
    }
    Some(pieces)
}

/// `text` with its first letter in upper case, to start a sentence.
fn capitalized(text: &str) -> String {
    let mut chars = text.chars();
    let first = chars.next().map(|c| c.to_uppercase().collect::<String>());
    first.unwrap_or_default() + chars.as_str()
}
