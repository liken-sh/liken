//! Who may change a workload: the accounts a binding grants `patch`,
//! `update`, or `delete` on its own kind in its namespace, with exec
//! into its pods on the same line; the controller of a custom kind; the
//! GitOps objects that apply it and who may change them; the accounts
//! bound to a role with every verb, in one box; and one outlined box for
//! the accounts that may change it by running a pod as an account that
//! may.

use std::collections::{BTreeMap, BTreeSet};

use super::look::{Look, and};
use crate::web::model::Model;
use transect_access::access::drawing::change::{Change, Changer, Target};
use transect_access::access::drawing::{FOLLOWS, PLAIN};
use transect_access::access::{Account, Capability};
use transect_graph::edges::{Endpoint, Kind, Object};
use transect_graph::links::Part;
use transect_map::map::WorkloadContents;
use transect_map::map::section_drawing::{Look as BoxLook, SectionBox};

/// Whether `account` may do `capability` to every object in
/// `namespace` by a grant.  When `follows` is true, a capability that
/// follows from a grant also counts.
fn may(
    model: &Model,
    account: &Account,
    capability: Capability,
    namespace: &str,
    follows: bool,
) -> bool {
    let Some(held) = model.access.of(account) else {
        return false;
    };
    match follows {
        true => held
            .unnamed(capability)
            .iter()
            .any(|at| at.covers(namespace)),
        false => {
            let cell = held.cells.get(&capability);
            let mut granted = cell.into_iter().flat_map(|cell| cell.granted_unnamed());
            granted.any(|at| at.covers(namespace))
        }
    }
}

/// Whether every binding that names `account` is one Kubernetes ships,
/// as its own controllers' are.
fn shipped(model: &Model, account: &Account) -> bool {
    let held = model.access.of(account);
    let bound = held.map(|held| held.bound.as_slice()).unwrap_or_default();
    !bound.is_empty() && bound.iter().all(|bound| bound.defaults)
}

/// What `account` is called in a box, and the page its name opens.
fn named(account: &Account, look: &Look) -> Part {
    let href = look.account(account);
    Part {
        text: account.text(),
        href,
    }
}

pub fn change(model: &Model, object: &Object, contents: &WorkloadContents, look: &Look) -> Change {
    let namespace = object.namespace.clone().unwrap_or_default();
    let key = (object.group.clone(), object.kind.clone());
    let plural = contents.plurals.get(&key).cloned().unwrap_or_default();
    let mut everything: BTreeMap<Account, BTreeSet<String>> = BTreeMap::new();
    for (account, held) in &model.access.accounts {
        let roles = held
            .everything
            .iter()
            .filter(|(at, _)| at.covers(&namespace));
        let roles: BTreeSet<String> = roles.map(|(_, role)| role.name.clone()).collect();
        if !roles.is_empty() {
            everything.insert(account.clone(), roles);
        }
    }
    let direct = model
        .access
        .changers
        .of(&object.group, &plural, &namespace, &object.name);
    let controllers: BTreeSet<Account> = match object.group.as_str() {
        "" | "apps" | "batch" => BTreeSet::new(),
        group => model
            .snapshot
            .grants
            .all_writers(group, &plural)
            .into_iter()
            .map(|(ns, name)| Account::service_account(ns, name))
            .collect(),
    };
    // Each account's lines, by what it may do.
    let mut lines: BTreeMap<Account, Vec<(Target, String)>> = BTreeMap::new();
    for account in direct.iter().filter(|a| !everything.contains_key(*a)) {
        let label = match may(model, account, Capability::Exec, &namespace, false) {
            true => "may change it · exec into its pods",
            false => "may change it",
        };
        lines
            .entry(account.clone())
            .or_default()
            .push((Target::Workload, label.into()));
    }
    let gitops = &model.snapshot.gitops;
    let from = Endpoint::Object(object.clone());
    let appliers: Vec<Object> = contents
        .graph
        .leaving(Kind::ReconcilesFrom, &from)
        .into_iter()
        .filter_map(|end| match end {
            Endpoint::Object(applier) if gitops.detail(applier).is_some_and(|d| d.applies) => {
                Some(applier.clone())
            }
            _ => None,
        })
        .collect();
    for (index, applier) in appliers.iter().enumerate() {
        let at = applier.namespace.as_deref().unwrap_or_default();
        for account in model.access.accounts.keys() {
            if everything.contains_key(account)
                || !may(model, account, Capability::ChangeApplied, at, false)
            {
                continue;
            }
            let entry = lines.entry(account.clone()).or_default();
            entry.insert(0, (Target::Applier(index), "may patch".into()));
        }
    }
    // Changing it follows for an account that may run a pod as an
    // account that may change it.
    let through: BTreeSet<String> = lines
        .keys()
        .chain(&controllers)
        .filter_map(|account| match account {
            Account::ServiceAccount { namespace, .. } => Some(namespace.clone()),
            _ => None,
        })
        .collect();
    let follows: Vec<Account> = model
        .access
        .accounts
        .keys()
        .filter(|account| !lines.contains_key(*account) && !everything.contains_key(*account))
        .filter(|account| {
            through.iter().any(|ns| {
                may(model, account, Capability::RunPods, ns, true)
                    || may(model, account, Capability::ChangeWorkloads, ns, true)
            })
        })
        .cloned()
        .collect();
    let mut changers: Vec<Changer> = Vec::new();
    let why = |account: &Account| {
        let object = account.object()?;
        model.snapshot.intents.of(&object)?.why.clone()
    };
    // An account with a sentence of its own, or the controller, gets a
    // box of its own; the others share a box with the accounts whose
    // lines read the same.
    let mut shared: BTreeMap<Vec<(Target, String)>, Vec<Account>> = BTreeMap::new();
    let mut own: Vec<Account> = Vec::new();
    for (account, its) in &lines {
        let part = named(account, look);
        if shipped(model, account) {
            own.push(account.clone());
        } else if let Some(line) = why(account) {
            let boxed = SectionBox::new(part.text, vec![line.text], BoxLook::Plain)
                .with_href(part.href)
                .marked(line.provenance);
            changers.push(Changer {
                boxed,
                class: PLAIN,
                lines: its.clone(),
            });
        } else if controllers.contains(account) {
            let boxed = SectionBox::new(
                part.text,
                vec!["the controller that reconciles it".into()],
                BoxLook::Plain,
            )
            .with_href(part.href);
            changers.push(Changer {
                boxed,
                class: PLAIN,
                lines: its.clone(),
            });
        } else {
            shared.entry(its.clone()).or_default().push(account.clone());
        }
    }
    if !everything.is_empty() {
        let roles: BTreeSet<&String> = everything.values().flatten().collect();
        let role = match roles.into_iter().collect::<Vec<_>>().as_slice() {
            [one] => (*one).clone(),
            _ => "a role with every verb".into(),
        };
        let (accounts, others): (Vec<&Account>, Vec<&Account>) = everything
            .keys()
            .partition(|a| matches!(a, Account::ServiceAccount { .. }));
        let title = match accounts.len() {
            1 => format!("1 account bound to {role}"),
            count => format!("{count} accounts bound to {role}"),
        };
        let lines = match others.as_slice() {
            [] => Vec::new(),
            others => {
                let names: Vec<String> = others.iter().map(|a| a.text()).collect();
                vec![format!("and {}", and(&names))]
            }
        };
        let names: Vec<String> = everything.keys().map(Account::text).collect();
        let boxed = SectionBox::new(title, lines, BoxLook::Plain)
            .with_href(Some("/access/accounts".into()))
            .with_hint(names.join(", "));
        changers.push(Changer {
            boxed,
            class: PLAIN,
            lines: vec![(Target::Workload, "every verb".into())],
        });
    }
    for (its, accounts) in shared {
        let boxed = match accounts.as_slice() {
            [one] => {
                let part = named(one, look);
                SectionBox::new(part.text, Vec::new(), BoxLook::Plain).with_href(part.href)
            }
            many => {
                let parts: Vec<Part> = many
                    .iter()
                    .map(|a| Part {
                        text: a.short(),
                        ..named(a, look)
                    })
                    .collect();
                let names: Vec<String> = many.iter().map(Account::text).collect();
                SectionBox::new(
                    format!("{} accounts", many.len()),
                    vec![String::new()],
                    BoxLook::Plain,
                )
                .with_names(parts)
                .with_hint(names.join(", "))
            }
        };
        changers.push(Changer {
            boxed,
            class: PLAIN,
            lines: its,
        });
    }
    if !own.is_empty() {
        // Kubernetes' own components and controllers fold into one box,
        // as on the accounts matrix.
        let parts: Vec<Part> = own
            .iter()
            .map(|a| Part {
                text: a.short(),
                ..named(a, look)
            })
            .collect();
        let names: Vec<String> = own.iter().map(Account::text).collect();
        let title = format!(
            "{} of Kubernetes' own components and controllers",
            own.len()
        );
        let boxed = SectionBox::new(title, vec![String::new()], BoxLook::Plain)
            .with_names(parts)
            .with_hint(names.join(", "));
        let lines = vec![(Target::Workload, "may change it".into())];
        changers.push(Changer {
            boxed,
            class: PLAIN,
            lines,
        });
    }
    if !follows.is_empty() {
        let names: Vec<String> = follows.iter().map(Account::text).collect();
        let title = match follows.len() {
            1 => "1 account, by running as another".to_string(),
            count => format!("{count} accounts, by running as another"),
        };
        let boxed = SectionBox::new(
            title,
            vec!["each may run a pod as an account that may change it".into()],
            BoxLook::Plain,
        )
        .with_href(look.namespace(&namespace))
        .with_hint(names.join(", "));
        changers.push(Changer {
            boxed,
            class: FOLLOWS,
            lines: vec![(Target::Workload, "follows".into())],
        });
    }
    let appliers = appliers
        .iter()
        .map(|applier| {
            let path = gitops.detail(applier).and_then(|d| d.path);
            let line = match path {
                Some(path) => format!("applies {path}"),
                None => "applies it".into(),
            };
            let title = transect_graph::links::kind_and_name_in(applier, Some(&namespace));
            SectionBox::new(title, vec![line], BoxLook::Plain).with_href(look.href(applier))
        })
        .collect();
    // The workload is the page, so its box opens nothing, and its name
    // drops the page's namespace.
    let workload = SectionBox::new(
        transect_graph::links::kind_and_name_in(object, Some(&namespace)),
        Vec::new(),
        BoxLook::Workload,
    );
    Change {
        changers,
        appliers,
        workload,
    }
}
