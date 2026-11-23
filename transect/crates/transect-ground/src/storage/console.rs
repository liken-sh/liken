//! A CSI driver's console is the web interface of the system that stores
//! the driver's volumes, such as a NAS.  The driver's `CSIDriver` gives
//! it as a URL template in its `console` intent, such as the
//! `transect.liken.sh/console` annotation.  Each placeholder names a
//! volume attribute of the driver's `PersistentVolume`s or a parameter of
//! its `StorageClass`es: `https://{dsm}:5001` for a driver whose
//! `StorageClass`es give the NAS's address as `dsm`.  So nobody has to
//! write each NAS's address by hand.

use std::collections::{BTreeMap, BTreeSet, HashMap};
use std::sync::Arc;

use k8s_openapi::api::core::v1::PersistentVolume;
use k8s_openapi::api::storage::v1::StorageClass;

/// Each driver's console URLs, keyed by driver, from filling the
/// driver's template in `templates` with the attributes of each of its
/// volumes and the parameters of each of its `StorageClass`es.  Each
/// list is sorted and has no duplicates.
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

/// `template` with each `{name}` replaced by its value in `values`, or
/// `None` when a placeholder has no value, when a value has a character
/// other than a letter, a digit, or one of `-._:/`, or when the result
/// is not an `http` or `https` URL that a page may link to.
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
        Arc::new(serde_yaml_ng::from_str(&text).unwrap())
    }

    fn class(driver: &str, dsm: &str) -> Arc<StorageClass> {
        let text = format!(
            "{{metadata: {{name: sc-{dsm}}}, provisioner: {driver}, parameters: {{dsm: {dsm}}}}}"
        );
        Arc::new(serde_yaml_ng::from_str(&text).unwrap())
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
