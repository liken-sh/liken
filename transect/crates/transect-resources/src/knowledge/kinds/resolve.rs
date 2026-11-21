//! The checks of `Kinds::resolve` that need the knowledge of a second
//! kind: each `through` that only the knowledge of the kind it starts
//! from can confirm. One that does not resolve keeps the reason, so the
//! page shows the reason in place of what the reference leads to.

use super::super::references::target_of;
use super::super::{Healthy, KindKnowledge, Reference};
use super::Kinds;
use crate::resources::Condition;

impl Kinds {
    /// Confirms each `through` that the knowledge of another kind must
    /// confirm: a reading through a reference of the part's kind, and a
    /// health through a reference to a kind whose knowledge must state a
    /// condition of its own. Each that does not resolve keeps the reason
    /// and gives its document the condition `Unresolved`. The document
    /// stays in use.
    pub(super) fn resolve_throughs(&mut self) {
        let checked: Vec<_> = self
            .by_kind
            .iter()
            .map(|(key, known)| (key.clone(), self.checked(known)))
            .collect();
        for (key, (known, messages)) in checked {
            let conditions = messages.into_iter().map(Condition::unresolved);
            self.conditions
                .extend(conditions.map(|condition| (known.doc, condition)));
            self.by_kind.insert(key, known);
        }
    }

    /// `known` with the reason of each `through` that does not resolve,
    /// and the messages of its conditions.
    fn checked(&self, known: &KindKnowledge) -> (KindKnowledge, Vec<String>) {
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
            let part = target_of(&references, &role.reference);
            for column in &mut role.reads {
                let Some(through) = &mut column.through else {
                    continue;
                };
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

    /// Why a reading through `path` of the part's kind `part` does not
    /// resolve: the part's kind must have knowledge with a reference at
    /// `path` that names a kind.
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
}

#[cfg(test)]
mod tests;
