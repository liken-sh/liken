use rstest::rstest;

use super::*;
use transect_graph::edges::Stated;
use transect_resources::intent::Line;

fn deployment(namespace: &str, name: &str) -> Object {
    Object::new("apps", "Deployment", Some(namespace), name)
}

fn hop(dependent: Dependent, lost: &[(&str, &str)]) -> Hop {
    Hop {
        namespace: None,
        dependent,
        lost: lost.iter().map(|(ns, name)| deployment(ns, name)).collect(),
        provides: Default::default(),
        declared_in: Vec::new(),
    }
}

fn caller(namespace: &str, name: &str) -> Dependent {
    Dependent::Caller(deployment(namespace, name))
}

fn player(namespace: Option<&str>, name: &str) -> Dependent {
    Dependent::Controlled(Object::new("media.example.com", "Player", namespace, name))
}

#[rstest]
#[case::a_caller_here(hop(caller("shop", "web"), &[("shop", "db")]), "web can't reach db")]
#[case::two_lost(
    hop(caller("shop", "web"), &[("shop", "cache"), ("shop", "db")]),
    "web can't reach cache and db"
)]
#[case::lost_elsewhere(hop(caller("shop", "web"), &[("data", "db")]), "web can't reach data/db")]
#[case::a_caller_elsewhere(hop(caller("blog", "feed"), &[("shop", "db")]), "blog/feed can't reach db")]
#[case::a_host(
    hop(Dependent::Host("shop.example.com".into()), &[("shop", "web")]),
    "shop.example.com stops answering"
)]
#[case::sign_in(
    hop(Dependent::SignsIn(deployment("shop", "wiki")), &[("auth", "login")]),
    "wiki loses sign-in"
)]
#[case::a_custom_resource(
    hop(player(Some("shop"), "den"), &[("shop", "operator")]),
    "Nothing reconciles Player den until operator returns"
)]
#[case::a_cluster_scoped_one(
    hop(player(None, "den"), &[("shop", "a"), ("shop", "b")]),
    "Nothing reconciles Player den until a and b return"
)]
fn writes_what_a_dependent_loses(#[case] hop: Hop, #[case] expected: &str) {
    assert_eq!(text(&hop.pieces(Some("shop"), false)), expected);
}

fn provided(text: &str) -> Line {
    Line {
        text: text.into(),
        provenance: Provenance::Intent(Stated::Annotation {
            field: "provides".into(),
            on: "Deployment db".into(),
        }),
    }
}

/// `hop` with `db` in shop providing `text`.
fn providing(hop: Hop, text: &str) -> Hop {
    let mut hop = hop;
    hop.provides
        .insert(deployment("shop", "db"), provided(text));
    hop
}

#[rstest]
#[case::a_caller(
    providing(hop(caller("shop", "web"), &[("shop", "db")]), "order storage"),
    "web loses order storage"
)]
#[case::beside_a_name(
    providing(hop(caller("blog", "feed"), &[("shop", "cache"), ("shop", "db")]), "order storage"),
    "blog/feed loses order storage and can't reach cache"
)]
#[case::a_custom_resource_names_its_controller(
    providing(hop(player(Some("shop"), "den"), &[("shop", "db")]), "order storage"),
    "Nothing reconciles Player den until db returns"
)]
fn writes_the_provides_text_of_a_lost_workload(#[case] hop: Hop, #[case] expected: &str) {
    let marks = match hop.dependent {
        Dependent::Caller(_) => vec![provided("").provenance],
        _ => vec![],
    };
    assert_eq!(
        (
            text(&hop.pieces(Some("shop"), false)),
            hop.provenances().into_iter().cloned().collect::<Vec<_>>()
        ),
        (expected.to_string(), marks)
    );
}

#[rstest]
fn a_namespaces_hops_share_its_line() {
    let hops = Hops {
        namespace: Some("shop".into()),
        hops: vec![
            hop(caller("shop", "web"), &[("shop", "db")]),
            hop(
                Dependent::Host("shop.example.com".into()),
                &[("shop", "web")],
            ),
        ],
    };
    assert_eq!(
        hops.to_string(),
        "web can't reach db; shop.example.com stops answering"
    );
}

/// A line where web provides "the web app and its database", beside a
/// dependent named database.  Each name links to its own page, and no
/// word inside the `provides` text is a link.
#[rstest]
fn links_names_and_never_words_in_what_intent_provides() {
    let database = deployment("shop", "database");
    let web = deployment("shop", "web");
    let mut calls_web = hop(caller("shop", "feed"), &[("shop", "web")]);
    calls_web
        .provides
        .insert(web.clone(), provided("the web app and its database"));
    let hops = Hops {
        namespace: Some("shop".into()),
        hops: vec![
            calls_web,
            hop(Dependent::Caller(database.clone()), &[("shop", "web")]),
        ],
    };
    let objects = [deployment("shop", "feed"), database, web];
    let linker = Linker::new(objects, |o| Some(format!("/{}", o.name)));
    let parts: Vec<(String, Option<String>)> = hops
        .parts(&linker)
        .into_iter()
        .map(|part| (part.text, part.href))
        .collect();
    let part = |text: &str, href: Option<&str>| (text.to_string(), href.map(Into::into));
    assert_eq!(
        parts,
        vec![
            part("feed", Some("/feed")),
            part(" loses the web app and its database; ", None),
            part("database", Some("/database")),
            part(" can't reach ", None),
            part("web", Some("/web")),
        ]
    );
}

#[rstest]
fn one_application_in_two_lines_is_named_without_its_kind() {
    let wiki = || deployment("shop", "wiki");
    let hops = Hops {
        namespace: Some("shop".into()),
        hops: vec![
            hop(Dependent::Caller(wiki()), &[("auth", "login")]),
            hop(Dependent::SignsIn(wiki()), &[("auth", "login")]),
        ],
    };
    assert_eq!(
        hops.to_string(),
        "wiki can't reach auth/login; wiki loses sign-in"
    );
}

#[rstest]
fn two_callers_of_one_name_are_named_by_kind() {
    let custom =
        |kind: &str| Dependent::Caller(Object::new("media.example.com", kind, Some("shop"), "den"));
    let hops = Hops {
        namespace: Some("shop".into()),
        hops: vec![
            hop(custom("Player"), &[("shop", "bus")]),
            hop(custom("Remote"), &[("shop", "bus")]),
        ],
    };
    assert_eq!(
        hops.to_string(),
        "Player den can't reach bus; Remote den can't reach bus"
    );
}

#[rstest]
#[case::something(vec![hop(caller("shop", "web"), &[("shop", "db")])], vec!["web can't reach db"], None)]
#[case::something_provided(
    vec![providing(hop(caller("shop", "web"), &[("shop", "db")]), "order storage")],
    vec!["web loses order storage"],
    None
)]
#[case::dependents_that_lose_the_same(
    vec![
        providing(hop(caller("shop", "beat"), &[("shop", "db")]), "the web app"),
        providing(hop(caller("shop", "celery"), &[("shop", "db")]), "the web app"),
        hop(caller("shop", "feed"), &[("shop", "db")]),
        providing(hop(caller("shop", "worker"), &[("shop", "db")]), "the web app"),
    ],
    vec!["beat, celery, and worker lose the web app", "feed can't reach db"],
    None
)]
#[case::a_dependent_of_the_lost_ones_name(
    vec![hop(Dependent::Caller(Object::new("media.example.com", "Player", Some("shop"), "den")), &[("shop", "den")])],
    vec!["Player den can't reach Deployment den"],
    None
)]
#[case::nothing(
    vec![],
    vec![],
    Some("Nothing in the cluster depends on it.")
)]
fn writes_the_lines_of_the_if_down_card(
    #[case] hops: Vec<Hop>,
    #[case] lines: Vec<&str>,
    #[case] nothing: Option<&str>,
) {
    let if_down = IfDown {
        namespace: Some("shop".into()),
        hops,
        linker: Default::default(),
        plurals: Default::default(),
    };
    let found: Vec<String> = if_down
        .lines()
        .iter()
        .map(|line| text(&line.pieces))
        .collect();
    assert_eq!(
        (found, if_down.nothing_text()),
        (
            lines.into_iter().map(String::from).collect::<Vec<_>>(),
            nothing
        )
    );
}

fn cluster(name: &str) -> Dependent {
    Dependent::Controlled(Object::new("db.example.com", "Cluster", Some("shop"), name))
}

#[rstest]
#[case::one_of_a_kind(&["main"], &["Nothing reconciles Cluster main until operator returns"])]
#[case::several_of_a_kind(&["a", "b", "c"], &["Nothing reconciles 3 Clusters until operator returns"])]
fn writes_one_line_for_each_controlled_kind(#[case] names: &[&str], #[case] expected: &[&str]) {
    let hops = names
        .iter()
        .map(|name| hop(cluster(name), &[("shop", "operator")]));
    let plurals = std::collections::HashMap::from([(
        ("db.example.com".to_string(), "Cluster".to_string()),
        "clusters".to_string(),
    )]);
    let if_down = IfDown {
        namespace: Some("shop".into()),
        hops: hops.collect(),
        linker: Default::default(),
        plurals,
    };
    let lines: Vec<String> = if_down
        .lines()
        .iter()
        .map(|line| text(&line.pieces))
        .collect();
    assert_eq!(lines, expected);
}

/// A card's line about a dependent named `web`, which loses "the web
/// console", the workload's `provides` text from intent.  The dependent
/// links to its page, and the word inside the `provides` text does not.
#[rstest]
fn a_cards_line_links_its_dependent_and_never_a_word() {
    let operator = deployment("shop", "operator");
    let mut calls = hop(caller("shop", "web"), &[("shop", "operator")]);
    calls.provides.insert(operator, provided("the web console"));
    let if_down = IfDown {
        namespace: Some("shop".into()),
        hops: vec![calls],
        linker: Default::default(),
        plurals: Default::default(),
    }
    .linked(|o| Some(format!("/{}", o.name)));
    let lines = if_down.lines();
    let parts: Vec<(String, Option<String>)> = if_down
        .line_parts(&lines[0])
        .into_iter()
        .map(|part| (part.text, part.href))
        .collect();
    assert_eq!(
        parts,
        [
            ("web".to_string(), Some("/web".to_string())),
            (" loses the web console".to_string(), None)
        ]
    );
}

#[rstest]
fn a_counted_line_keeps_its_place_and_links_its_kind() {
    let hops = vec![
        hop(caller("shop", "web"), &[("shop", "operator")]),
        hop(cluster("a"), &[("shop", "operator")]),
        hop(
            caller("shop", "feed"),
            &[("shop", "operator"), ("shop", "db")],
        ),
        hop(cluster("b"), &[("shop", "operator")]),
    ];
    let plurals = std::collections::HashMap::from([(
        ("db.example.com".to_string(), "Cluster".to_string()),
        "clusters".to_string(),
    )]);
    let if_down = IfDown {
        namespace: Some("shop".into()),
        hops,
        linker: Default::default(),
        plurals,
    };
    let lines = if_down.lines();
    let texts: Vec<String> = lines.iter().map(|line| text(&line.pieces)).collect();
    assert_eq!(
        texts,
        [
            "web can't reach operator",
            "Nothing reconciles 2 Clusters until operator returns",
            "feed can't reach operator and db",
        ]
    );
    let linked: Vec<(String, Option<String>)> = if_down
        .line_parts(&lines[1])
        .into_iter()
        .filter(|part| part.href.is_some())
        .map(|part| (part.text, part.href))
        .collect();
    assert_eq!(
        linked,
        [(
            "2 Clusters".to_string(),
            Some("/kinds/clusters.db.example.com".to_string())
        )]
    );
}
