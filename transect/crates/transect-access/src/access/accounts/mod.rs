//! The `/access/accounts` page: a row for each account that has any of
//! the six capabilities, from a binding or by following from another
//! grant.  The rows are in runs, each under a heading.  The first run
//! is the accounts bound to a role with every verb on every resource.
//! Next is a run for each first hop by which accounts get the same
//! power through another account, and last a run of every other
//! account.  Accounts whose bindings are all ones that Kubernetes ships
//! are in a collapsed table below the runs.  Gray lines count the
//! accounts bound only to roles that grant none of the six, and the
//! ServiceAccounts that no binding names.

mod cells;
mod here;
mod words;

use std::collections::{BTreeMap, BTreeSet};

use askama::Template;

use super::account::{Account, Hop, Where};
use super::capability::ALL;
use super::grants::EVERYONE;
use super::{Access, Capability, Held};
pub use cells::{Look, Named, Row};
pub use here::rows as namespace_rows;
use transect_frame::frame::crumbs::Crumb;
use transect_graph::links::Part;

/// One column: the capability's name, and its verbs on hover.
#[derive(Debug, PartialEq)]
pub struct Column {
    pub name: &'static str,
    pub verbs: String,
}

/// Rows under one heading, which says what the rows have in common.
#[derive(Debug, PartialEq)]
pub struct Run {
    pub heading: Vec<Part>,
    pub rows: Vec<Row>,
}

/// A gray line that counts accounts, with their names on hover.
#[derive(Debug, PartialEq)]
pub struct Counted {
    pub text: String,
    pub names: String,
}

#[derive(Template)]
#[template(path = "access/accounts/accounts.html", blocks = ["main"])]
pub struct AccountsPage {
    pub columns: Vec<Column>,
    pub runs: Vec<Run>,
    /// The accounts whose every binding is one that Kubernetes ships.
    pub controllers: Vec<Row>,
    /// The line that says whether the groups that every account belongs
    /// to have any of the six.
    pub everyone: String,
    pub none: Option<Counted>,
    pub unbound: Option<Counted>,
    /// Whether `Access::gitops` is empty, so the sixth column has no
    /// kinds to show.
    pub no_gitops: bool,
}

impl AccountsPage {
    pub fn build(access: &Access, look: &Look) -> Self {
        let runs = Runs::sort(access);
        let rows = |accounts: &[&Account], heading: Option<&Hop>| {
            let row = |a: &&Account| cells::row(access, a, &access.accounts[*a], heading, look);
            accounts.iter().map(row).collect::<Vec<_>>()
        };
        let mut built: Vec<Run> = Vec::new();
        if !runs.everything.is_empty() {
            let mut heading = vec![Part::plain("Bound to ")];
            heading.extend(listed(
                runs.roles
                    .iter()
                    .map(|role| look_role(role, look))
                    .collect(),
            ));
            built.push(Run {
                heading,
                rows: rows(&runs.everything, None),
            });
        }
        // A row in one of these runs shows its own hop only when the hop
        // differs from the one in the run's heading.
        for (hop, accounts) in &runs.through {
            built.push(Run {
                heading: heading(hop, access, look),
                rows: rows(accounts, Some(hop)),
            });
        }
        if !runs.other.is_empty() {
            built.push(Run {
                heading: vec![Part::plain("Every other account granted one of the six")],
                rows: rows(&runs.other, None),
            });
        }
        let columns = ALL
            .iter()
            .map(|capability| Column {
                name: capability.words(),
                verbs: capability.verbs(&access.gitops),
            })
            .collect();
        Self {
            columns,
            runs: built,
            controllers: rows(&runs.controllers, None),
            everyone: everyone(access),
            none: counted(
                &runs.none,
                "account is",
                "accounts are",
                "bound to roles that grant none of the six",
            ),
            unbound: counted(
                &runs.unbound,
                "ServiceAccount is",
                "ServiceAccounts are",
                "named by no binding",
            ),
            no_gitops: access.gitops.is_empty(),
        }
    }

    /// The number of rows in all the runs.  It leaves out `controllers`.
    pub fn count(&self) -> usize {
        self.runs.iter().map(|run| run.rows.len()).sum()
    }

    pub fn crumbs(&self) -> Vec<Crumb> {
        vec![
            Crumb::home(),
            Crumb::link("access", "/access".into()),
            Crumb::current("accounts"),
        ]
    }

    pub fn events_url(&self) -> String {
        "/events/access/accounts".into()
    }
}

/// The accounts of each run, and the roles that the first run is bound
/// to.
struct Runs<'a> {
    everything: Vec<&'a Account>,
    roles: BTreeSet<transect_graph::edges::Object>,
    /// The accounts that can do the first five capabilities in every
    /// namespace by a follow, keyed by the first hop toward an account
    /// bound to everything.
    through: BTreeMap<Hop, Vec<&'a Account>>,
    other: Vec<&'a Account>,
    controllers: Vec<&'a Account>,
    none: Vec<&'a Account>,
    unbound: Vec<&'a Account>,
}

impl<'a> Runs<'a> {
    fn sort(access: &'a Access) -> Self {
        let mut runs = Self {
            everything: Vec::new(),
            roles: BTreeSet::new(),
            through: BTreeMap::new(),
            other: Vec::new(),
            controllers: Vec::new(),
            none: Vec::new(),
            unbound: Vec::new(),
        };
        let bound_everywhere = |held: &Held| {
            let roles = held.everything.iter();
            roles
                .filter(|(at, _)| *at == Where::Everywhere)
                .map(|(_, role)| role.clone())
                .collect::<Vec<_>>()
        };
        let top: BTreeSet<&Account> = access
            .accounts
            .iter()
            .filter(|(_, held)| !bound_everywhere(held).is_empty())
            .map(|(account, _)| account)
            .collect();
        for (account, held) in &access.accounts {
            let shipped = !held.bound.is_empty() && held.bound.iter().all(|bound| bound.defaults);
            if !held.holds_any() {
                match held.bound.is_empty() {
                    true if matches!(account, Account::ServiceAccount { .. }) => {
                        runs.unbound.push(account)
                    }
                    true => {}
                    false => runs.none.push(account),
                }
                continue;
            }
            if shipped {
                runs.controllers.push(account);
            } else if top.contains(account) {
                runs.everything.push(account);
                runs.roles.extend(bound_everywhere(held));
            } else if let Some(hop) = toward(held, &top) {
                runs.through.entry(hop).or_default().push(account);
            } else {
                runs.other.push(account);
            }
        }
        runs
    }
}

/// The hop that a run's heading names for `held`, or `None` unless
/// `held` has each of the first five capabilities of `ALL` in every
/// namespace.  When a binding lets it run pods or change workloads in
/// the namespace of a ServiceAccount in `top`, the hop is running as
/// that account.  Otherwise it is the hop of one of the five that no
/// binding grants everywhere: the first such hop that leads to an
/// account in `top`, or else the last one.
///
/// Every `Hop::RunsAs` it returns has the basis `RunPods`, so the
/// accounts that run pods and the accounts that change workloads in one
/// namespace share one run, whose heading names both.
fn toward(held: &Held, top: &BTreeSet<&Account>) -> Option<Hop> {
    let five = &ALL[..5];
    let everywhere =
        |capability: &Capability| held.unnamed(*capability).contains(&Where::Everywhere);
    if !five.iter().all(everywhere) {
        return None;
    }
    // Only a grant from a binding counts here.  A capability that
    // follows from another hop is handled after this loop.
    for basis in [Capability::RunPods, Capability::ChangeWorkloads] {
        let granted = held.cells.get(&basis).into_iter();
        for at in granted.flat_map(|cell| cell.granted_unnamed()) {
            let beside = top.iter().find(|account| match account {
                Account::ServiceAccount { namespace, .. } => at.covers(namespace),
                _ => false,
            });
            if let Some(Account::ServiceAccount { namespace, .. }) = beside {
                return Some(Hop::RunsAs {
                    account: (*beside?).clone(),
                    basis: Capability::RunPods,
                    at: Where::In(namespace.clone()),
                });
            }
        }
    }
    let hops = five.iter().filter_map(|capability| {
        let cell = held.cells.get(capability)?;
        let granted = cell.granted_unnamed().any(|at| *at == Where::Everywhere);
        let follow = cell.follows.get(&Where::Everywhere).filter(|_| !granted)?;
        Some(&follow.hop)
    });
    let hops: Vec<&Hop> = hops.collect();
    let into_top = hops
        .iter()
        .find(|hop| hop.account().is_some_and(|a| top.contains(a)));
    into_top.or(hops.last()).map(|hop| match hop {
        Hop::RunsAs { account, at, .. } => Hop::RunsAs {
            account: account.clone(),
            basis: Capability::RunPods,
            at: at.clone(),
        },
        other => (*other).clone(),
    })
}

/// A run's heading for the accounts that go through `hop`.
fn heading(hop: &Hop, access: &Access, look: &Look) -> Vec<Part> {
    let part = |named: Named| Part {
        text: named.text,
        href: named.href,
    };
    let namespace = |at: &Where| match at {
        Where::In(namespace) => Some(Part {
            text: format!("Namespace {namespace}"),
            href: (look.href)(&transect_graph::edges::Object::new(
                "",
                "Namespace",
                None,
                namespace,
            )),
        }),
        Where::Everywhere => None,
    };
    let mut parts: Vec<Part> = Vec::new();
    match hop {
        Hop::RunsAs { account, at, .. } => {
            parts.push(Part::plain("May run pods or change workloads in "));
            parts.extend(namespace(at));
            parts.push(Part::plain(", and so may run as "));
            parts.push(part(look.named(account)));
        }
        Hop::ExecToken { account, at } => {
            parts.push(Part::plain("May exec into pods in "));
            parts.extend(namespace(at));
            parts.push(Part::plain(" that run as "));
            parts.push(part(look.named(account)));
            parts.push(Part::plain(" and mount its token"));
        }
        Hop::Applies { account, .. } => {
            parts.push(Part::plain("May change what "));
            parts.push(part(look.named(account)));
            parts.push(Part::plain(" applies"));
        }
        Hop::MintsToken { account, .. } => {
            parts.push(Part::plain("May create a token for "));
            parts.push(part(look.named(account)));
        }
        Hop::Impersonates { account } => {
            parts.push(Part::plain("May impersonate "));
            parts.push(part(look.named(account)));
        }
        Hop::Binds { role, at } => {
            parts.push(Part::plain("May bind "));
            parts.push(look_role(role, look));
            parts.push(Part {
                text: format!(" to itself {}", at.words()),
                href: None,
            });
            return parts;
        }
        Hop::Rewrites { role, at } => {
            parts.push(Part::plain("May rewrite "));
            parts.push(look_role(role, look));
            parts.push(Part {
                text: format!(", bound to it {}", at.words()),
                href: None,
            });
            return parts;
        }
        Hop::StartsPod { .. } | Hop::ExecMounts { .. } => return parts,
    }
    let Some(through) = hop.account().and_then(|account| access.of(account)) else {
        return parts;
    };
    let roles: Vec<Part> = through
        .everything
        .iter()
        .filter(|(at, _)| *at == Where::Everywhere)
        .map(|(_, role)| look_role(role, look))
        .collect();
    if roles.is_empty() {
        return parts;
    }
    parts.push(Part::plain(", which is bound to "));
    parts.extend(listed(roles));
    parts
}

fn look_role(role: &transect_graph::edges::Object, look: &Look) -> Part {
    Part {
        text: words::named(role),
        href: crate::access::link(role, look.href),
    }
}

/// `parts` joined as an English list: "a and b", "a, b, and c".
fn listed(parts: Vec<Part>) -> Vec<Part> {
    let count = parts.len();
    let mut joined = Vec::new();
    for (at, part) in parts.into_iter().enumerate() {
        match (count, at) {
            (_, 0) => {}
            (2, _) => joined.push(Part::plain(" and ")),
            (_, at) if at + 1 == count => joined.push(Part::plain(", and ")),
            _ => joined.push(Part::plain(", ")),
        }
        joined.push(part);
    }
    joined
}

fn everyone(access: &Access) -> String {
    let holding: Vec<String> = EVERYONE
        .iter()
        .filter(|group| {
            let held = access.of(&Account::Group(group.to_string()));
            held.is_some_and(Held::holds_any)
        })
        .map(|group| format!("Group {group}"))
        .collect();
    match holding.as_slice() {
        [] => "No binding grants any of the six to Group system:authenticated or Group system:serviceaccounts, the groups every account is in.".into(),
        groups => format!("{} may do some of these six, and every account is in it, so every account may do them too.", groups.join(" and ")),
    }
}

/// The gray line that counts `accounts`, such as "3 accounts are bound
/// to roles that grant none of the six", with their names on hover.
/// Returns `None` when `accounts` is empty.
fn counted(accounts: &[&Account], one: &str, many: &str, rest: &str) -> Option<Counted> {
    let verb = match accounts.len() {
        0 => return None,
        1 => one,
        _ => many,
    };
    let names: Vec<String> = accounts.iter().map(|account| account.text()).collect();
    Some(Counted {
        text: format!("{} {verb} {rest}.", accounts.len()),
        names: names.join(", "),
    })
}
