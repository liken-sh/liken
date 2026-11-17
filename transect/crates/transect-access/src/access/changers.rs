//! Who may change the objects of each kind, and where.  These are the
//! subjects that a binding grants `patch`, `update`, or `delete` on the
//! kind.  `Access::build` indexes them once per build, so a page looks
//! them up without reading RBAC again.

use std::collections::{BTreeSet, HashMap};

use k8s_openapi::api::rbac::v1::PolicyRule;

use super::account::{Account, Where};

/// The verbs that change an object that is already there.
const CHANGES: [&str; 3] = ["patch", "update", "delete"];

/// What one rule lets a subject change: a kind by API group and plural,
/// either of which may be `*`, and the names it is limited to, or none
/// for every object.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Changed {
    pub group: String,
    pub resource: String,
    pub names: Vec<String>,
}

/// The kinds `rules` let a subject change.  A subresource such as
/// `deployments/scale` changes only that part, so this function skips
/// it.
pub fn changed(rules: &[PolicyRule]) -> Vec<Changed> {
    let mut found = Vec::new();
    for rule in rules {
        let changes = rule
            .verbs
            .iter()
            .any(|verb| verb == "*" || CHANGES.contains(&verb.as_str()));
        if !changes {
            continue;
        }
        let names = rule.resource_names.clone().unwrap_or_default();
        for group in rule.api_groups.iter().flatten() {
            for resource in rule.resources.iter().flatten() {
                if resource.contains('/') {
                    continue;
                }
                found.push(Changed {
                    group: group.clone(),
                    resource: resource.clone(),
                    names: names.clone(),
                });
            }
        }
    }
    found
}

/// One subject a binding lets change a kind, where, and by which names.
#[derive(Clone, Debug)]
struct Grant {
    at: Where,
    names: Vec<String>,
    account: Account,
}

/// Every subject a binding lets change each kind, by the kind as the
/// rule names it.
#[derive(Debug, Default)]
pub struct Changers {
    by_kind: HashMap<(String, String), Vec<Grant>>,
}

impl Changers {
    /// Records that a binding at `at` lets `account` change what
    /// `changed` names.
    pub fn add(&mut self, changed: &Changed, at: &Where, account: &Account) {
        let key = (changed.group.clone(), changed.resource.clone());
        self.by_kind.entry(key).or_default().push(Grant {
            at: at.clone(),
            names: changed.names.clone(),
            account: account.clone(),
        });
    }

    /// The subjects a binding lets change the object `name` of `plural`
    /// in `group`, in `namespace`.
    pub fn of(&self, group: &str, plural: &str, namespace: &str, name: &str) -> BTreeSet<Account> {
        let keys = [(group, plural), (group, "*"), ("*", plural), ("*", "*")];
        let grants = keys.iter().flat_map(|(group, resource)| {
            self.by_kind
                .get(&(group.to_string(), resource.to_string()))
                .into_iter()
                .flatten()
        });
        grants
            .filter(|grant| grant.at.covers(namespace))
            .filter(|grant| grant.names.is_empty() || grant.names.iter().any(|n| n == name))
            .map(|grant| grant.account.clone())
            .collect()
    }
}
