//! The links of an access page. Each name links to the object's own
//! page, and `carry_access` in `web::carry` then rewrites the link in
//! the rendered page to the object's access page when it has one.

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

    /// `object`'s page. A `ServiceAccount`'s own page is its account
    /// page, which shows who may run as it and what it may do.
    pub fn href(&self, object: &Object) -> Option<String> {
        self.pages.href(object)
    }

    /// The page that an account's name opens: a `ServiceAccount`'s own
    /// page, or a group's or user's page under `/access/accounts`.
    pub fn account(&self, account: &Account) -> Option<String> {
        match account {
            Account::ServiceAccount { .. } => self.pages.href(&account.object()?),
            _ => account.subject_page(),
        }
    }

    pub fn namespace(&self, namespace: &str) -> Option<String> {
        Some(transect_graph::links::namespace(namespace))
    }
}

/// Joins `items` as an English list: "a and b", or "a, b, and c".
pub fn and(items: &[String]) -> String {
    match items {
        [] => String::new(),
        [one] => one.clone(),
        [a, b] => format!("{a} and {b}"),
        [rest @ .., last] => format!("{}, and {last}", rest.join(", ")),
    }
}
