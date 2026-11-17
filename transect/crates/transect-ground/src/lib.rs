//! The ground under the workloads.  This crate models the machines,
//! devices, storage and volumes that workloads run on, the systems
//! outside the cluster that they depend on, the loss of a machine,
//! placement, and guards.

pub mod devices;
pub mod guards;
pub mod loss;
pub mod machines;
pub mod outside;
pub mod placement;
pub mod storage;
pub mod volumes;
