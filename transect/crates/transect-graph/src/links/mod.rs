//! Every object's page, and the one way a page names an object: its
//! name, linked to its page when it has one, and plain text when it has
//! none, so no name links to a page that does not exist.  `object_link`
//! in `link.html` draws every such name, and `map::pages::Pages`
//! decides which objects have a page.

use std::fmt::Write;

use crate::edges::{Device, Object, Plurals};

pub mod kinds;
mod name;
mod reference;
mod sentence;

pub use name::{kind_and_name_in, name_in};
pub use reference::{Named, Reference};
pub use sentence::{Linker, Part, Piece, listed, text};

/// The path of `object`'s page at the URL every kind but a pod and a
/// `Node` shares, or none for a kind with no such URL: under its
/// namespace, or under `/cluster` for a kind outside any.  This is only
/// the URL's shape; `Pages` decides whether a page exists there.
pub fn page(object: &Object, plurals: &Plurals) -> Option<String> {
    match (object.group.as_str(), object.kind.as_str()) {
        ("", "Namespace") => return Some(namespace(&object.name)),
        ("", "Node") => return Some(machine(&object.name)),
        ("", "PersistentVolume") => return Some(volume(&object.name)),
        _ => {}
    }
    let resource = kinds::url_of(object, plurals)?;
    let name = segment(&object.name);
    Some(match object.namespace.as_deref() {
        Some(namespace) => format!("/namespaces/{}/{resource}/{name}", segment(namespace)),
        None => format!("/cluster/{resource}/{name}"),
    })
}

/// The page a link to `object` opens: a pod's own page, or the path any
/// other kind shares.
pub fn link(object: &Object, plurals: &Plurals) -> Option<String> {
    match (object.group.as_str(), object.kind.as_str()) {
        ("", "Pod") => Some(pod(object.namespace.as_deref()?, &object.name)),
        _ => page(object, plurals),
    }
}

/// The access page of a group that no object represents.
pub fn group(name: &str) -> String {
    format!("/access/accounts/groups/{}", segment(name))
}

/// The access page of a user that no object represents.
pub fn user(name: &str) -> String {
    format!("/access/accounts/users/{}", segment(name))
}

pub fn namespace(name: &str) -> String {
    format!("/namespaces/{}", segment(name))
}

/// The page of every object of the custom kind `plural` of `group`,
/// across namespaces.
pub fn kind(group: &str, plural: &str) -> String {
    format!("/kinds/{}", kinds::url_name(group, plural))
}

/// The list of one kind's objects in `namespace`, by the kind's name
/// in a URL, such as `widgets.example.io`.
pub fn objects(namespace: &str, resource: &str) -> String {
    format!("/namespaces/{}/{resource}", segment(namespace))
}

pub fn volume(name: &str) -> String {
    format!("/volumes/{}", segment(name))
}

pub fn machine(name: &str) -> String {
    format!("/machines/{}", segment(name))
}

/// A claim's page, which shows the volume bound to it.
pub fn claim(namespace: &str, name: &str) -> String {
    format!(
        "/namespaces/{}/persistentvolumeclaims/{}",
        segment(namespace),
        segment(name)
    )
}

/// A device's place on its machine's page, or none for a device that
/// no one machine publishes.
pub fn device(device: &Device) -> Option<String> {
    let machine = machine(device.machine.as_deref()?);
    Some(format!(
        "{machine}#{}",
        device_anchor(&device.driver, &device.name)
    ))
}

/// The id of a device's line on its machine's page: its driver and
/// name, with every character but ASCII letters, digits, `-`, and `_`
/// replaced by a dash.
pub fn device_anchor(driver: &str, name: &str) -> String {
    let id = format!("device-{driver}-{name}");
    id.chars()
        .map(
            |c| match c.is_ascii_alphanumeric() || c == '-' || c == '_' {
                true => c,
                false => '-',
            },
        )
        .collect()
}

pub fn pod(namespace: &str, name: &str) -> String {
    format!("/namespaces/{}/pods/{}", segment(namespace), segment(name))
}

/// `text` with every byte but the URL's unreserved characters
/// percent-encoded, so a name cannot end the query or the link.
pub fn percent_encoded(text: &str) -> String {
    encoded(text, b"")
}

/// `name` as one segment of a URL's path.  Most names are DNS labels
/// and pass unchanged, and so does the `:` that RBAC names use; any byte
/// that could end the segment or the path, such as `/`, `?`, `#`, or a
/// space, is percent-encoded, so a `ClusterRole` named `a?b` links to its
/// own page.
pub fn segment(name: &str) -> String {
    encoded(name, b":@")
}

/// `text` with every byte but the unreserved characters and `keep`
/// percent-encoded.
fn encoded(text: &str, keep: &[u8]) -> String {
    let mut encoded = String::with_capacity(text.len());
    for byte in text.bytes() {
        match byte {
            b'A'..=b'Z' | b'a'..=b'z' | b'0'..=b'9' | b'-' | b'.' | b'_' | b'~' => {
                encoded.push(byte as char);
            }
            _ if keep.contains(&byte) => encoded.push(byte as char),
            _ => {
                let _ = write!(encoded, "%{byte:02X}");
            }
        }
    }
    encoded
}

/// `path` with each `%XX` read as its byte, as the server's router,
/// axum's `Path`, reads it, for the tests that render a page from a
/// link the way the server would.
#[cfg(any(test, feature = "fixtures"))]
pub fn percent_decoded(path: &str) -> String {
    let bytes = path.as_bytes();
    let mut out = Vec::with_capacity(bytes.len());
    let mut at = 0;
    while at < bytes.len() {
        let hex = bytes
            .get(at + 1..at + 3)
            .and_then(|hex| u8::from_str_radix(std::str::from_utf8(hex).ok()?, 16).ok());
        match (bytes[at], hex) {
            (b'%', Some(byte)) => {
                out.push(byte);
                at += 3;
            }
            (byte, _) => {
                out.push(byte);
                at += 1;
            }
        }
    }
    String::from_utf8(out).unwrap()
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use std::collections::HashMap;

    use super::*;

    fn plurals() -> Plurals {
        HashMap::from([(("cache.example.io".into(), "Cache".into()), "caches".into())])
    }

    #[rstest]
    #[case::namespaced(
        Some("shop"),
        Some("/namespaces/shop/caches.cache.example.io/sessions")
    )]
    #[case::cluster_scoped(None, Some("/cluster/caches.cache.example.io/sessions"))]
    fn the_shared_url(#[case] namespace: Option<&str>, #[case] expected: Option<&str>) {
        let object = Object::new("cache.example.io", "Cache", namespace, "sessions");
        assert_eq!(page(&object, &plurals()).as_deref(), expected);
    }

    #[rstest]
    #[case::on_a_machine(Some("alpha"), Some("/machines/alpha#device-gpu-example-com-card-0"))]
    #[case::on_no_one_machine(None, None)]
    fn a_device_links_to_its_line_on_its_machine(
        #[case] machine: Option<&str>,
        #[case] expected: Option<&str>,
    ) {
        let found = Device {
            driver: "gpu.example.com".into(),
            pool: "alpha".into(),
            name: "card-0".into(),
            machine: machine.map(Into::into),
        };
        assert_eq!(device(&found).as_deref(), expected);
    }
}
