//! Tests of each domain's reading of the fixture cluster.  The fixture
//! builds a `Snapshot`, so it reads every domain, and these tests are
//! in the binary crate beside it, because the tests of a lower crate
//! cannot use the fixture.

mod gitops;
mod vulnerabilities;
mod vulnerabilities_pages;
