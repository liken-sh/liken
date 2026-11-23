//! Renders a page template, either whole or as its `main` block alone,
//! and passes the values that the masthead reads. Every page shares the
//! masthead.

use askama::Template;

use super::render::Part;

/// A page template that can also render only its `main` block.
pub trait Parts: Template {
    fn main(&self) -> impl Template + '_;
}

macro_rules! parts {
    ($($page:ty),*) => {$(
        impl Parts for $page {
            fn main(&self) -> impl Template + '_ {
                self.as_main()
            }
        }
    )*};
}

parts!(
    transect_map::map::ClusterPage,
    transect_map::map::NamespacePage,
    transect_map::map::WorkloadPage,
    transect_map::map::SectionPage,
    transect_map::map::PodPage,
    transect_map::map::GonePage,
    transect_map::map::GitOpsPage,
    transect_map::map::KnownPage,
    transect_map::map::MachinePage,
    transect_map::map::ground::GroundPage,
    transect_map::objects::ListPage,
    transect_map::objects::ObjectPage,
    transect_map::objects::KindsPage,
    transect_map::objects::KindPage,
    transect_map::shapes::ShapesPage,
    transect_map::lenses::logs::LogsPage,
    transect_resources::resources::ResourcesPage,
    transect_supply::changed::ChangedPage,
    transect_access::access::AccountsPage,
    transect_access::access::WaysPage,
    transect_access::access::drawing::page::AccessPage,
    transect_access::access::drawing::account_page::AccountPage,
    transect_access::access::namespace::NamespaceAccessPage,
    transect_supply::vulnerabilities::ListPage,
    transect_supply::vulnerabilities::IdPage,
    transect_map::map::vulnerabilities::WorkloadVulnerabilitiesPage
);

/// Which part of a page to render, and whether the masthead links to the
/// vulnerabilities pages. It does when some loaded `KindKnowledge` has a
/// `findings` section that describes a scanner's reports.
#[derive(Clone, Copy)]
pub struct Drawing {
    pub part: Part,
    pub vulnerabilities: bool,
}

/// A part with the masthead's vulnerabilities link off.
impl From<Part> for Drawing {
    fn from(part: Part) -> Self {
        Self {
            part,
            vulnerabilities: false,
        }
    }
}

pub fn template(page: impl Parts, drawing: impl Into<Drawing>) -> Option<String> {
    let drawing = drawing.into();
    match drawing.part {
        Part::Whole => render(&page, drawing),
        Part::Main => render(&page.main(), drawing),
    }
}

fn render(template: &impl Template, drawing: Drawing) -> Option<String> {
    let scripts = super::scripts::version();
    let values: [(&str, &dyn std::any::Any); 2] = [
        ("vulnerabilities", &drawing.vulnerabilities),
        ("scripts", &scripts),
    ];
    template
        .render_with_values(&values)
        .inspect_err(|error| tracing::error!(%error, "A page did not render"))
        .ok()
}
