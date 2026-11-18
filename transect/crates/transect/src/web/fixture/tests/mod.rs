//! Tests that read each domain from the fixture cluster. They are in
//! this crate because the fixture builds a `Snapshot`, and the domain
//! crates that this crate depends on cannot depend on it.

mod gitops;
mod vulnerabilities;
mod vulnerabilities_pages;
