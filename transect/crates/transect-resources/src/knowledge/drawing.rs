//! Finds the shape that each part of a role is drawn in, for the roles
//! of a kind and for the roles of the pattern that the kind fills, and
//! leaves out each wire that meets a shape with no port on the facing
//! edge.

use std::sync::Arc;

use super::{Elevation, Reference, role, wire};
use crate::shapes::{Shape, Shapes, library};

/// What `KindKnowledge::draw_with` finds the shapes of roles with: the
/// group and the references of the kind whose roles they are, the
/// loaded shapes, and the shape that the knowledge of each kind states.
pub(super) struct Drawing<'a> {
    pub group: &'a str,
    pub references: &'a [Reference],
    pub shapes: &'a Shapes,
    pub of_kind: &'a dyn Fn(&str, &str) -> Option<Arc<Shape>>,
}

impl Drawing<'_> {
    /// Finds the shape of each of `roles`, and leaves out each wire of
    /// `elevations` that meets a role whose shape has no port on the
    /// facing edge. It returns the messages of the conditions.
    pub fn draw(
        &self,
        roles: &mut [role::Role],
        elevations: &mut [Elevation],
    ) -> (Vec<String>, Vec<String>) {
        let mut unknown = Vec::new();
        for role in roles.iter_mut() {
            role.drawn = self.shape_of(role).unwrap_or_else(|message| {
                unknown.push(message);
                library::default()
            });
        }
        let mut unported = Vec::new();
        for elevation in elevations {
            let grid = &elevation.grid;
            let shape_of = |name: &str| {
                let role = roles.iter().find(|role| role.name == name)?;
                Some(grid.posed(name, &role.drawn))
            };
            let faults = wire::leave_unported(&mut elevation.wires, grid, shape_of);
            let named = faults
                .into_iter()
                .map(|f| format!("elevation {:?}: {f}", elevation.name));
            unported.extend(named);
        }
        (unknown, unported)
    }

    /// The shape that each part of `role` is drawn in: the first of the
    /// role's `shape`, the shape that the knowledge of the part's kind
    /// states, and the library's default. The kind is the kind of the
    /// role's parts, which `Fill::parts_kind` gives, so an expected role
    /// with no part takes the same shape as its parts would. A reference
    /// that names no kind reads a value, so only the role can state its
    /// shape. The error is
    /// the message of a condition for a role whose shape is not loaded.
    fn shape_of(&self, role: &role::Role) -> Result<Arc<Shape>, String> {
        if let Some(written) = &role.shape {
            return self.shapes.resolve(written, self.group).cloned().ok_or_else(|| {
                format!(
                    "the role {} names the shape {written:?}, which transect has not loaded, so it is drawn as a {}",
                    role.name,
                    library::DEFAULT
                )
            });
        }
        let kind = role.fill.parts_kind(self.references);
        let stated = kind.and_then(|(group, kind)| (self.of_kind)(group, kind));
        Ok(stated.unwrap_or_else(library::default))
    }
}
