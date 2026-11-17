//! A card is a one-screen summary of one object, drawn only from what
//! the API and Prometheus return.  The namespace card shows on two
//! views, the cluster with a namespace selected and the namespace page,
//! so cards are kept apart from the views that draw them.
//!
//! A card in this module is the shell: the name, the health, and the
//! chips, with each section in the order the card draws them.  Each
//! section's view model and template are in the module of the domain
//! the section reads, as `card.rs` and `card.html` in addresses,
//! ownership, policy, devices, and gitops.  A section names the ends it
//! lists with `links::Reference`.

mod chips;
mod namespace;
mod sentence;
pub(crate) mod workload;

pub use chips::owned_key;
pub use namespace::NamespaceCard;
pub use workload::WorkloadCard;
