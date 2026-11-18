//! The metrics that a cluster declares how to read.  The
//! `MetricsSource` resource names the Prometheus, the PromQL queries,
//! and the alert conventions, so the code contains none of them.  This
//! module also picks which Prometheus transect queries, and polls the
//! bytes used on each volume.

pub mod poll;
mod reach;
pub mod source;

pub use poll::Reading;
pub use reach::Reaching;
pub use source::MetricsSource;
