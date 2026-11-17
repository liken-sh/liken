//! The steps from rules that grant none of the six capabilities: acting
//! as an account through a token or impersonation, binding a role to
//! itself, rewriting a role it is bound to, and exec into pods a grant
//! names.

use super::super::account::{Account, Hop, Where};
use super::super::capability::{ALL, Scope};
use super::super::grants::RoleGrants;
use super::super::routes::{Names, Route, admits};
use super::{Follow, Places, Steps};
use transect_graph::edges::Object;

/// The group the API server's authorizer lets do anything, bound or
/// not.
const MASTERS: &str = "system:masters";

/// The accounts that `routes` let an account act as, through a token it
/// may mint or an account it may impersonate.  Each step's hop is
/// `through` when the routes come through another step, such as a role
/// it may bind, and the route itself otherwise.
pub(super) fn acts_through(
    routes: &[(Route, Where, Names)],
    through: Option<&Hop>,
    places: &Places,
    steps: &mut Steps,
) {
    let hop = |own: Hop| through.cloned().unwrap_or(own);
    let mut impersonates_a_user = false;
    for (route, at, names) in routes {
        match route {
            Route::MintsTokens | Route::ImpersonatesServiceAccounts => {
                impersonates_a_user |= *route == Route::ImpersonatesServiceAccounts;
                for namespace in places.within(at) {
                    let here = places.accounts.get(namespace).into_iter().flatten();
                    for account in here {
                        let Account::ServiceAccount { name, .. } = account else {
                            continue;
                        };
                        if !admits(names, name) {
                            continue;
                        }
                        let own = match route {
                            Route::MintsTokens => Hop::MintsToken {
                                account: account.clone(),
                                at: Where::In(namespace.clone()),
                            },
                            _ => Hop::Impersonates {
                                account: account.clone(),
                            },
                        };
                        steps.acts_as.push((account.clone(), hop(own)));
                    }
                }
            }
            Route::ImpersonatesUsers => {
                impersonates_a_user = true;
                for user in places.users.iter().filter(|u| admits(names, &u.short())) {
                    let own = Hop::Impersonates {
                        account: user.clone(),
                    };
                    steps.acts_as.push((user.clone(), hop(own)));
                }
            }
            _ => {}
        }
    }
    if !impersonates_a_user {
        return;
    }
    let groups = routes
        .iter()
        .filter(|(route, ..)| *route == Route::ImpersonatesGroups);
    for (_, _, names) in groups {
        for group in places.groups.iter().filter(|g| admits(names, &g.short())) {
            let own = Hop::Impersonates {
                account: group.clone(),
            };
            steps.acts_as.push((group.clone(), hop(own)));
        }
        if admits(names, MASTERS) {
            let own = Hop::Impersonates {
                account: Account::Group(MASTERS.into()),
            };
            all_six(Where::Everywhere, hop(own), steps);
        }
    }
}

/// Every role `held` may bind to itself, and so what it may do through
/// each.  Binding a role requires `bind` on its kind, by its name, and
/// `create` on the binding kind, in a place where both grants apply.  A
/// RoleBinding in a namespace may name that namespace's Roles or any
/// ClusterRole.  A ClusterRoleBinding may name a ClusterRole, and only
/// a ClusterRoleBinding's grant may create one.
pub(super) fn binds(held: &super::Held, places: &Places, steps: &mut Steps) {
    let at = |wanted: Route| {
        let routes = held.routes.iter();
        let routes = routes.filter(move |((route, _), _)| *route == wanted);
        routes.map(|((_, at), names)| (at, names))
    };
    let creates_cluster =
        at(Route::CreatesClusterRoleBindings).any(|(at, _)| *at == Where::Everywhere);
    for (binds, names) in at(Route::BindsRoles) {
        for (creates, _) in at(Route::CreatesRoleBindings) {
            let Some(both) = meet(binds, creates) else {
                continue;
            };
            for namespace in places.within(&both) {
                let roles = places.roles.iter().filter(|(role, _)| {
                    role.kind == "Role"
                        && role.namespace.as_deref() == Some(namespace)
                        && admits(names, &role.name)
                });
                for (role, grants) in roles {
                    let here = Where::In(namespace.clone());
                    bound(role, grants, Scope::Namespace, here, places, steps);
                }
            }
        }
    }
    for (binds, names) in at(Route::BindsClusterRoles) {
        let roles = places
            .roles
            .iter()
            .filter(|(role, _)| role.kind == "ClusterRole" && admits(names, &role.name));
        let roles: Vec<&(Object, RoleGrants)> = roles.collect();
        for (creates, _) in at(Route::CreatesRoleBindings) {
            let Some(both) = meet(binds, creates) else {
                continue;
            };
            // A RoleBinding in a namespace grants a ClusterRole's
            // namespaced rules in that namespace, so when both grants
            // apply everywhere, the rules apply in every namespace.
            let places_bound: Vec<Where> = match &both {
                Where::Everywhere => vec![Where::Everywhere],
                Where::In(_) => places.within(&both).map(|n| Where::In(n.clone())).collect(),
            };
            for here in places_bound {
                for (role, grants) in &roles {
                    bound(role, grants, Scope::Namespace, here.clone(), places, steps);
                }
            }
        }
        if *binds == Where::Everywhere && creates_cluster {
            for (role, grants) in &roles {
                bound(
                    role,
                    grants,
                    Scope::Cluster,
                    Where::Everywhere,
                    places,
                    steps,
                );
            }
        }
    }
}

/// What follows when `held` may bind `role`, which grants `grants`, to
/// itself at `at` through a binding of `scope`: what the role grants
/// through such a binding.
fn bound(
    role: &Object,
    grants: &RoleGrants,
    scope: Scope,
    at: Where,
    places: &Places,
    steps: &mut Steps,
) {
    let hop = Hop::Binds {
        role: role.clone(),
        at: at.clone(),
    };
    // The place to read `grants` at, so that `grants` returns what a
    // binding of `scope` grants.
    let probe = match scope {
        Scope::Cluster => Where::Everywhere,
        Scope::Namespace => Where::In(String::new()),
    };
    for (capability, names) in grants.at(&probe) {
        let follow = Follow {
            hop: hop.clone(),
            names: names.clone(),
        };
        steps.own.push((*capability, at.clone(), follow));
    }
    let routes = grants.routes_at(&probe).iter();
    let routes: Vec<(Route, Where, Names)> = routes
        .map(|(route, names)| (*route, at.clone(), names.clone()))
        .collect();
    acts_through(&routes, Some(&hop), places, steps);
    let writes = grants.writes.iter();
    let writes: Vec<_> = writes
        .map(|(kind, names)| (kind.clone(), at.clone(), names.clone()))
        .collect();
    super::appliers::applies_through(&writes, Some(&hop), places, steps);
}

/// Every role `held` is bound to that it may rewrite to grant anything,
/// and so all six capabilities where its binding applies.  Rewriting a
/// role to grant more than the account has requires `escalate` and
/// `update` or `patch` on the role's kind, by its name.  A ClusterRole
/// is cluster-scoped, so only a ClusterRoleBinding's grant rewrites one.
pub(super) fn rewrites(held: &super::Held, steps: &mut Steps) {
    let at = |wanted: Route| {
        let routes = held.routes.iter();
        let routes = routes.filter(move |((route, _), _)| *route == wanted);
        routes.map(|((_, at), names)| (at, names))
    };
    let pairs = [
        (Route::EscalatesRoles, Route::RewritesRoles, "Role"),
        (
            Route::EscalatesClusterRoles,
            Route::RewritesClusterRoles,
            "ClusterRole",
        ),
    ];
    for (escalates, rewrites, kind) in pairs {
        for (escalate, escalate_names) in at(escalates) {
            for (rewrite, rewrite_names) in at(rewrites) {
                let Some(both) = meet(escalate, rewrite) else {
                    continue;
                };
                let roles = held.roles.iter().filter(|(_, role)| {
                    role.kind == kind
                        && admits(escalate_names, &role.name)
                        && admits(rewrite_names, &role.name)
                        && both.covers(role.namespace.as_deref().unwrap_or_default())
                });
                for (bound_at, role) in roles {
                    let hop = Hop::Rewrites {
                        role: role.clone(),
                        at: bound_at.clone(),
                    };
                    all_six(bound_at.clone(), hop, steps);
                }
            }
        }
    }
}

/// Adds all six capabilities at `at`, through `hop`.
fn all_six(at: Where, hop: Hop, steps: &mut Steps) {
    for capability in ALL {
        let follow = Follow {
            hop: hop.clone(),
            names: Vec::new(),
        };
        steps.own.push((capability, at.clone(), follow));
    }
}

/// The place where both `a` and `b` apply, when they share one.
fn meet(a: &Where, b: &Where) -> Option<Where> {
    match (a, b) {
        (Where::Everywhere, other) | (other, Where::Everywhere) => Some(other.clone()),
        (a, b) if a == b => Some(a.clone()),
        _ => None,
    }
}

/// What follows from exec into pods a grant names: by a pod's own name,
/// or through `nodes/proxy`, by the name of the machine it runs on.
/// An account that may exec into such a pod may act as the pod's
/// account through its mounted token, and may read its Secrets, by
/// name.
pub(super) fn execs_named(held: &super::Held, places: &Places, steps: &mut Steps) {
    for ((route, at), names) in &held.routes {
        if names.is_empty() {
            continue;
        }
        let named = |pod: &&super::Running| match route {
            Route::ExecsPods => admits(names, &pod.name),
            _ => pod.node.as_deref().is_some_and(|node| admits(names, node)),
        };
        if !matches!(route, Route::ExecsPods | Route::ProxiesNodes) {
            continue;
        }
        for namespace in places.within(at) {
            let pods = places.pods.get(namespace).into_iter().flatten();
            let pods: Vec<&super::Running> = pods.filter(named).collect();
            let here = Where::In(namespace.clone());
            let mut secrets: Vec<String> = Vec::new();
            for pod in &pods {
                if pod.token {
                    let account = Account::service_account(namespace, &pod.account);
                    let hop = Hop::ExecToken {
                        account: account.clone(),
                        at: here.clone(),
                    };
                    steps.acts_as.push((account, hop));
                }
                secrets.extend(pod.secrets.iter().cloned());
            }
            secrets.sort();
            secrets.dedup();
            if !secrets.is_empty() {
                let follow = Follow {
                    hop: Hop::ExecMounts { at: here.clone() },
                    names: secrets,
                };
                steps
                    .own
                    .push((super::Capability::ReadSecrets, here, follow));
            }
        }
    }
}
