//! The client side of `liken_arrangement_v1`, generated from the
//! protocol's XML at build time.
//!
//! The XML beside this file is a copy of
//! `weston/layout/liken-arrangement-v1.xml`, which the compositor builds
//! from. The display's image build can read only this component's
//! directory, so the copy is here. A test fails when the two differ.

#![allow(missing_docs, unused_imports, clippy::all)]

// The generated code refers to the client crate and the core protocol
// by name, so both must be in scope in this module.
use wayland_client;
use wayland_client::protocol::*;

pub mod __interfaces {
    use wayland_client::protocol::__interfaces::*;
    wayland_scanner::generate_interfaces!("src/arranger/liken-arrangement-v1.xml");
}
use self::__interfaces::*;

wayland_scanner::generate_client_code!("src/arranger/liken-arrangement-v1.xml");

#[cfg(test)]
mod tests {
    /// The compositor builds from weston's copy and the display from this
    /// one. If only one changed, the two would use different versions of
    /// the protocol.
    #[test]
    fn the_copy_matches_the_compositor_protocol() {
        let root = env!("CARGO_MANIFEST_DIR");
        let here = std::fs::read_to_string(format!("{root}/src/arranger/liken-arrangement-v1.xml"))
            .expect("reading the display's copy of the protocol");
        let weston = std::fs::read_to_string(format!(
            "{root}/../../weston/layout/liken-arrangement-v1.xml"
        ))
        .expect("reading weston's copy of the protocol");
        assert!(
            here == weston,
            "display/src/arranger/liken-arrangement-v1.xml differs from weston/layout/liken-arrangement-v1.xml; copy weston's file over the display's"
        );
    }
}
