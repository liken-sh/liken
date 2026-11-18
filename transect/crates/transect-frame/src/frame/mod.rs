//! `layout.html`, the template that every full page extends, with the
//! breadcrumbs, the tabs, and the brand files. This crate depends on no
//! other `transect` crate, so every other crate can use it without a
//! dependency cycle. The `transect` binary serves the brand files from
//! `web::assets`.

pub mod crumbs;
pub mod tabs;
