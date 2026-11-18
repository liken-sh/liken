//! A card is a one-screen summary of one object, built only from what
//! the Kubernetes API and Prometheus return. Cards are kept apart from
//! the views because several views draw the same card: the namespace
//! card shows on the cluster map with a namespace selected and on the
//! namespace page.
//!
//! This module builds the parts that every card shares: the name, the health, the
//! chips, and the order of the sections. Each section's view model and
//! template are `card.rs` and `card.html` in the module of the domain
//! that the section reads. A section links the objects it lists with
//! `links::Reference`.

mod chips;
mod namespace;
mod sentence;
pub(crate) mod workload;

pub use chips::owned_key;
pub use namespace::NamespaceCard;
pub use workload::WorkloadCard;
