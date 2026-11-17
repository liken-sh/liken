//! The words a page names an object by.  A page writes a namespaced
//! object as kubectl writes it, `namespace/name`, and drops its
//! namespace on a page about that namespace or about an object in it,
//! where the reader already knows it.  A cluster-scoped object has only
//! its name.

use crate::edges::{Object, kind_and_name};

/// `object`'s name, with its namespace before it when that is not
/// `here`: "web", "shop/web".
pub fn name_in(object: &Object, here: Option<&str>) -> String {
    match object.namespace.as_deref() {
        Some(namespace) if Some(namespace) != here => format!("{namespace}/{}", object.name),
        _ => object.name.clone(),
    }
}

/// `object`'s kind and name, with its namespace before its name when
/// that is not `here`: "Deployment web", "Deployment shop/web".
pub fn kind_and_name_in(object: &Object, here: Option<&str>) -> String {
    kind_and_name(&object.kind, &name_in(object, here))
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    #[rstest]
    #[case::in_another_namespace(Some("shop"), Some("ops"), "shop/web", "Deployment shop/web")]
    #[case::here(Some("shop"), Some("shop"), "web", "Deployment web")]
    #[case::on_a_page_of_no_namespace(Some("shop"), None, "shop/web", "Deployment shop/web")]
    #[case::cluster_scoped(None, Some("shop"), "web", "Deployment web")]
    fn names_an_object(
        #[case] namespace: Option<&str>,
        #[case] here: Option<&str>,
        #[case] name: &str,
        #[case] kinded: &str,
    ) {
        let object = Object::new("apps", "Deployment", namespace, "web");
        assert_eq!(
            (name_in(&object, here), kind_and_name_in(&object, here)),
            (name.into(), kinded.into())
        );
    }

    #[test]
    fn says_a_name_that_repeats_its_kind_once_when_here() {
        let object = Object::new("example.io", "Catalog", Some("media"), "catalog");
        assert_eq!(
            (
                kind_and_name_in(&object, Some("media")),
                kind_and_name_in(&object, None)
            ),
            ("Catalog".into(), "Catalog media/catalog".into())
        );
    }
}
