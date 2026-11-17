//! The page a name on an access page links to: the object's usual
//! page.  `web::carry` rewrites that link on the rendered page to point
//! into the access lens, so the name opens the object's access page
//! when it has one.

use crate::web::model::Model;
use transect_access::access::Account;
use transect_graph::edges::Object;
use transect_map::map::pages::Pages;

pub struct Look<'a> {
    pages: Pages<'a>,
}

impl<'a> Look<'a> {
    pub fn new(model: &'a Model) -> Self {
        Self {
            pages: model.snapshot.pages(),
        }
    }

    /// `object`'s page, which for a `ServiceAccount` is its drawing.
    pub fn href(&self, object: &Object) -> Option<String> {
        self.pages.href(object)
    }

    /// The page an account's name opens: a `ServiceAccount`'s own page,
    /// which is its drawing, or a group's or a user's under the
    /// accounts matrix.
    pub fn account(&self, account: &Account) -> Option<String> {
        match account {
            Account::ServiceAccount { .. } => self.pages.href(&account.object()?),
            _ => account.subject_page(),
        }
    }

    /// A namespace's page.
    pub fn namespace(&self, namespace: &str) -> Option<String> {
        Some(transect_graph::links::namespace(namespace))
    }
}

/// `items` joined the way a sentence lists them: "a and b", "a, b, and
/// c".
pub fn and(items: &[String]) -> String {
    match items {
        [] => String::new(),
        [one] => one.clone(),
        [a, b] => format!("{a} and {b}"),
        [rest @ .., last] => format!("{}, and {last}", rest.join(", ")),
    }
}
