//! Where each running image comes from, and what transect reads about
//! it: images, forges, GitOps, what changed, and a scanner's findings
//! and vulnerabilities.

pub mod changed;
pub mod findings;
pub mod forge;
pub mod gitops;
pub mod images;
pub mod vulnerabilities;
