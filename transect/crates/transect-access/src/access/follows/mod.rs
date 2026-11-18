//! The capabilities that an account gets without a binding that grants
//! them, by acting as another account or by changing a role.  [`close`]
//! adds them to each account's `Held`.  An account gets a capability in
//! these ways:
//!
//! - It may run pods or change workloads in a namespace.  It can start
//!   a pod that runs as any ServiceAccount there, so it may do what
//!   that ServiceAccount may.  A pod it starts can also mount any
//!   Secret in the namespace, so it may read them.
//! - It may exec into pods.  It may read the Secrets that those pods
//!   mount, by the Secrets' names.  When a pod mounts a ServiceAccount
//!   token, it can use that token to act as the ServiceAccount.
//! - It may change an object that applies other objects, such as a Flux
//!   `Kustomization`, so it may do what the object applies as: the
//!   ServiceAccount that the object names, or the account of the kind's
//!   controller when the object names none.  When the kind has a field
//!   that names the ServiceAccount, the account can set it to any
//!   ServiceAccount in the object's namespace, or unset it.  An account
//!   that may change the object's source, in any namespace, may also do
//!   what the object applies as.
//! - It may bind a role and create a binding that names the role.  It
//!   can bind the role to itself, so it may do what the role grants
//!   there.  When it may escalate and update a role that it is bound
//!   to, it can rewrite the role to grant anything.
//! - It may create a token for a ServiceAccount, or impersonate an
//!   account, so it may do what that account may.  When it may
//!   impersonate the group `system:masters` and a user beside it, it
//!   may do everything everywhere, because the API server's authorizer
//!   allows that group every request, with or without a binding.
//!
//! Each added capability records the first hop of the chain that
//! reached it.

use std::collections::{BTreeMap, BTreeSet};

use super::account::{Account, Hop, Where};
use super::capability::Capability;
use super::grants::{self, RoleGrants};
use super::{Applier, Follow, Held, Inputs, Running};
use transect_graph::edges::Object;

mod appliers;
mod routes;

/// The accounts that one account may act as, and the capabilities it
/// gets without acting as another account.
struct Steps {
    acts_as: Vec<(Account, Hop)>,
    own: Vec<(Capability, Where, Follow)>,
}

struct Found {
    account: Account,
    follows: Vec<(Capability, Where, Follow)>,
}

/// Adds to `held` every capability that an account gets by a chain of
/// steps, and returns when a pass adds nothing, which also ends a cycle
/// of accounts.  Each pass reads every account before it writes any, so
/// pass n finds the chains of n hops, and a capability keeps the first
/// hop of a shortest chain.
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

/// The capabilities that `account`, which has `mine`, gets in one pass:
/// those of each account it may act as, and those of its own steps.
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
    // `close` skips a capability in a namespace when the account
    // already has it everywhere.  So each list puts `Where::Everywhere`
    // before `Where::In`, and a capability that applies everywhere is
    // not also listed for single namespaces.
    follows.sort_by(|a, b| a.1.cmp(&b.1));
    steps.own.sort_by(|a, b| a.1.cmp(&b.1));
    follows.extend(steps.own);
    Found {
        account: account.clone(),
        follows,
    }
}

/// The pods, accounts, appliers, and roles that the steps look up.
struct Places {
    /// Every namespace that has a ServiceAccount or a pod.
    namespaces: BTreeSet<String>,
    accounts: BTreeMap<String, Vec<Account>>,
    /// The ServiceAccounts whose token a pod in the namespace mounts.
    tokens: BTreeMap<String, BTreeSet<Account>>,
    /// The Secrets the pods in the namespace mount, by name.
    secrets: BTreeMap<String, BTreeSet<String>>,
    appliers: Vec<Applier>,
    /// The pods of each namespace, for a grant limited to pods by name.
    pods: BTreeMap<String, Vec<Running>>,
    /// The users and groups a binding names.
    users: Vec<Account>,
    groups: Vec<Account>,
    /// Every role with what it grants.  The roles that allow every verb
    /// on every resource sort first, because a step that binds a role
    /// takes the first role that fits.
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

    /// The namespaces in which a grant at `place` applies.
    fn within<'a>(&'a self, place: &'a Where) -> impl Iterator<Item = &'a String> + 'a {
        self.namespaces
            .iter()
            .filter(move |namespace| place.covers(namespace))
    }
}

/// Every step that an account with `held` can take.
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

/// The hop to record for a step that depends on the capability `basis`
/// at `at`.  It is `hop` when a binding grants `basis` there, and the
/// first hop of `basis` when `basis` came from an earlier step.
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
