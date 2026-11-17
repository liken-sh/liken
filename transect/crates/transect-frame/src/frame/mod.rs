//! The page frame every full page extends: `layout.html`, the
//! elevation crumb, the lens tabs, and the brand.  The frame names
//! nothing else in `transect`, so every domain's pages can extend it.
//! The binary serves the brand and the stylesheets from `web::assets`.

pub mod crumbs;
pub mod tabs;
