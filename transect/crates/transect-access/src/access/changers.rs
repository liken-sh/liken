//! Which accounts may change the objects of each kind, and where.  An
//! account may change a kind when a binding grants it `patch`,
//! `update`, or `delete` on the kind.  `Access::build` indexes these
//! grants once, so a page does not read the RBAC rules again.

use std::collections::{BTreeSet, HashMap};

use k8s_openapi::api::rbac::v1::PolicyRule;

use super::account::{Account, Where};

/// The verbs that change an object that is already there.
const CHANGES: [&str; 3] = ["patch", "update", "delete"];

/// What one rule lets a subject change: a kind by API group and plural,
/// either of which may be `*`, and the object names the rule is limited
/// to.  An empty `names` means every object.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Changed {
    pub group: String,
    pub resource: String,
    pub names: Vec<String>,
}

/// The kinds that `rules` let a subject change.  It skips a rule on a
/// subresource such as `deployments/scale`, because that rule changes
/// only part of an object.
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

/// One account that a binding lets change a kind, the places the
/// binding applies, and the object names it is limited to.  An empty
/// `names` means every object.
#[derive(Clone, Debug)]
struct Grant {
    at: Where,
    names: Vec<String>,
    account: Account,
}

/// The grants that let an account change each kind, keyed by the API
/// group and plural exactly as the rule writes them, so `*` is a key.
#[derive(Debug, Default)]
pub struct Changers {
    by_kind: HashMap<(String, String), Vec<Grant>>,
}

impl Changers {
    /// Records that a binding at `at` lets `account` change `changed`.
    pub fn add(&mut self, changed: &Changed, at: &Where, account: &Account) {
        let key = (changed.group.clone(), changed.resource.clone());
        self.by_kind.entry(key).or_default().push(Grant {
            at: at.clone(),
            names: changed.names.clone(),
            account: account.clone(),
        });
    }

    /// The accounts that a binding lets change the object `name` of the
    /// kind `plural` in `group`, in `namespace`.  A grant whose group or
    /// resource is `*` also matches.
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
