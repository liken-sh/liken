//! Search entries for the groups and users that bindings name. No
//! Kubernetes object represents a group or a user, so the object index
//! has no entry for them.

use std::sync::Arc;

use transect_access::access::{Access, Account};
use transect_map::search::{Entry, Sort};

/// Each entry links to the subject's access page.
pub fn subjects(access: &Access) -> impl Iterator<Item = Entry> + '_ {
    access.accounts.keys().filter_map(|account| {
        let (name, kind) = match account {
            Account::Group(name) => (name, "Group"),
            Account::User(name) => (name, "User"),
            Account::ServiceAccount { .. } => return None,
        };
        Some(Entry {
            aliases: Vec::new(),
            kind_names: Arc::new([]),
            name: name.clone(),
            kind: kind.into(),
            sort: Sort::Object,
            context: None,
            href: account.subject_page()?,
            object: None,
            lenses: Vec::new(),
        })
    })
}
