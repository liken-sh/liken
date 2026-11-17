//! What follows from the grants: the steps by which an account may do
//! what no binding grants it.  [`close`] repeats the steps until a pass
//! finds nothing new.
//!
//! - An account that may run pods or change workloads in a namespace
//!   may run a pod as any ServiceAccount there, so it may do what that
//!   account may.  A pod it starts may also mount any Secret there.
//! - An account that may exec into a pod may read the Secrets the pod
//!   mounts, by name, and act as the pod's ServiceAccount through the
//!   token the pod mounts, when it mounts one.
//! - An account that may change an object that applies may do what
//!   the account the object applies as may: the ServiceAccount the
//!   object names, or its controller's.  When the object may name its
//!   account, the account that changes it may also do what every
//!   ServiceAccount in the object's namespace may, and what the object
//!   applies as when it names none.  An account that may change the
//!   source an applier fetches through, in any namespace, may do what
//!   the applier's account may.
//! - An account that may bind a role and create a binding that may
//!   name it may bind the role to itself, so it may do what the role
//!   grants there.  An account that may escalate and update a role it
//!   is bound to may rewrite the role to grant anything.
//! - An account that may create a ServiceAccount's token, or
//!   impersonate an account, may do what that account may.  One that
//!   may impersonate Group `system:masters`, with a user to impersonate
//!   beside it, may do everything everywhere, because the API server's
//!   authorizer lets that group do anything, bound or not.
//!
//! Each capability records the first hop of its chain, so a capability
//! reached in two hops names the first hop.  A cycle ends when a pass
//! finds nothing new.

use std::collections::{BTreeMap, BTreeSet};

use super::account::{Account, Hop, Where};
use super::capability::Capability;
use super::grants::{self, RoleGrants};
use super::{Applier, Follow, Held, Inputs, Running};
use transect_graph::edges::Object;

mod appliers;
mod routes;

/// What one account may reach in one pass: the accounts it may act as,
/// and the capabilities that follow for it alone.
struct Steps {
    acts_as: Vec<(Account, Hop)>,
    own: Vec<(Capability, Where, Follow)>,
}

/// What one pass finds for one account.
struct Found {
    account: Account,
    follows: Vec<(Capability, Where, Follow)>,
}

/// Adds every capability that follows to `held`, as a follow.  Each
/// pass reads what the pass before it left, and adds what it found only
/// after it has read every account.  So the first pass finds what one
/// hop gives, the second what two hops give, and each capability keeps
/// the first hop of a shortest chain.
pub fn close(held: &mut BTreeMap<Account, Held>, inputs: &Inputs) {
    let places = Places::read(inputs, held);
    loop {
        let found: Vec<Found> = held
            .iter()
            .map(|(account, mine)| find(account, mine, held, &places))
            .collect();
        let mut changed = false;
        for Found { account, follows } in found {
            let mine = held.get_mut(&account).expect("every account is held");
            for (capability, place, follow) in follows {
                let cell = mine.cells.entry(capability).or_default();
                changed |= cell.follow(place, follow);
            }
        }
        if !changed {
            return;
        }
    }
}

/// What `account`, which holds `mine`, finds in one pass over `held`:
/// what each account it may act as holds that it does not.
fn find(account: &Account, mine: &Held, held: &BTreeMap<Account, Held>, places: &Places) -> Found {
    let mut steps = steps(mine, places);
    let mut follows: Vec<(Capability, Where, Follow)> = Vec::new();
    let mut seen: BTreeSet<&Account> = BTreeSet::new();
    for (target, hop) in &steps.acts_as {
        if target == account || !seen.insert(target) {
            continue;
        }
        let Some(other) = held.get(target) else {
            continue;
        };
        for (capability, cell) in &other.cells {
            let have = mine.cells.get(capability);
            for place in cell.unnamed_places() {
                if have.is_some_and(|have| have.holds(&place)) {
                    continue;
                }
                let follow = Follow {
                    hop: hop.clone(),
                    names: Vec::new(),
                };
                follows.push((*capability, place, follow));
            }
        }
    }
    // The capabilities of the accounts it acts as come first, so a
    // capability that applies everywhere through one of them is not also
    // listed in single namespaces through the account's own steps.
    // Within each group, every namespace sorts before one namespace, so
    // a single namespace is not listed beside every namespace.
    follows.sort_by(|a, b| a.1.cmp(&b.1));
    steps.own.sort_by(|a, b| a.1.cmp(&b.1));
    follows.extend(steps.own);
    Found {
        account: account.clone(),
        follows,
    }
}

/// What the pods and ServiceAccounts of each namespace give an account
/// that reaches it.
struct Places {
    /// Every namespace that holds a ServiceAccount or a pod.
    namespaces: BTreeSet<String>,
    accounts: BTreeMap<String, Vec<Account>>,
    /// The ServiceAccounts whose token a pod in the namespace mounts.
    tokens: BTreeMap<String, BTreeSet<Account>>,
    /// The Secrets the pods in the namespace mount, by name.
    secrets: BTreeMap<String, BTreeSet<String>>,
    appliers: Vec<Applier>,
    /// The pods of each namespace, for a grant that names them.
    pods: BTreeMap<String, Vec<Running>>,
    /// The users and groups a binding names.
    users: Vec<Account>,
    groups: Vec<Account>,
    /// Every role, with what it grants: those with every verb on every
    /// kind first, then by name, so a step that binds a role names the
    /// widest one.
    roles: Vec<(Object, RoleGrants)>,
}

impl Places {
    fn read(inputs: &Inputs, held: &BTreeMap<Account, Held>) -> Self {
        let mut places = Self {
            namespaces: BTreeSet::new(),
            accounts: BTreeMap::new(),
            tokens: BTreeMap::new(),
            secrets: BTreeMap::new(),
            appliers: Vec::new(),
            pods: BTreeMap::new(),
            users: Vec::new(),
            groups: Vec::new(),
            roles: grants::roles(inputs).into_iter().collect(),
        };
        let widest = |(role, grants): &(Object, RoleGrants)| (!grants.everything, role.clone());
        places.roles.sort_by_key(widest);
        for account in held.keys() {
            match account {
                Account::ServiceAccount { namespace, .. } => {
                    places.namespaces.insert(namespace.clone());
                    let here = places.accounts.entry(namespace.clone()).or_default();
                    here.push(account.clone());
                }
                Account::User(_) => places.users.push(account.clone()),
                Account::Group(_) => places.groups.push(account.clone()),
            }
        }
        for pod in &inputs.pods {
            let here = places.pods.entry(pod.namespace.clone()).or_default();
            here.push(pod.clone());
            places.namespaces.insert(pod.namespace.clone());
            if pod.token {
                let account = Account::service_account(&pod.namespace, &pod.account);
                let here = places.tokens.entry(pod.namespace.clone()).or_default();
                here.insert(account);
            }
            let here = places.secrets.entry(pod.namespace.clone()).or_default();
            here.extend(pod.secrets.iter().cloned());
        }
        places.appliers = inputs.appliers.clone();
        places
    }

    /// The namespaces `place` covers.
    fn within<'a>(&'a self, place: &'a Where) -> impl Iterator<Item = &'a String> + 'a {
        self.namespaces
            .iter()
            .filter(move |namespace| place.covers(namespace))
    }
}

/// Every step that an account with `held` may take from what it may do
/// now.
fn steps(held: &Held, places: &Places) -> Steps {
    let mut steps = Steps {
        acts_as: Vec::new(),
        own: Vec::new(),
    };
    for basis in [Capability::RunPods, Capability::ChangeWorkloads] {
        for at in held.unnamed(basis) {
            for namespace in places.within(&at) {
                let here = places.accounts.get(namespace).into_iter().flatten();
                for account in here {
                    let hop = Hop::RunsAs {
                        account: account.clone(),
                        basis,
                        at: Where::In(namespace.clone()),
                    };
                    steps
                        .acts_as
                        .push((account.clone(), first(held, basis, &at, hop)));
                }
            }
            let follow = Follow {
                hop: first(
                    held,
                    basis,
                    &at,
                    Hop::StartsPod {
                        basis,
                        at: at.clone(),
                    },
                ),
                names: Vec::new(),
            };
            steps.own.push((Capability::ReadSecrets, at, follow));
        }
    }
    for at in held.unnamed(Capability::Exec) {
        for namespace in places.within(&at) {
            for account in places.tokens.get(namespace).into_iter().flatten() {
                let hop = Hop::ExecToken {
                    account: account.clone(),
                    at: Where::In(namespace.clone()),
                };
                steps
                    .acts_as
                    .push((account.clone(), first(held, Capability::Exec, &at, hop)));
            }
            let secrets = places.secrets.get(namespace).into_iter().flatten();
            let names: Vec<String> = secrets.cloned().collect();
            if !names.is_empty() {
                let hop = Hop::ExecMounts { at: at.clone() };
                let follow = Follow {
                    hop: first(held, Capability::Exec, &at, hop),
                    names,
                };
                steps.own.push((
                    Capability::ReadSecrets,
                    Where::In(namespace.clone()),
                    follow,
                ));
            }
        }
    }
    let writes = held.writes.iter();
    let writes = writes.map(|((kind, at), names)| (kind.clone(), at.clone(), names.clone()));
    appliers::applies_through(&writes.collect::<Vec<_>>(), None, places, &mut steps);
    let direct = held.routes.iter();
    let direct = direct.map(|((route, at), names)| (*route, at.clone(), names.clone()));
    routes::acts_through(&direct.collect::<Vec<_>>(), None, places, &mut steps);
    routes::binds(held, places, &mut steps);
    routes::rewrites(held, &mut steps);
    routes::execs_named(held, places, &mut steps);
    steps
}

/// The first hop of a step that `basis` at `at` takes as `hop`: `hop`
/// itself when a binding grants the basis there, and the first hop of
/// the basis when it follows from another step.
fn first(held: &Held, basis: Capability, at: &Where, hop: Hop) -> Hop {
    let Some(cell) = held.cells.get(&basis) else {
        return hop;
    };
    if cell.granted_unnamed().any(|granted| granted == at) {
        return hop;
    }
    match cell.follows.get(at) {
        Some(follow) if follow.names.is_empty() => follow.hop.clone(),
        _ => hop,
    }
}
