//! Reads the cluster for `transect`. `connect` builds the API server
//! client, `cluster` keeps in-memory stores of the objects current with
//! watches, `ownership` follows each pod to its top owner, and
//! `workloads` describes each top owner as a workload.

pub mod cluster;
pub mod connect;
pub mod ownership;
pub mod workloads;
