//! Metrics that a cluster declares how to read.  This module defines
//! the `MetricsSource` resource, which declares the Prometheus, the
//! PromQL queries, and the alert conventions, so the code contains none
//! of them.  It also selects which Prometheus transect queries, and
//! polls the bytes used on each volume.

pub mod poll;
mod reach;
pub mod source;

pub use poll::Reading;
pub use reach::Reaching;
pub use source::MetricsSource;
