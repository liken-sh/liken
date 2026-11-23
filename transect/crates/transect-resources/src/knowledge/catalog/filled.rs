//! A kind's `pattern`: which pattern of the catalog the kind fills, and
//! which entry of its `references` fills each role that the pattern asks
//! a kind to fill. Every fault shows in the document alone, because a
//! role names an entry of the same document and the catalog is in the
//! binary.

use serde::Deserialize;

use super::super::Reference;
use super::super::references::target_of;
use super::super::role::{self, Fill, First, Role};
use super::{Pattern, Source, definitions};

/// `pattern`, as a `KindKnowledge` writes it.
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Written {
    name: String,
    #[serde(default)]
    roles: serde_yaml_ng::Mapping,
}

/// One role of `pattern.roles`, which names an entry of `references`.
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct WrittenRole {
    reference: String,
}

/// The pattern that `written`, a document's `pattern`, fills from
/// `references`, the document's own. The error names the first fault.
pub fn fill(written: serde_yaml_ng::Value, references: &[Reference]) -> Result<Pattern, String> {
    let written: Written =
        serde_yaml_ng::from_value(written).map_err(|error| format!("pattern: {error}"))?;
    let Some(definition) = definitions().iter().find(|d| d.name == written.name) else {
        let names: Vec<&str> = definitions().iter().map(|d| d.name.as_str()).collect();
        return Err(format!(
            "pattern.name is {:?}, which is not a pattern of transect's catalog: {}",
            written.name,
            names.join(", ")
        ));
    };
    let mut filled: Vec<(String, String)> = Vec::new();
    for (name, fields) in role::entries(Some(written.roles))? {
        let asked = definition.roles.iter().find(|t| t.name == name);
        let Some(Source::Fill {
            group, kind, list, ..
        }) = asked.map(|t| &t.source)
        else {
            return Err(format!(
                "pattern.roles has {name}, which {} does not ask a kind to fill",
                definition.name
            ));
        };
        let role: WrittenRole = serde_yaml_ng::from_value(fields.into()).map_err(|error| {
            format!("pattern.roles.{name}: {error}, and a role of a pattern has only reference")
        })?;
        let path = role.reference;
        let fault = |text: String| Err(format!("pattern.roles.{name}: {text}"));
        let Some(entry) = references.iter().find(|r| r.path == path) else {
            return fault(format!(
                "reference {path} is the path or the name of no entry of references"
            ));
        };
        if target_of(references, &path) != Some((group.as_str(), kind.as_str())) {
            let named = match group.is_empty() {
                true => kind.clone(),
                false => format!("{kind} of {group}"),
            };
            return fault(format!(
                "reference {path} names no {named}, and the role's parts are each a {named}"
            ));
        }
        if *list && (entry.by_name || !path.contains("[]")) {
            return fault(format!(
                "reference {path} reads one value, and the role reads a list, so its path has []"
            ));
        }
        filled.push((name, path));
    }
    let mut roles = Vec::new();
    for template in &definition.roles {
        let written = filled.iter().find(|(name, _)| *name == template.name);
        let (fill, unresolved) = match (&template.source, written) {
            (Source::Derive(derive), _) => (First::Derived(derive.clone()), None),
            (Source::Fill { .. }, Some((_, path))) => (First::Reference(path.clone()), None),
            (Source::Fill { required: true, .. }, None) => {
                return Err(format!(
                    "pattern.roles has no {}, which {} requires",
                    template.name, definition.name
                ));
            }
            (Source::Fill { kind, .. }, None) => (
                First::Unfilled,
                Some(format!(
                    "pattern.roles has no {}, so the KindKnowledge names no {kind} for it",
                    template.name
                )),
            ),
        };
        let look = template.look.clone();
        let role = Role::new(
            template.name.clone(),
            Fill::of(fill),
            None,
            look,
            references,
        )
        .map_err(|error| format!("pattern.roles.{}: {error}", template.name))?;
        roles.push(Role { unresolved, ..role });
    }
    Ok(Pattern {
        name: definition.name.clone(),
        title: definition.title.clone(),
        roles,
        elevations: definition.elevations.clone(),
        failover: definition.failover.clone(),
    })
}
