//! The objects of a cluster, the edges between them, the URLs of their
//! pages, and the parsers that every domain shares: selectors, pods, DNS
//! names, image references, projected tokens, untrusted text, upstream
//! URLs, and environments.

pub mod dns;
pub mod edges;
pub mod environment;
pub mod image;
pub mod links;
pub mod pods;
pub mod selector;
pub mod tokens;
pub mod untrusted;
pub mod upstream;
