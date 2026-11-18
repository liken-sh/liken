//! Models the machines, devices, storage, and volumes that workloads run
//! on, the systems outside the cluster that they depend on, the loss of a
//! machine, placement, and the guards that keep a workload up.

pub mod devices;
pub mod guards;
pub mod loss;
pub mod machines;
pub mod outside;
pub mod placement;
pub mod storage;
pub mod volumes;
