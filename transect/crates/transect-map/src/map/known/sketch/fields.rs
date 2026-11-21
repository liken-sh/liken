//! The fields that a role's parts are read from, as a part's source
//! line and a wire's source name them. A reference by a name template
//! is written as the name that it makes for the page's object, linked to
//! that object's page, because a reader knows the name and not the
//! template.

use super::super::board::{Board, Subject};
use transect_graph::edges::Object;
use transect_graph::links::Reference;
use transect_resources::knowledge::role::{First, Role};
use transect_resources::knowledge::target_of;

/// The fields of `role` in `subject`'s elevation, as words and links.
pub fn fields(board: &Board, subject: &Subject, role: &Role) -> Vec<Reference> {
    let references = &subject.knowledge.references;
    let text = role.fill.fields_of(references, &subject.object);
    let named = match &role.fill.first {
        First::Reference(path) => references
            .iter()
            .find(|reference| reference.path == *path && reference.by_name),
        _ => None,
    };
    let Some(named) = named else {
        return vec![words(&text)];
    };
    let name = named.field_of(&subject.object);
    let href = target_of(references, &named.path).and_then(|(group, kind)| {
        let object = Object::new(group, kind, subject.object.namespace.as_deref(), &name);
        (board.page)(&object)
    });
    let rest = text.strip_prefix(&name).unwrap_or_default();
    let mut pieces = vec![Reference { text: name, href }];
    if !rest.is_empty() {
        pieces.push(words(rest));
    }
    pieces
}

/// Plain words.
pub fn words(text: &str) -> Reference {
    Reference {
        text: text.into(),
        href: None,
    }
}

/// `pieces` as plain text.
pub fn text(pieces: &[Reference]) -> String {
    pieces.iter().map(|piece| piece.text.as_str()).collect()
}
