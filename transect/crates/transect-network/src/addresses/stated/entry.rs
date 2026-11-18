//! Reads one entry of a list that a person writes, such as
//! `depends-on`, as a URL, a host, or an object by its resource and
//! name.

use crate::addresses::parse::{self, Address};

#[derive(Debug, PartialEq, Eq)]
pub enum Entry {
    /// A URL, a host, or a `host:port`.
    Address(Address),
    /// `<resource>/<name>` or `<namespace>/<resource>/<name>`, with a
    /// `<resource>` the cluster serves.
    Object {
        namespace: Option<String>,
        resource: String,
        name: String,
    },
    /// Nothing transect can read, such as prose or a resource the
    /// cluster does not serve.
    Unread,
}

/// `served` returns whether the cluster serves a resource by its
/// plural name, such as `deployments`.  An entry with a slash and no
/// `://` is an object, because a host has no slash.  Any other entry
/// must parse as exactly one address, or it is unread.
pub fn read(entry: &str, served: impl Fn(&str) -> bool) -> Entry {
    if !entry.contains("://") && entry.contains('/') {
        return object(entry, served);
    }
    match parse::addresses(entry).as_slice() {
        [address] => Entry::Address(address.clone()),
        _ => Entry::Unread,
    }
}

fn object(entry: &str, served: impl Fn(&str) -> bool) -> Entry {
    let parts: Vec<&str> = entry.split('/').collect();
    let (namespace, resource, name) = match parts.as_slice() {
        [resource, name] => (None, *resource, *name),
        [namespace, resource, name] => (Some(*namespace), *resource, *name),
        _ => return Entry::Unread,
    };
    let word = |part: &str| !part.is_empty() && !part.contains(char::is_whitespace);
    let words = namespace.is_none_or(word) && word(resource) && word(name);
    match words && served(resource) {
        true => Entry::Object {
            namespace: namespace.map(Into::into),
            resource: resource.into(),
            name: name.into(),
        },
        false => Entry::Unread,
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    fn served(resource: &str) -> bool {
        ["deployments", "services", "nodes"].contains(&resource)
    }

    fn address(text: &str) -> Entry {
        match parse::addresses(text).pop() {
            Some(address) => Entry::Address(address),
            None => Entry::Unread,
        }
    }

    fn object(namespace: Option<&str>, resource: &str, name: &str) -> Entry {
        Entry::Object {
            namespace: namespace.map(Into::into),
            resource: resource.into(),
            name: name.into(),
        }
    }

    #[rstest]
    #[case::a_url("https://api.example.com/v1", address("https://api.example.com/v1"))]
    #[case::a_host("db.shop", address("db.shop"))]
    #[case::a_host_and_port("queue:9092", address("queue:9092"))]
    #[case::a_short_name("postgres", address("postgres"))]
    #[case::an_object("deployments/cache", object(None, "deployments", "cache"))]
    #[case::an_object_elsewhere("blog/services/db", object(Some("blog"), "services", "db"))]
    #[case::a_resource_not_served("widgets/cache", Entry::Unread)]
    #[case::too_many_parts("a/deployments/b/c", Entry::Unread)]
    #[case::prose("the billing team's API", Entry::Unread)]
    #[case::two_hosts("db-0,db-1", Entry::Unread)]
    fn reads_each_entry_form(#[case] entry: &str, #[case] expected: Entry) {
        assert_eq!(read(entry, served), expected);
    }
}
