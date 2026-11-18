//! The "who may change this workload" section of the access tab. It
//! lists the accounts that a binding lets `patch`, `update`, or
//! `delete` the workload's kind in its namespace (with `exec` into its
//! pods on the same line), the controller of a custom kind, the GitOps
//! objects that apply the workload and the accounts that may change
//! those, and the accounts bound to a role with every verb, which share
//! one box. A last box, drawn with an outline, holds the accounts that
//! can change the workload only by running a pod as an account that
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

/// Whether `account` has `capability` on every object of `namespace`,
/// through a grant that names no particular resource. When `follows` is
/// true, a capability that follows from a grant also counts.
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

/// Whether `account` has bindings, and every one is a default binding
/// that Kubernetes creates itself, like those of its own controllers.
fn shipped(model: &Model, account: &Account) -> bool {
    let held = model.access.of(account);
    let bound = held.map(|held| held.bound.as_slice()).unwrap_or_default();
    !bound.is_empty() && bound.iter().all(|bound| bound.defaults)
}

/// The text and link that name `account` in a box.
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
    let mut all_verbs: BTreeMap<Account, BTreeSet<String>> = BTreeMap::new();
    for (account, held) in &model.access.accounts {
        let roles = held
            .everything
            .iter()
            .filter(|(at, _)| at.covers(&namespace));
        let roles: BTreeSet<String> = roles.map(|(_, role)| role.name.clone()).collect();
        if !roles.is_empty() {
            all_verbs.insert(account.clone(), roles);
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
    // The lines of each account's box, by what the account may do.
    let mut lines: BTreeMap<Account, Vec<(Target, String)>> = BTreeMap::new();
    for account in direct.iter().filter(|a| !all_verbs.contains_key(*a)) {
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
            if all_verbs.contains_key(account)
                || !may(model, account, Capability::ChangeApplied, at, false)
            {
                continue;
            }
            let entry = lines.entry(account.clone()).or_default();
            entry.insert(0, (Target::Applier(index), "may patch".into()));
        }
    }
    // An account with no line can still change the workload by running
    // a pod as a listed service account. `follows` holds the accounts
    // that may run pods or change workloads in the namespace of one.
    let service_account_namespaces: BTreeSet<String> = lines
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
        .filter(|account| !lines.contains_key(*account) && !all_verbs.contains_key(*account))
        .filter(|account| {
            service_account_namespaces.iter().any(|ns| {
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
    // An account that Kubernetes ships goes in the combined box below.
    // An account with an intent sentence, and the controller, each get a
    // box. Every other account shares a box with the accounts that have
    // the same lines.
    let mut shared: BTreeMap<Vec<(Target, String)>, Vec<Account>> = BTreeMap::new();
    let mut built_in: Vec<Account> = Vec::new();
    for (account, its) in &lines {
        let part = named(account, look);
        if shipped(model, account) {
            built_in.push(account.clone());
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
    if !all_verbs.is_empty() {
        let roles: BTreeSet<&String> = all_verbs.values().flatten().collect();
        let role = match roles.into_iter().collect::<Vec<_>>().as_slice() {
            [one] => (*one).clone(),
            _ => "a role with every verb".into(),
        };
        let (accounts, others): (Vec<&Account>, Vec<&Account>) = all_verbs
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
        let names: Vec<String> = all_verbs.keys().map(Account::text).collect();
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
    if !built_in.is_empty() {
        let parts: Vec<Part> = built_in
            .iter()
            .map(|a| Part {
                text: a.short(),
                ..named(a, look)
            })
            .collect();
        let names: Vec<String> = built_in.iter().map(Account::text).collect();
        let title = format!(
            "{} of Kubernetes' own components and controllers",
            built_in.len()
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
    // The page is this workload's, so its box has no link, and its
    // title leaves out the namespace.
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
