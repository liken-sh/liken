//! The client side of `liken_arrangement_v1`, generated from the protocol's
//! XML at build time.
//!
//! The XML beside this file is a copy of `weston/layout/liken-arrangement-v1.xml`,
//! which the compositor module builds from. The image build of the display
//! reads only this component's directory, so the copy is here, and a test
//! fails when the two differ.

#![allow(missing_docs, unused_imports, clippy::all)]

// The generated code names the client crate and its core protocol from the
// module it expands in, so both are in scope here.
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
    /// one, so a change to one that misses the other would put two versions
    /// of the protocol on the wire.
    #[test]
    fn the_protocol_is_the_one_the_compositor_builds() {
        let root = env!("CARGO_MANIFEST_DIR");
        let here = std::fs::read_to_string(format!("{root}/src/arranger/liken-arrangement-v1.xml"))
            .expect("the display's copy reads");
        let weston = std::fs::read_to_string(format!(
            "{root}/../../weston/layout/liken-arrangement-v1.xml"
        ))
        .expect("weston's copy reads");
        assert!(
            here == weston,
            "display/src/arranger/liken-arrangement-v1.xml differs from weston/layout/liken-arrangement-v1.xml; copy weston's file over the display's"
        );
    }
}
