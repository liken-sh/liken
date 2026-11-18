//! What transect reads about the workloads that run in the cluster:
//! image labels and registry mirrors, forges, GitOps appliers, recent
//! changes, findings such as log-pattern matches, and vulnerabilities.

pub mod changed;
pub mod findings;
pub mod forge;
pub mod gitops;
pub mod images;
pub mod vulnerabilities;
