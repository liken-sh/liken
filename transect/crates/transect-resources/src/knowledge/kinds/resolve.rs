//! The checks of `Kinds::resolve` that need the knowledge of a second
//! kind: each `through` that only the knowledge of the kind it starts
//! from can confirm, each `namedBy` that only the knowledge of the
//! naming kind can confirm, and each `elevation` that only the knowledge
//! of the parts' kind states. One that does not resolve keeps the
//! reason, so the page shows the reason in place of what it leads to.

use super::super::references::target_of;
use super::super::role::{First, Role};
use super::super::{Healthy, KindKnowledge, Reference};
use super::Kinds;
use crate::resources::Condition;

impl Kinds {
    /// Confirms each reference and elevation that the knowledge of
    /// another kind must confirm: a reading through a reference of the
    /// part's kind, a health through a reference to a kind whose
    /// knowledge must state a condition of its own, and a role's
    /// `namedBy`, `through`, and `elevation`. Each that does not resolve
    /// keeps the reason and gives its document the condition
    /// `Unresolved`. The document stays in use.
    pub(super) fn resolve_throughs(&mut self) {
        let checked: Vec<_> = self
            .by_kind
            .iter()
            .map(|(key, known)| (key.clone(), self.checked(key, known)))
            .collect();
        for (key, (known, messages)) in checked {
            let conditions = messages.into_iter().map(Condition::unresolved);
            self.conditions
                .extend(conditions.map(|condition| (known.doc, condition)));
            self.by_kind.insert(key, known);
        }
    }

    /// `known`, the knowledge of the kind `own`, with the reason of each
    /// reference that does not resolve, and the messages of its
    /// conditions.
    fn checked(
        &self,
        own: &(String, String),
        known: &KindKnowledge,
    ) -> (KindKnowledge, Vec<String>) {
        let mut known = known.clone();
        let references = known.references.clone();
        let mut messages = Vec::new();
        if let Some(Healthy::Through(through)) = &mut known.healthy
            && let Some(fault) = self.health_fault(&references, &through.path)
        {
            let message = format!("healthy follows {}, and {fault}", through.path);
            through.unresolved = Some(message.clone());
            messages.push(message);
        }
        for role in &mut known.roles {
            messages.extend(self.role_faults(role, own, &references));
            let part = role.fill.parts_kind(&references);
            let part = part.map(|(group, kind)| (group.to_string(), kind.to_string()));
            for column in &mut role.reads {
                let Some(through) = &mut column.through else {
                    continue;
                };
                let part = part.as_ref().map(|(g, k)| (g.as_str(), k.as_str()));
                let Some(fault) = self.reading_fault(part, &through.path) else {
                    continue;
                };
                let (role, name, path) = (&role.name, &column.name, &through.path);
                let message = format!("roles.{role} reads {name} through {path}, and {fault}");
                through.unresolved = Some(message.clone());
                messages.push(message);
            }
        }
        (known, messages)
    }

    /// The messages of the faults of `role`, a role of the kind `own`,
    /// whose document has `references`, after it keeps each reason: its
    /// `namedBy` must name a reference to `own` in the knowledge of the
    /// naming kind, its `through` a reference to a kind in the knowledge
    /// of the first step's kind, and its `elevation` an elevation of the
    /// parts' kind.
    fn role_faults(
        &self,
        role: &mut Role,
        own: &(String, String),
        references: &[Reference],
    ) -> Vec<String> {
        let mut messages = Vec::new();
        let name = role.name.clone();
        if let First::NamedBy(named) = &role.fill.first
            && let Some(fault) = self.named_by_fault(named, own)
        {
            let message = format!(
                "roles.{name} is named by {} {}, and {fault}",
                named.kind, named.path
            );
            role.unresolved = Some(message.clone());
            messages.push(message);
        }
        let first = role.fill.first_kind(references);
        let first = first.map(|(group, kind)| (group.to_string(), kind.to_string()));
        if let Some(through) = &mut role.fill.through {
            let first = first.as_ref().map(|(g, k)| (g.as_str(), k.as_str()));
            match self.reading_fault(first, &through.path) {
                Some(fault) => {
                    let message = format!("roles.{name} follows {}, and {fault}", through.path);
                    through.unresolved = Some(message.clone());
                    messages.push(message);
                }
                None => through.target = first.and_then(|(g, k)| self.target(g, k, &through.path)),
            }
        }
        if let Some(elevation) = &role.elevation
            && role.unresolved().is_none()
            && let Some(fault) = self.elevation_fault(role.fill.parts_kind(references), elevation)
        {
            let message = format!("roles.{name} draws each part as {elevation:?}, and {fault}");
            role.unresolved = Some(message.clone());
            messages.push(message);
        }
        messages
    }

    /// The group and the kind that the reference at `path` of the kind
    /// `group`/`kind` names.
    fn target(&self, group: &str, kind: &str, path: &str) -> Option<(String, String)> {
        let other = self.of(group, kind)?;
        let (group, kind) = target_of(&other.references, path)?;
        Some((group.to_string(), kind.to_string()))
    }

    /// Why a health through the reference at `path` among `references`
    /// does not resolve: the kind it names must have knowledge that
    /// states a condition of its own.
    fn health_fault(&self, references: &[Reference], path: &str) -> Option<String> {
        let (group, kind) = target_of(references, path)?;
        match self.of(group, kind) {
            None => Some(format!("transect has no knowledge of {kind}")),
            Some(other) if other.condition().is_none() => Some(format!(
                "the knowledge of {kind} states no condition of its own"
            )),
            Some(_) => None,
        }
    }

    /// Why a reference at `path` of the kind `part` does not resolve:
    /// the kind must have knowledge with a reference at `path` that
    /// names a kind.
    fn reading_fault(&self, part: Option<(&str, &str)>, path: &str) -> Option<String> {
        let (group, kind) = part?;
        match self.of(group, kind) {
            None => Some(format!("transect has no knowledge of {kind}")),
            Some(other) if target_of(&other.references, path).is_none() => Some(format!(
                "the knowledge of {kind} has no reference at {path} that names a kind"
            )),
            Some(_) => None,
        }
    }

    /// Why the objects that name an object of the kind `own` do not
    /// resolve: the knowledge of the naming kind must state a reference
    /// at the path to `own`, because the page reads the relation from
    /// that reference.
    fn named_by_fault(
        &self,
        named: &super::super::role::NamedBy,
        own: &(String, String),
    ) -> Option<String> {
        let (group, kind) = (own.0.as_str(), own.1.as_str());
        let Some(other) = self.of(&named.group, &named.kind) else {
            return Some(format!("transect has no knowledge of {}", named.kind));
        };
        match target_of(&other.references, &named.path) {
            Some(target) if target == (group, kind) => None,
            _ => Some(format!(
                "the knowledge of {} has no reference at {} to {kind}",
                named.kind, named.path
            )),
        }
    }

    /// Why the parts of the kind `part` cannot be drawn as `elevation`:
    /// the knowledge of their kind must state it. The parser refuses an
    /// `elevation` on a role whose parts are values, so a role with no
    /// kind here follows a `through` that did not resolve, and that
    /// reason is kept already.
    fn elevation_fault(&self, part: Option<(&str, &str)>, elevation: &str) -> Option<String> {
        let (group, kind) = part?;
        let other = self.of(group, kind);
        let states =
            other.is_some_and(|other| other.elevations.iter().any(|e| e.name == elevation));
        (!states).then(|| format!("the knowledge of {kind} states no elevation {elevation:?}"))
    }
}

#[cfg(test)]
mod tests;
