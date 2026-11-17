//! The accounts that RBAC grants to, where a grant applies, and how an
//! account gets a capability: from a binding that grants it, or by a
//! step from such a grant.

use transect_graph::edges::Object;

/// A subject that a binding may name.  A group or a user has no object
/// of its own.  A ServiceAccount has one, but a binding may name a
/// ServiceAccount that does not exist.
#[derive(Clone, Debug, PartialEq, Eq, Hash, PartialOrd, Ord)]
pub enum Account {
    ServiceAccount { namespace: String, name: String },
    Group(String),
    User(String),
}

impl Account {
    pub fn service_account(namespace: &str, name: &str) -> Self {
        Self::ServiceAccount {
            namespace: namespace.into(),
            name: name.into(),
        }
    }

    /// The account's object, when the account has one.
    pub fn object(&self) -> Option<Object> {
        match self {
            Self::ServiceAccount { namespace, name } => {
                Some(Object::new("", "ServiceAccount", Some(namespace), name))
            }
            _ => None,
        }
    }

    /// The page of a group or a user, which has no object.  None for a
    /// ServiceAccount, because its page is its object's page.
    pub fn subject_page(&self) -> Option<String> {
        match self {
            Self::ServiceAccount { .. } => None,
            Self::Group(name) => Some(transect_graph::links::group(name)),
            Self::User(name) => Some(transect_graph::links::user(name)),
        }
    }

    /// Its kind and name as a page names it: `ServiceAccount shop/web`,
    /// `Group system:masters`.
    pub fn text(&self) -> String {
        match self {
            Self::ServiceAccount { namespace, name } => {
                format!("ServiceAccount {namespace}/{name}")
            }
            Self::Group(name) => format!("Group {name}"),
            Self::User(name) => format!("User {name}"),
        }
    }

    /// Its name alone, for a line that already states that it is an
    /// account: `shop/web`.
    pub fn short(&self) -> String {
        match self {
            Self::ServiceAccount { namespace, name } => format!("{namespace}/{name}"),
            Self::Group(name) | Self::User(name) => name.clone(),
        }
    }

    /// The groups that the API server puts it in and that a binding may
    /// name.  A ServiceAccount is in the group of every ServiceAccount
    /// and in the group of its namespace's ServiceAccounts, and every
    /// account that signs in is in the authenticated group.
    pub fn groups(&self) -> Vec<String> {
        match self {
            Self::ServiceAccount { namespace, .. } => vec![
                SERVICE_ACCOUNTS.into(),
                format!("{SERVICE_ACCOUNTS}:{namespace}"),
                AUTHENTICATED.into(),
            ],
            Self::User(_) => vec![AUTHENTICATED.into()],
            Self::Group(_) => Vec::new(),
        }
    }
}

/// The group of every ServiceAccount, and with `:{namespace}`, of
/// every ServiceAccount in one namespace.
pub const SERVICE_ACCOUNTS: &str = "system:serviceaccounts";
/// The group of every request that signs in.
pub const AUTHENTICATED: &str = "system:authenticated";

/// Where a grant applies.  A ClusterRoleBinding grants in every
/// namespace, and a RoleBinding grants in one.
#[derive(Clone, Debug, PartialEq, Eq, Hash, PartialOrd, Ord)]
pub enum Where {
    Everywhere,
    In(String),
}

impl Where {
    /// Whether a grant here applies in `namespace`.
    pub fn covers(&self, namespace: &str) -> bool {
        match self {
            Self::Everywhere => true,
            Self::In(here) => here == namespace,
        }
    }

    /// The place in words: "in every namespace", "in shop".
    pub fn words(&self) -> String {
        match self {
            Self::Everywhere => "in every namespace".into(),
            Self::In(namespace) => format!("in {namespace}"),
        }
    }
}

/// A binding that grants a capability: the binding, the role it binds,
/// the objects the rule names when it is limited by name, and the
/// group the account is in when the binding names the group and not
/// the account.
#[derive(Clone, Debug, PartialEq, Eq, Hash, PartialOrd, Ord)]
pub struct Via {
    pub binding: Object,
    pub role: Object,
    pub names: Vec<String>,
    pub group: Option<String>,
}

/// The step by which a capability follows from a grant, named by its
/// first hop.
#[derive(Clone, Debug, PartialEq, Eq, Hash, PartialOrd, Ord)]
pub enum Hop {
    /// It may run pods or change workloads in a namespace, through
    /// `basis`, so it may run a pod as `account`, a ServiceAccount
    /// there, and do what that account may.
    RunsAs {
        account: Account,
        basis: super::Capability,
        at: Where,
    },
    /// It may exec into a pod that runs as `account` and mounts its
    /// token.
    ExecToken { account: Account, at: Where },
    /// A pod it starts, through `basis`, may mount any Secret in its
    /// namespace.
    StartsPod { basis: super::Capability, at: Where },
    /// It may exec into pods that mount Secrets, and read those, by
    /// name.
    ExecMounts { at: Where },
    /// It may change `applier`, an object that applies what it names,
    /// or its source, and the applier may apply as `account`, in the
    /// way that `how` gives.
    Applies {
        account: Account,
        applier: Object,
        how: AppliesAs,
    },
    /// It may bind `role` to itself at `at`: it may bind the role and
    /// create a binding there.
    Binds { role: Object, at: Where },
    /// It may rewrite `role`, which a binding binds it to at `at`, to
    /// grant anything: it may escalate and update the role.
    Rewrites { role: Object, at: Where },
    /// It may create a token for `account`, a ServiceAccount in `at`,
    /// and sign in as it.
    MintsToken { account: Account, at: Where },
    /// It may impersonate `account`.
    Impersonates { account: Account },
}

/// How an applier comes to apply as an account that a step reaches.
#[derive(Clone, Debug, PartialEq, Eq, Hash, PartialOrd, Ord)]
pub enum AppliesAs {
    /// Its object names the account now.
    Names,
    /// It applies as its kind's controller's account.
    Controller,
    /// It may name the account: an account that may change the object
    /// may set `field` to any ServiceAccount in its namespace.
    MayName { field: String },
    /// It applies as the account when `field` names no account, and an
    /// account that may change the object may unset `field`.
    Unset { field: String },
    /// It fetches through `source`, which the step may change, and it
    /// applies as the account now.
    Through { source: Object },
}

impl Hop {
    /// The account the step goes through, when it goes through one.
    pub fn account(&self) -> Option<&Account> {
        match self {
            Self::RunsAs { account, .. }
            | Self::ExecToken { account, .. }
            | Self::Applies { account, .. }
            | Self::MintsToken { account, .. }
            | Self::Impersonates { account } => Some(account),
            _ => None,
        }
    }
}
