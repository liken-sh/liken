use rstest::rstest;

use super::*;
use transect_graph::edges::{ClusterScoped, Endpoint, Object, Plurals};
use transect_resources::knowledge::{self, Names, Reached, Relation, fixtures::knowledge};

const SPEC: &str = "{references: [
    {path: 'spec.dials[].name', kind: Dial, group: widgets.example.com, label: controlled by},
    {path: status.screen.monitor, label: shows on},
    {path: status.amplifier.name, kind: Amplifier, group: amps.example.com, label: amplified by},
    {path: status.bus.address, address: true, label: talks over},
    {path: status.screen.node, kind: Node, edge: runs on}]}";

const JUKEBOX: &str = "
spec:
  dials: [{name: hall}, {name: den}]
status:
  screen: {node: alpha, monitor: tv-1}
  bus: {address: 'broker.messages.svc:1883'}
";

fn jukebox() -> Object {
    Object::new("widgets.example.com", "Jukebox", Some("shop"), "front-room")
}

fn plurals() -> Plurals {
    Plurals::from([
        (
            ("widgets.example.com".to_string(), "Jukebox".to_string()),
            "jukeboxes".to_string(),
        ),
        (
            ("widgets.example.com".to_string(), "Dial".to_string()),
            "dials".to_string(),
        ),
    ])
}

/// A fake of what each relation resolves to. The broker's address
/// resolves to the Deployment behind it, the value "alpha" to the
/// machine of that name, any other named object to itself, and the
/// rest to nothing.
fn reach(relation: &Relation) -> Reached {
    let ends = match (&relation.names, relation.value.as_str()) {
        (Names::Address, _) => vec![Endpoint::Object(Object::new(
            "apps",
            "Deployment",
            Some("messages"),
            "broker",
        ))],
        (Names::Object(_), "alpha") => vec![Endpoint::Machine("alpha".into())],
        (Names::Object(object), _) => vec![Endpoint::Object(object.clone())],
        (Names::Value | Names::Ambiguous(_), _) => vec![],
    };
    Reached {
        ends,
        guessed: false,
    }
}

/// The page of the Jukebox above. Dials and the broker's Deployment
/// have pages, and nothing else does.
fn board(card: Option<WorkloadCard>) -> KnownPage {
    let known = knowledge("jukeboxes.widgets.example.com", SPEC);
    let data = serde_yaml_ng::from_str(JUKEBOX).unwrap();
    let relations = knowledge::relations(
        &known,
        &jukebox(),
        &data,
        &ClusterScoped::default(),
        &knowledge::Matches::default(),
    );
    let plurals = plurals();
    let page = |object: &Object| {
        ["Dial", "Deployment"]
            .contains(&object.kind.as_str())
            .then(|| transect_graph::links::page(object, &plurals))
            .flatten()
    };
    let board = Board {
        subject: super::Subject {
            object: jukebox(),
            knowledge: &known,
            reached: relations.iter().map(|r| (r, reach(r))).collect(),
            conditions: Vec::new(),
        },
        layer: "apps".into(),
        path: "/namespaces/shop/jukeboxes.widgets.example.com/front-room".into(),
        elevation: None,
        column: Vec::new(),
        counts: Vec::new(),
        chips: Vec::new(),
        level: Level::Healthy,
        resting: None,
        plurals: &plurals,
        page: &page,
        ground: &|title| Some(format!("/ground/{title}")),
        device: &|device| device.short(),
        standing: &|_| Standing::Plain,
        readings: &|_, _| Vec::new(),
        subjects: &|_| None,
        named_by: &|_, _| Vec::new(),
        controller: &|_| None,
        top: &|object| object.clone(),
        owned: &|_, _| Vec::new(),
        presents: &crate::presents::Presents::default(),
        data: &serde_json::Value::Null,
        logs: &|_| None,
        ends: &transect_resources::knowledge::fixtures::Nowhere,
        judged: None,
    };
    let object_card = crate::objects::ObjectCard {
        kind: "Jukebox".into(),
        name: "front-room".into(),
        level: Some(Level::Healthy),
        resting: false,
        why: None,
        controllers: Vec::new(),
        code: Vec::new(),
        known: Some(KnownThrough::of(&known, None)),
        facts: Vec::new(),
    };
    KnownPage::build(board, card, object_card)
}

type Shown<'a> = (
    &'a str,
    &'a str,
    Vec<(&'a str, Option<&'a str>, Option<&'a str>)>,
    &'a str,
);

#[rstest]
fn draws_a_box_for_each_reference_with_what_its_field_names() {
    let page = board(None);
    let boxes: Vec<Shown> = page
        .boxes
        .iter()
        .map(|b| {
            let ends = b.ends.iter().map(|end| {
                (
                    end.reference.text.as_str(),
                    end.reference.href.as_deref(),
                    end.under.as_deref(),
                )
            });
            (b.label.as_str(), b.arrow, ends.collect(), b.source.as_str())
        })
        .collect();
    let dial = "/namespaces/shop/dials.widgets.example.com/";
    assert_eq!(
        boxes,
        vec![
            (
                "controlled by",
                "↖",
                vec![
                    ("Dial hall", Some(format!("{dial}hall").as_str()), None),
                    ("Dial den", Some(format!("{dial}den").as_str()), None),
                ],
                "from spec.dials[].name"
            ),
            (
                "shows on",
                "↑",
                vec![("tv-1", None, Some("not an object in the cluster"))],
                "from status.screen.monitor"
            ),
            ("amplified by", "↗", vec![], "from status.amplifier.name"),
            (
                "talks over",
                "←",
                vec![(
                    "broker.messages.svc:1883",
                    Some("/namespaces/messages/deployments/broker"),
                    Some("reaches Deployment messages/broker")
                )],
                "from status.bus.address"
            ),
            (
                "runs on",
                "→",
                vec![(
                    "Node alpha",
                    Some("/machines/alpha"),
                    Some("reaches machine alpha")
                )],
                "from status.screen.node"
            ),
        ]
    );
}

#[rstest]
fn links_each_object_an_end_reaches() {
    let page = board(None);
    let reached: Vec<(&str, Option<&str>)> = page
        .boxes
        .iter()
        .flat_map(|b| &b.ends)
        .flat_map(|end| &end.reaches)
        .map(|reached| (reached.text.as_str(), reached.href.as_deref()))
        .collect();
    assert_eq!(
        reached,
        [
            (
                "Deployment messages/broker",
                Some("/namespaces/messages/deployments/broker")
            ),
            ("machine alpha", Some("/machines/alpha")),
        ]
    );
}

#[rstest]
#[case::a_workload_shows_its_workload_card_and_section_tab(
    true,
    Some("/namespaces/shop/jukeboxes.widgets.example.com/front-room/section"),
    false
)]
#[case::an_object_with_no_pods_shows_its_object_card(false, None, true)]
fn opens_its_card_by_whether_it_runs_pods(
    #[case] workload: bool,
    #[case] section: Option<&str>,
    #[case] known: bool,
) {
    let card =
        workload.then(|| crate::cards::workload::fixtures::workload_card(Some((1, 1)), None));
    let page = board(card);
    assert_eq!(
        (
            page.tabs.as_ref().map(|tabs| tabs.tabs[1].href.as_str()),
            page.known.is_some(),
            page.card.is_some()
        ),
        (section, known, workload)
    );
}

#[rstest]
fn draws_each_kind_of_line_its_own_way() {
    let page = board(None);
    let classes: Vec<&str> = page.boxes.iter().map(|b| b.line_class).collect();
    assert_eq!(
        classes,
        vec![
            "section-path section-path-dashed",
            "section-path known-path-value",
            "section-path section-path-dashed",
            "section-path section-path-dashed",
            "section-path",
        ]
    );
}
