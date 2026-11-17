//! A CSI driver's console: the web page of the system that stores the
//! driver's volumes, such as a NAS's interface.  The driver's publisher
//! writes it as a `console` URL template on the driver's `CSIDriver`.
//! Each placeholder names a volume attribute of the driver's
//! PersistentVolumes or a parameter of its StorageClasses, such as
//! `https://{dsm}:5001` for a driver whose StorageClasses name their
//! NAS as `dsm`.  So transect reads the console's address from the
//! cluster, and no one writes it by hand.

use std::collections::{BTreeMap, BTreeSet, HashMap};
use std::sync::Arc;

use k8s_openapi::api::core::v1::PersistentVolume;
use k8s_openapi::api::storage::v1::StorageClass;

/// Each driver's console URLs, keyed by the driver.  This function fills
/// `templates` from the attributes of each of the driver's volumes and
/// the parameters of each of its StorageClasses.  Every URL is a plain
/// `http` or `https` URL, and each list is sorted.
pub fn consoles(
    templates: &HashMap<String, String>,
    volumes: &[Arc<PersistentVolume>],
    classes: &[Arc<StorageClass>],
) -> HashMap<String, Vec<String>> {
    let mut found: HashMap<String, BTreeSet<String>> = HashMap::new();
    let mut add = |driver: &str, values: &BTreeMap<String, String>| {
        let Some(template) = templates.get(driver) else {
            return;
        };
        if let Some(url) = fill(template, values) {
            found.entry(driver.to_string()).or_default().insert(url);
        }
    };
    for volume in volumes {
        let csi = volume.spec.as_ref().and_then(|spec| spec.csi.as_ref());
        if let Some(csi) = csi {
            add(
                &csi.driver,
                &csi.volume_attributes.clone().unwrap_or_default(),
            );
        }
    }
    for class in classes {
        add(
            &class.provisioner,
            &class.parameters.clone().unwrap_or_default(),
        );
    }
    found
        .into_iter()
        .map(|(driver, urls)| (driver, urls.into_iter().collect()))
        .collect()
}

/// `template` with each `{name}` replaced by `values`' value for it.
/// The function returns `None` when a placeholder has no value, when a
/// value contains a character that a host or a path would not, or when
/// the result is not a URL that a page may link to.
fn fill(template: &str, values: &BTreeMap<String, String>) -> Option<String> {
    let mut url = String::new();
    let mut rest = template;
    while let Some(open) = rest.find('{') {
        let close = open + rest[open..].find('}')?;
        let value = values.get(&rest[open + 1..close])?;
        let plain = |c: char| c.is_ascii_alphanumeric() || "-._:/".contains(c);
        if value.is_empty() || !value.chars().all(plain) {
            return None;
        }
        url.push_str(&rest[..open]);
        url.push_str(value);
        rest = &rest[close + 1..];
    }
    url.push_str(rest);
    transect_graph::untrusted::link(&url)
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    fn values(pairs: &[(&str, &str)]) -> BTreeMap<String, String> {
        pairs
            .iter()
            .map(|(k, v)| (k.to_string(), v.to_string()))
            .collect()
    }

    #[rstest]
    #[case::one_placeholder("https://{dsm}:5001", &[("dsm", "nas.example")], Some("https://nas.example:5001"))]
    #[case::two("https://{host}/{share}", &[("host", "nas"), ("share", "media")], Some("https://nas/media"))]
    #[case::a_missing_value("https://{dsm}:5001", &[("location", "/volume1")], None)]
    #[case::a_value_that_would_end_the_url("https://{dsm}", &[("dsm", "nas\" onclick=x")], None)]
    #[case::not_http("javascript:{dsm}", &[("dsm", "nas")], None)]
    fn fills_a_template(
        #[case] template: &str,
        #[case] pairs: &[(&str, &str)],
        #[case] expected: Option<&str>,
    ) {
        assert_eq!(fill(template, &values(pairs)).as_deref(), expected);
    }

    fn volume(driver: &str, dsm: &str) -> Arc<PersistentVolume> {
        let text = format!(
            "{{metadata: {{name: pv-{dsm}}}, spec: {{csi: {{driver: {driver}, volumeHandle: a, volumeAttributes: {{dsm: {dsm}}}}}}}}}"
        );
        Arc::new(serde_yaml::from_str(&text).unwrap())
    }

    fn class(driver: &str, dsm: &str) -> Arc<StorageClass> {
        let text = format!(
            "{{metadata: {{name: sc-{dsm}}}, provisioner: {driver}, parameters: {{dsm: {dsm}}}}}"
        );
        Arc::new(serde_yaml::from_str(&text).unwrap())
    }

    #[rstest]
    fn gathers_each_drivers_urls_from_its_volumes_and_classes() {
        let templates =
            HashMap::from([("nas.example".to_string(), "https://{dsm}:5001".to_string())]);
        let volumes = [volume("nas.example", "a"), volume("disk.example", "b")];
        let classes = [class("nas.example", "a"), class("nas.example", "c")];
        let found = consoles(&templates, &volumes, &classes);
        assert_eq!(
            found,
            HashMap::from([(
                "nas.example".to_string(),
                vec!["https://a:5001".to_string(), "https://c:5001".to_string()]
            )])
        );
    }
}
