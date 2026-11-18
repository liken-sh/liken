use std::collections::HashMap;

use rstest::rstest;

use super::*;
use transect_graph::edges::Object;
use transect_ground::devices::knowledge::fixtures::knowledge;

fn device(driver: &str, name: &str) -> Device {
    Device {
        driver: driver.into(),
        pool: "alpha".into(),
        name: name.into(),
        machine: Some("alpha".into()),
    }
}

/// A radio with a product name, and a GPU whose render node and display
/// node have the same PCI bus ID.
fn attributes() -> HashMap<String, Vec<(String, String)>> {
    let pairs = |pairs: &[(&str, &str)]| -> Vec<(String, String)> {
        pairs
            .iter()
            .map(|(k, v)| (k.to_string(), v.to_string()))
            .collect()
    };
    HashMap::from([
        (
            "usb-1".to_string(),
            pairs(&[("bus", "usb"), ("product", "A USB radio")]),
        ),
        (
            "card-render".to_string(),
            pairs(&[("product", "A GPU"), (PCI_BUS_ID, "0000:01:00.0")]),
        ),
        (
            "card-display".to_string(),
            pairs(&[("product", "A GPU"), (PCI_BUS_ID, "0000:01:00.0")]),
        ),
    ])
}

/// The drivers that `by_driver` builds from `listed`, using `known` as
/// the knowledge.
fn read(listed: &[(&str, &str, bool)], known: &[DeviceKnowledge]) -> Vec<Driver> {
    let devices: Vec<Device> = listed.iter().map(|(d, n, _)| device(d, n)).collect();
    let tuner = Object::new("resource.k8s.io", "ResourceClaim", Some("shop"), "tuner");
    let published = devices
        .iter()
        .zip(listed)
        .map(|(device, (_, _, held))| Published {
            device,
            claims: held.then(|| tuner.clone()).into_iter().collect(),
        });
    read_published(published.collect(), known)
}

/// The drivers that `by_driver` builds from `published`, using `known`
/// as the knowledge.
fn read_published(published: Vec<Published>, known: &[DeviceKnowledge]) -> Vec<Driver> {
    let attributes = attributes();
    let none: &[(String, String)] = &[];
    let attributes = |device: &Device| -> &[(String, String)] {
        attributes.get(&device.name).map_or(none, Vec::as_slice)
    };
    let names = |_: &Device| Vec::new();
    let href = |claim: &Object| Some(format!("/claims/{}", claim.name));
    let read = Read {
        attributes: &attributes,
        knowledge: known,
        referenced: &names,
        href: &href,
    };
    by_driver(published, &read)
}

/// Two claims that share one device are both listed as its holders,
/// sorted by name, in whatever order the claims were read.
#[rstest]
fn lists_every_claim_that_holds_a_shared_device() {
    let card = device("gpu.example.com", "card-0");
    let claim = |name: &str| Object::new("resource.k8s.io", "ResourceClaim", Some("shop"), name);
    let published = vec![Published {
        device: &card,
        claims: vec![claim("scaler"), claim("player")],
    }];
    let drivers = read_published(published, &[]);
    let holders: Vec<&str> = drivers[0].devices[0]
        .claims
        .iter()
        .map(|claim| claim.text.as_str())
        .collect();
    assert_eq!(holders, vec!["shop/player", "shop/scaler"]);
}

/// For each piece of hardware, its title, its other parts, and its
/// facts.
fn shown(drivers: &[Driver]) -> Vec<(String, Vec<String>, String)> {
    let hardware = drivers.iter().flat_map(|driver| &driver.devices);
    hardware
        .map(|h| (h.title.clone(), h.parts.clone(), h.facts_line()))
        .collect()
}

#[rstest]
fn titles_a_device_by_its_name_and_lists_every_attribute_with_no_knowledge() {
    let drivers = read(&[("radio.example.com", "usb-1", false)], &[]);
    assert_eq!(
        shown(&drivers),
        [(
            "usb-1".into(),
            vec![],
            "bus usb · product A USB radio".into()
        )]
    );
}

#[rstest]
fn titles_and_describes_a_device_as_its_knowledge_says() {
    let known = knowledge(
        "radio.example.com",
        "{title: product, facts: [{attribute: bus, label: on}]}",
    );
    let drivers = read(&[("radio.example.com", "usb-1", false)], &[known]);
    assert_eq!(
        shown(&drivers),
        [("A USB radio".into(), vec![], "on usb".into())]
    );
}

#[rstest]
fn draws_the_devices_that_share_a_pci_bus_id_as_one() {
    let known = knowledge("gpu.example.com", "{title: product}");
    let listed = [
        ("gpu.example.com", "card-display", false),
        ("gpu.example.com", "card-render", true),
    ];
    let drivers = read(&listed, &[known]);
    let held: Vec<Option<String>> = drivers[0]
        .devices
        .iter()
        .flat_map(|h| h.claims.iter().map(|c| c.href.clone()))
        .collect();
    assert_eq!(
        (shown(&drivers), held),
        (
            vec![("A GPU".into(), vec!["card-render".into()], String::new())],
            vec![Some("/claims/tuner".into())]
        )
    );
}

#[rstest]
#[case::a_few(6, false)]
#[case::more_than_a_box_shows(7, true)]
fn folds_the_hardware_no_claim_holds_past_a_few(#[case] count: usize, #[case] folds: bool) {
    let names: Vec<String> = (0..count).map(|n| format!("card-{n}")).collect();
    let listed: Vec<(&str, &str, bool)> = names
        .iter()
        .map(|name| ("gpu.example.com", name.as_str(), false))
        .collect();
    assert_eq!(read(&listed, &[])[0].folds(), folds);
}

#[rstest]
#[case::by_its_knowledge("{title: product}", true)]
#[case::by_an_attribute_it_lacks("{title: model}", false)]
#[case::by_its_name("{}", false)]
fn says_whether_its_knowledge_titled_a_device(#[case] known: &str, #[case] titled: bool) {
    let known = knowledge("radio.example.com", known);
    let drivers = read(&[("radio.example.com", "usb-1", false)], &[known]);
    assert_eq!(drivers[0].devices[0].titled, titled);
}

#[rstest]
#[case::one_unheld(&[("card-0", false)], "1 device · none held")]
#[case::some_held(&[("card-0", true), ("card-1", false), ("card-2", true)], "3 devices · 2 held")]
#[case::shared_hardware_counts_once(&[("card-render", true), ("card-display", false)], "1 device · 1 held")]
fn counts_a_drivers_hardware_and_what_is_held(
    #[case] listed: &[(&str, bool)],
    #[case] count: &str,
) {
    let listed: Vec<(&str, &str, bool)> = listed
        .iter()
        .map(|(name, held)| ("gpu.example.com", *name, *held))
        .collect();
    assert_eq!(read(&listed, &[])[0].count(), count);
}

#[rstest]
#[case::a_few(6, false)]
#[case::more_than_a_column(7, true)]
fn spans_the_box_with_more_hardware_shown_than_one_column_holds(
    #[case] held: usize,
    #[case] wide: bool,
) {
    let names: Vec<String> = (0..held).map(|n| format!("card-{n}")).collect();
    let listed: Vec<(&str, &str, bool)> = names
        .iter()
        .map(|name| ("gpu.example.com", name.as_str(), true))
        .collect();
    assert_eq!(read(&listed, &[])[0].wide(), wide);
}

/// A piece of hardware's references are grouped by verb, in the order
/// that the verbs first appear, and each target appears once.
#[rstest]
fn groups_references_by_verb_without_duplicates() {
    let mut drivers = read(&[("audio.example.com", "hdmi-1", false)], &[]);
    let named = |text: &str| Reference {
        text: text.into(),
        href: None,
    };
    let hardware = &mut drivers[0].devices[0];
    hardware.referenced = vec![
        ("speakers of".into(), named("card-display")),
        ("pairs through".into(), named("radio")),
        ("speakers of".into(), named("card-render")),
        ("speakers of".into(), named("card-display")),
    ];
    let gathered: Vec<(&str, Vec<&str>)> = hardware
        .references()
        .into_iter()
        .map(|(verb, things)| (verb, things.iter().map(|t| t.text.as_str()).collect()))
        .collect();
    assert_eq!(
        gathered,
        vec![
            ("speakers of", vec!["card-display", "card-render"]),
            ("pairs through", vec!["radio"]),
        ]
    );
}
