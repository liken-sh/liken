//! The URL of every object's page, and how a page writes an object's
//! name: as a link to the object's page when it has one, and as plain
//! text when it has none, so no link leads to a page that does not
//! exist.  `object_link` in `link.html` draws every such name, and
//! `map::pages::Pages` decides which objects have a page.

use std::fmt::Write;

use crate::edges::{Device, Object, Plurals};

mod breaks;
pub mod kinds;
mod name;
mod reference;
mod sentence;

pub use breaks::pieces;
pub use name::{kind_and_name_in, name_in};
pub use reference::{Named, Reference};
pub use sentence::{Linker, Part, Piece, listed, text};

/// The path of `object`'s page, under its namespace or under `/cluster`
/// when the object has no namespace.  A `Namespace`, a `Node`, and a
/// `PersistentVolume` have their own paths, and a kind that
/// `kinds::url_of` cannot name returns none.  It does not check that a
/// page exists at the path, which `Pages` decides.
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

/// The path that a link to `object` opens: `pod`'s path for a pod, and
/// `page`'s path for every other kind.
pub fn link(object: &Object, plurals: &Plurals) -> Option<String> {
    match (object.group.as_str(), object.kind.as_str()) {
        ("", "Pod") => Some(pod(object.namespace.as_deref()?, &object.name)),
        _ => page(object, plurals),
    }
}

/// The path of the access page of a group that no object represents.
pub fn group(name: &str) -> String {
    format!("/access/accounts/groups/{}", segment(name))
}

/// The path of the access page of a user that no object represents.
pub fn user(name: &str) -> String {
    format!("/access/accounts/users/{}", segment(name))
}

pub fn namespace(name: &str) -> String {
    format!("/namespaces/{}", segment(name))
}

/// The path of the page that lists the objects of the custom kind
/// `plural` of `group` in every namespace.
pub fn kind(group: &str, plural: &str) -> String {
    format!("/kinds/{}", kinds::url_name(group, plural))
}

/// The path of the list of one kind's objects in `namespace`.
/// `resource` is the kind's name in a URL, such as `widgets.example.io`.
pub fn objects(namespace: &str, resource: &str) -> String {
    format!("/namespaces/{}/{resource}", segment(namespace))
}

pub fn volume(name: &str) -> String {
    format!("/volumes/{}", segment(name))
}

pub fn machine(name: &str) -> String {
    format!("/machines/{}", segment(name))
}

/// The path of a claim's page, which shows the volume bound to it.
pub fn claim(namespace: &str, name: &str) -> String {
    format!(
        "/namespaces/{}/persistentvolumeclaims/{}",
        segment(namespace),
        segment(name)
    )
}

/// The path of a device's line on its machine's page, or none when the
/// device does not belong to one machine.
pub fn device(device: &Device) -> Option<String> {
    let machine = machine(device.machine.as_deref()?);
    Some(format!(
        "{machine}#{}",
        device_anchor(&device.driver, &device.name)
    ))
}

/// The HTML id of a device's line on its machine's page.  It is built
/// from the driver and name, with every character other than an ASCII
/// letter, digit, `-`, or `_` replaced by `-`.
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

/// `text` with every byte except the unreserved characters of a URL
/// percent-encoded, so `text` cannot end a query or a link early.
pub fn percent_encoded(text: &str) -> String {
    encoded(text, b"")
}

/// `name` as one segment of a URL path.  A DNS label comes back
/// unchanged, and so do the `:` and `@` in RBAC names.  Every other
/// byte outside the unreserved characters, such as `/`, `?`, `#`, or a
/// space, is percent-encoded, so a `ClusterRole` named `a?b` still links
/// to its own page.
pub fn segment(name: &str) -> String {
    encoded(name, b":@")
}

/// `text` with every byte percent-encoded except the unreserved
/// characters and the bytes in `keep`.
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

/// `path` with each `%XX` decoded to its byte, as axum's `Path` decodes
/// it in the server.  Tests use it to render a page from a link the way
/// the server does.
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
    fn builds_a_path_with_or_without_a_namespace(
        #[case] namespace: Option<&str>,
        #[case] expected: Option<&str>,
    ) {
        let object = Object::new("cache.example.io", "Cache", namespace, "sessions");
        assert_eq!(page(&object, &plurals()).as_deref(), expected);
    }

    #[rstest]
    #[case::on_a_machine(Some("alpha"), Some("/machines/alpha#device-gpu-example-com-card-0"))]
    #[case::on_no_one_machine(None, None)]
    fn links_a_device_to_its_line_on_its_machine(
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
