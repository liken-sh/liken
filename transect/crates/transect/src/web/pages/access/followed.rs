//! What follows from an account's grants, as the last column of its
//! drawing: the Secrets a pod it starts may mount, the accounts
//! it may run as, each account a step goes through, and below each, a
//! step at a time, how that account holds what follows, ending at the
//! role the last account is bound to.

use std::collections::BTreeSet;

use super::account::{FOLLOWS, listed};
use super::look::Look;
use crate::web::model::Model;
use transect_access::access::drawing::account::{Follow, Placing};
use transect_access::access::drawing::{FOLLOWS as OUTLINED, NONE};
use transect_access::access::{ALL, Account, AppliesAs, Capability, Held, Hop, Where};
use transect_map::map::section_drawing::{Look as BoxLook, SectionBox};

/// The most accounts a chain goes through before it stops.
const DEEPEST: usize = 4;

/// The grant box that shows a capability, by its place in the scene.
pub type Place<'a> = &'a dyn Fn(Capability) -> Option<usize>;

pub fn follows(
    model: &Model,
    account: &Account,
    held: &Held,
    place: Place,
    look: &Look,
) -> Vec<Follow> {
    // Each capability that follows, where, and its first hop.
    let mut steps: Vec<(Capability, &Where, &Hop, &[String])> = Vec::new();
    for capability in ALL {
        let Some(cell) = held.cells.get(&capability) else {
            continue;
        };
        for (at, follow) in &cell.follows {
            steps.push((capability, at, &follow.hop, &follow.names));
        }
    }
    if steps.is_empty() {
        return vec![nothing(held)];
    }
    let mut found: Vec<Follow> = Vec::new();
    let boxed = |title: &str, line: String| {
        SectionBox::new(title.into(), vec![line], BoxLook::Plain).with_block(FOLLOWS)
    };
    let starts: Vec<&Where> = steps
        .iter()
        .filter(|(_, _, hop, _)| matches!(hop, Hop::StartsPod { .. }))
        .map(|(_, at, _, _)| *at)
        .collect();
    if !starts.is_empty() {
        let line = format!(
            "a pod it starts may mount any Secret in its namespace, {}",
            places(&starts)
        );
        found.push(outlined(
            boxed("read Secrets", line),
            sources(place, &[Capability::RunPods, Capability::ChangeWorkloads]),
        ));
    }
    // A pod names any `ServiceAccount` in its namespace, so an account
    // that may run pods or change workloads may run as any there.
    let bases = [Capability::RunPods, Capability::ChangeWorkloads];
    let running: BTreeSet<Where> = bases
        .iter()
        .filter_map(|basis| held.cells.get(basis))
        .flat_map(|cell| cell.granted_unnamed().cloned())
        .collect();
    let runs_as = !running.is_empty();
    if runs_as {
        let running: Vec<&Where> = running.iter().collect();
        let title = SectionBox::new(
            "run as any account".into(),
            vec![format!(
                "a pod names any ServiceAccount in its namespace, {}",
                places(&running)
            )],
            BoxLook::Plain,
        );
        found.push(outlined(title, sources(place, &bases)));
    }
    // The accounts a step goes through, each once, with what follows
    // through it.
    let mut through: Vec<(Account, Hop, Vec<Capability>)> = Vec::new();
    for (capability, _, hop, _) in &steps {
        let Some(target) = hop.account() else {
            continue;
        };
        match through.iter_mut().find(|(t, _, _)| t == target) {
            Some((_, _, capabilities)) => {
                if !capabilities.contains(capability) {
                    capabilities.push(*capability);
                }
            }
            None => through.push((target.clone(), (*hop).clone(), vec![*capability])),
        }
    }
    // When an account a step reaches is bound to every verb in every
    // namespace, all that follows through any account follows through
    // it, so the drawing draws that one chain.
    if let Some(top) = top(model, &through) {
        through = vec![top];
    }
    // The accounts it runs as come first, right under the box that
    // states it may run as any.
    through.sort_by_key(|(_, hop, _)| !matches!(hop, Hop::RunsAs { .. }));
    let mut visited: BTreeSet<Account> = BTreeSet::from([account.clone()]);
    for (at, (target, hop, capabilities)) in through.into_iter().enumerate() {
        let runs_here = match &target {
            Account::ServiceAccount { namespace, .. } => {
                running.iter().any(|at| at.covers(namespace))
            }
            _ => false,
        };
        let after = (at == 0 && runs_as && runs_here).then(|| "one of them".to_string());
        let from = match &hop {
            Hop::RunsAs { .. } if after.is_some() => Vec::new(),
            Hop::RunsAs { basis, .. } => sources(place, &[*basis]),
            Hop::ExecToken { .. } => sources(place, &[Capability::Exec]),
            Hop::Applies { .. } => sources(place, &[Capability::ChangeApplied]),
            _ => Vec::new(),
        };
        found.push(Follow {
            placing: Placing {
                boxed: step(&target, &hop, look),
                class: OUTLINED,
            },
            grants: from,
            after,
        });
        visited.insert(target.clone());
        chain(
            model,
            &target,
            &capabilities,
            &mut visited,
            look,
            &mut found,
            1,
        );
    }
    for (_, at, hop, names) in &steps {
        if let Hop::ExecMounts { .. } = hop {
            let line = format!("by name, as its pods {} mount them", at.words());
            found.push(outlined(
                boxed("read Secrets", line).with_hint(names.join(", ")),
                sources(place, &[Capability::Exec]),
            ));
        }
    }
    // Each role it may bind to itself, or rewrite, with where.
    let mut roles: Vec<(&str, &transect_graph::edges::Object, Vec<&Where>)> = Vec::new();
    for (_, _, hop, _) in &steps {
        let (title, role, at) = match hop {
            Hop::Binds { role, at } => ("bind to itself", role, at),
            Hop::Rewrites { role, at } => ("rewrite", role, at),
            _ => continue,
        };
        match roles.iter_mut().find(|(t, r, _)| *t == title && *r == role) {
            Some((_, _, places)) if !places.contains(&at) => places.push(at),
            Some(_) => {}
            None => roles.push((title, role, vec![at])),
        }
    }
    for (title, role, at) in roles {
        let line = match title {
            "rewrite" => format!(
                "it may escalate and update this role, which binds it {}",
                places(&at)
            ),
            _ => format!("it may bind the role and create a binding, {}", places(&at)),
        };
        let boxed = SectionBox::new(
            format!(
                "{title}: {}",
                transect_graph::links::kind_and_name_in(role, None)
            ),
            vec![line],
            BoxLook::Plain,
        )
        .with_href(look.href(role));
        found.push(outlined(boxed, sources(place, &[Capability::GrantRoles])));
    }
    found
}

/// How `account` holds `capabilities`, which follow through it: the
/// role it is bound to that grants them, or the next account a step
/// goes through, a box each, each below the one before.
fn chain(
    model: &Model,
    account: &Account,
    capabilities: &[Capability],
    visited: &mut BTreeSet<Account>,
    look: &Look,
    found: &mut Vec<Follow>,
    depth: usize,
) {
    let Some(held) = model.access.of(account) else {
        return;
    };
    let blocks = vec![FOLLOWS; capabilities.len()];
    let everything = held
        .everything
        .iter()
        .find(|(at, _)| *at == Where::Everywhere);
    if let Some((_, role)) = everything {
        let boxed = SectionBox::new(
            transect_graph::links::kind_and_name_in(role, None),
            // The blocks under it do not name a capability on their own,
            // so its lines state which of the six they are.
            vec![
                "every verb, every kind".into(),
                match capabilities.len() == ALL.len() {
                    true => "so each of the six follows".into(),
                    false => format!("so {} follow", listed(capabilities)),
                },
            ],
            BoxLook::Plain,
        )
        .with_href(look.href(role))
        .with_blocks(blocks)
        .with_hint(format!(
            "{} is bound to it in every namespace, so {} follow.",
            account.text(),
            listed(capabilities)
        ));
        found.push(after(boxed, "bound to"));
        return;
    }
    // The roles that grant it what follows, and what follows for it in
    // turn, by the next account.
    let mut roles: Vec<&transect_graph::edges::Object> = Vec::new();
    let mut next: Vec<(&Account, &Hop, Vec<Capability>)> = Vec::new();
    for capability in capabilities {
        let Some(cell) = held.cells.get(capability) else {
            continue;
        };
        for via in cell.granted.values().flatten() {
            if !roles.contains(&&via.role) {
                roles.push(&via.role);
            }
        }
        let hop = cell.follows.values().map(|follow| &follow.hop).next();
        if let Some(hop) = hop
            && let Some(target) = hop.account()
        {
            match next.iter_mut().find(|(t, _, _)| *t == target) {
                Some((_, _, those)) => those.push(*capability),
                None => next.push((target, hop, vec![*capability])),
            }
        }
    }
    if let Some((first, rest)) = roles.split_first() {
        let mut lines = vec![format!("grants {}", listed(capabilities))];
        if !rest.is_empty() {
            let others: Vec<String> = rest
                .iter()
                .map(|role| transect_graph::links::kind_and_name_in(role, None))
                .collect();
            lines.push(format!("and {}", others.join(", ")));
        }
        let boxed = SectionBox::new(
            transect_graph::links::kind_and_name_in(first, None),
            lines,
            BoxLook::Plain,
        )
        .with_href(look.href(first))
        .with_blocks(blocks);
        found.push(after(boxed, "bound to"));
    }
    if depth >= DEEPEST {
        return;
    }
    for (target, hop, those) in next {
        if !visited.insert(target.clone()) {
            continue;
        }
        let label = match hop {
            Hop::RunsAs { .. } => "runs as",
            Hop::ExecToken { .. } => "exec into its pods",
            Hop::Applies { .. } => "applies as",
            Hop::MintsToken { .. } => "creates a token for",
            Hop::Impersonates { .. } => "impersonates",
            _ => "then",
        };
        found.push(Follow {
            placing: Placing {
                boxed: step(target, hop, look),
                class: OUTLINED,
            },
            grants: Vec::new(),
            after: Some(label.into()),
        });
        chain(model, target, &those, visited, look, found, depth + 1);
    }
}

/// The one chain to draw when an account a step reaches is bound to
/// every verb in every namespace: through an applier it may change that
/// applies as such an account, or else through such an account a step
/// reaches, with all that follows through any account.
fn top(
    model: &Model,
    through: &[(Account, Hop, Vec<Capability>)],
) -> Option<(Account, Hop, Vec<Capability>)> {
    let access = &model.access;
    let everything = |account: &Account| {
        let held = access.of(account);
        held.is_some_and(|held| {
            held.everything
                .iter()
                .any(|(at, _)| *at == Where::Everywhere)
        })
    };
    let mut all: Vec<Capability> = Vec::new();
    for (_, _, capabilities) in through {
        for capability in capabilities {
            if !all.contains(capability) {
                all.push(*capability);
            }
        }
    }
    if all.is_empty() {
        return None;
    }
    all.sort();
    // A step through an applier names the applier, so it comes first.
    let applied = through.iter().find_map(|(target, hop, _)| {
        let applies = matches!(hop, Hop::Applies { .. });
        (applies && everything(target)).then(|| (target.clone(), hop.clone()))
    });
    let reached = || {
        let found = through.iter().find(|(target, _, _)| everything(target));
        found.map(|(target, hop, _)| (target.clone(), hop.clone()))
    };
    let (account, hop) = applied.or_else(reached)?;
    Some((account, hop, all))
}

/// The box of an account a step goes through, with the step in words.
fn step(target: &Account, hop: &Hop, look: &Look) -> SectionBox {
    let line = match hop {
        Hop::RunsAs { at, .. } => format!("a pod {} may run as it", at.words()),
        Hop::ExecToken { at, .. } => format!("its pods {} mount its token", at.words()),
        Hop::Applies {
            applier,
            how: AppliesAs::Names,
            ..
        } => format!(
            "{} applies as it",
            transect_graph::links::kind_and_name_in(applier, None)
        ),
        Hop::Applies {
            applier,
            how: AppliesAs::MayName { field } | AppliesAs::Unset { field },
            ..
        } => format!(
            "{} may apply as it, by its {field}",
            transect_graph::links::kind_and_name_in(applier, None)
        ),
        Hop::Applies {
            applier,
            how: AppliesAs::Through { source },
            ..
        } => format!(
            "{} applies as it, from {}",
            transect_graph::links::kind_and_name_in(applier, None),
            transect_graph::links::kind_and_name_in(source, None)
        ),
        Hop::Applies { applier, .. } => {
            format!("applies what {} objects name", applier.kind)
        }
        Hop::MintsToken { at, .. } => format!("it may create its token {}", at.words()),
        Hop::Impersonates { .. } => "it may impersonate it".into(),
        _ => String::new(),
    };
    SectionBox::new(target.text(), vec![line], BoxLook::Plain).with_href(look.account(target))
}

/// The dashed box of an account from which nothing follows, with the
/// six it may do nowhere.
fn nothing(held: &Held) -> Follow {
    let missing: Vec<Capability> = ALL
        .iter()
        .filter(|capability| {
            held.cells
                .get(capability)
                .is_none_or(|cell| cell.is_empty())
        })
        .copied()
        .collect();
    let line = match missing.as_slice() {
        [] => "A binding grants it each of the six that it may do.".to_string(),
        missing if missing.len() == ALL.len() => {
            "It may do none of the six in any namespace.".to_string()
        }
        missing => format!(
            "It may not {} in any namespace.",
            listed(missing)
                .replace(", and ", ", or ")
                .replace(" and ", " or ")
        ),
    };
    Follow {
        placing: Placing {
            boxed: SectionBox::new("nothing follows".into(), vec![line], BoxLook::Plain),
            class: NONE,
        },
        grants: Vec::new(),
        after: None,
    }
}

fn outlined(boxed: SectionBox, grants: Vec<usize>) -> Follow {
    Follow {
        placing: Placing {
            boxed,
            class: OUTLINED,
        },
        grants,
        after: None,
    }
}

fn after(boxed: SectionBox, label: &str) -> Follow {
    Follow {
        placing: Placing {
            boxed,
            class: OUTLINED,
        },
        grants: Vec::new(),
        after: Some(label.into()),
    }
}

/// The grant boxes that show `capabilities`.
fn sources(place: Place, capabilities: &[Capability]) -> Vec<usize> {
    let mut found: Vec<usize> = capabilities.iter().filter_map(|c| place(*c)).collect();
    found.sort();
    found.dedup();
    found
}

/// Where a step holds, in words: "in every namespace", "in shop and
/// ledger".
fn places(places: &[&Where]) -> String {
    if places.contains(&&Where::Everywhere) {
        return "in every namespace".into();
    }
    let names: Vec<String> = places
        .iter()
        .filter_map(|place| match place {
            Where::In(namespace) => Some(namespace.clone()),
            Where::Everywhere => None,
        })
        .collect::<BTreeSet<_>>()
        .into_iter()
        .collect();
    format!("in {}", super::look::and(&names))
}
