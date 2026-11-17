//! An account's page.  A ServiceAccount's page is its access drawing,
//! with its card beside it.  A group's or a user's page, which has no
//! object, is under the accounts matrix.

use askama::Template;

use super::Drawing;
use transect_frame::frame::crumbs::Crumb;
use transect_graph::links::Part;
use transect_map::objects::ObjectCard;

/// Where an account's page belongs, as its crumbs show.
#[derive(Debug)]
pub enum Place {
    /// A ServiceAccount, in its namespace and the namespace's band.
    Namespace { namespace: String, layer: String },
    /// A group or a user, under the accounts matrix.
    Accounts,
}

#[derive(Template)]
#[template(path = "access/drawing/account.html", blocks = ["main"])]
pub struct AccountPage {
    pub place: Place,
    /// The account as a page names it: `ServiceAccount web`, `Group
    /// system:masters`.
    pub title: String,
    pub name: String,
    pub path: String,
    pub drawing: Drawing,
    /// "Who may run as it", for a ServiceAccount.
    pub run_as: Option<Vec<Part>>,
    /// A ServiceAccount's card.  A group or a user gets the lens's
    /// card.
    pub card: Option<ObjectCard>,
    pub no_gitops: bool,
}

impl AccountPage {
    pub fn crumbs(&self) -> Vec<Crumb> {
        let mut crumbs = vec![Crumb::home()];
        match &self.place {
            Place::Namespace { namespace, layer } => {
                let select = format!("/?select={namespace}");
                crumbs.push(Crumb::link(layer, format!("{select}#{layer}")));
                crumbs.push(Crumb::link_name(
                    namespace,
                    transect_graph::links::namespace(namespace),
                ));
                crumbs.push(Crumb::current("ServiceAccounts"));
                crumbs.push(Crumb::current_name(&self.name));
            }
            Place::Accounts => {
                crumbs.push(Crumb::link("access", "/access".into()));
                crumbs.push(Crumb::link("accounts", "/access/accounts".into()));
                crumbs.push(Crumb::current_name(&self.title));
            }
        }
        crumbs
    }

    pub fn namespace(&self) -> Option<&str> {
        match &self.place {
            Place::Namespace { namespace, .. } => Some(namespace),
            Place::Accounts => None,
        }
    }

    pub fn events_url(&self) -> String {
        format!("/events{}", self.path)
    }
}
