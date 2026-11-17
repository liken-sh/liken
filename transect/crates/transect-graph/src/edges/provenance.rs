//! Which layer of "What transect knows" a fact came from.  A fact read
//! from the API has no mark, because such facts are most of the map; a
//! fact from stored text has one, so a reader can tell what the cluster
//! reports from what someone wrote about it.

/// Where an edge or a card line came from.
#[derive(Clone, Debug, Default, PartialEq, Eq, Hash, PartialOrd, Ord)]
pub enum Provenance {
    /// Read from the Kubernetes API.
    #[default]
    Kubernetes,
    /// Written by the people who run the cluster.
    Intent(Stated),
    /// Read through the kind knowledge an operator ships: its name, and
    /// the field of it, or the field path of the object, that gave the
    /// fact.
    Kinds { knowledge: String, field: String },
    /// Read through the device knowledge a DRA driver's operator ships:
    /// the driver it is named for, the attribute that gave the fact, and
    /// the reference's label, the verb a page shows for the edge.
    Devices {
        driver: String,
        attribute: String,
        label: String,
    },
    /// Read from the description at the top of a custom kind's CRD
    /// schema, by the CRD's name, which the operator writes as it
    /// writes kind knowledge.
    Definition { crd: String },
    /// Read from an image's labels, with the image when the line is not
    /// shown under it.
    Image(Option<String>),
    /// Guessed from the name of a field of a custom resource whose kind
    /// has no knowledge, by its path.
    Guess { field: String },
    /// Read from the labels and annotations a custom kind's CRD shares
    /// with one workload, which show they were installed together: the
    /// CRD's name, and each label that matched as `key=value`.
    Package { crd: String, labels: Vec<String> },
}

/// Where a piece of intent was written.
#[derive(Clone, Debug, PartialEq, Eq, Hash, PartialOrd, Ord)]
pub enum Stated {
    /// An annotation on the object, as "Deployment web".
    Annotation { field: String, on: String },
    /// An overlay that takes the place of that annotation, by its file.
    Overlay { field: String, file: String },
    /// An `Intent` resource, as "shop/the-database".
    Resource { field: String, name: String },
    /// An `Outside` resource, by its name.
    Outside { field: String, name: String },
    /// An `IngressController` resource, by its name, which declares
    /// what its controller's own configuration does.
    IngressController { field: String, name: String },
    /// Intent written for a `Secret`, and so for the workloads that read
    /// it, with the way `transect` read it.  `transect` never reads a
    /// `Secret`'s data, and reads its annotations only when
    /// `--secret-annotations` is set.
    Secret { stated: Box<Stated>, way: Way },
}

/// Which way `transect` read the intent for a `Secret`.
#[derive(Clone, Debug, PartialEq, Eq, Hash, PartialOrd, Ord)]
pub enum Way {
    /// From the object that writes the `Secret`, such as a secrets
    /// operator's custom resource, by the `Secret`'s name.
    Producer { secret: String },
    /// From the `Secret`'s own annotations, by its name, which `transect`
    /// reads only with `--secret-annotations`.
    Annotations { secret: String },
}

/// The words a mark shows, which the legend explains.  The first three
/// are stored text; a guess is read from the cluster, but only from what
/// a field is named.
const MARKS: [(&str, &str); 4] = [
    ("intent", "written by the people who run the cluster"),
    ("kind", "from the kind knowledge"),
    ("image", "from an image's labels"),
    ("guess", "from what a field of a custom resource is named"),
];

/// How many of `MARKS` are stored text.
const STORED: usize = 3;

impl Provenance {
    /// The word the mark shows, or none for a fact that has no mark.
    pub fn mark(&self) -> Option<&'static str> {
        match self {
            Self::Kubernetes | Self::Package { .. } => None,
            Self::Intent(_) => Some(MARKS[0].0),
            Self::Kinds { .. } | Self::Devices { .. } | Self::Definition { .. } => Some(MARKS[1].0),
            Self::Image(_) => Some(MARKS[2].0),
            Self::Guess { .. } => Some(MARKS[3].0),
        }
    }

    /// The mark's title: what the mark means, then where the fact was
    /// written, so a reader who hovers a mark learns both.
    pub fn hover(&self) -> String {
        let meaning = MARKS.iter().find(|(mark, _)| Some(*mark) == self.mark());
        match meaning {
            Some((_, meaning)) => format!("{} · {}", capitalized(meaning), self.title()),
            None => self.title(),
        }
    }

    /// Where the fact came from.
    pub fn title(&self) -> String {
        match self {
            Self::Kubernetes => "from the Kubernetes API".into(),
            Self::Intent(stated) => stated.title(),
            Self::Kinds { knowledge, field } if field.is_empty() => {
                format!("from the KindKnowledge {knowledge}")
            }
            Self::Kinds { knowledge, field } => {
                format!("from {field} in the KindKnowledge {knowledge}")
            }
            Self::Devices {
                driver, attribute, ..
            } => {
                format!("from the attribute {attribute} in the DeviceKnowledge {driver}")
            }
            Self::Definition { crd } => format!("from its CRD {crd}"),
            Self::Image(Some(image)) => format!("from the description label of {image}"),
            Self::Image(None) => "from the image's description label".into(),
            Self::Guess { field } => format!("a guess from the field {field}"),
            Self::Package { crd, labels } => format!(
                "from the labels its CRD {crd} shares with the workload: {}",
                labels.join(", ")
            ),
        }
    }
}

fn capitalized(text: &str) -> String {
    let mut chars = text.chars();
    match chars.next() {
        Some(first) => first.to_uppercase().chain(chars).collect(),
        None => String::new(),
    }
}

impl Stated {
    /// Where the intent was written, for a mark's title.
    pub fn title(&self) -> String {
        match self {
            Self::Annotation { field, on } => format!("from {field} on {on}"),
            Self::Overlay { field, file } => format!("from {field} in the overlay {file}"),
            Self::Resource { field, name } => format!("from {field} in the Intent {name}"),
            Self::Outside { field, name } => format!("from {field} in the Outside {name}"),
            Self::IngressController { field, name } => {
                format!("from {field} in the IngressController {name}")
            }
            Self::Secret { stated, way } => match way {
                Way::Producer { secret } => {
                    format!("{}, through the Secret {secret} it writes", stated.title())
                }
                Way::Annotations { secret } => format!(
                    "{}, for the Secret {secret}, read with --secret-annotations",
                    stated.title()
                ),
            },
        }
    }
}

impl Provenance {
    /// This provenance for a list read for the workloads that read a
    /// `Secret`, the way `way` gives.  Only intent is read that way.
    pub fn through(self, way: Way) -> Self {
        match self {
            Self::Intent(stated) => Self::Intent(Stated::Secret {
                stated: Box::new(stated),
                way,
            }),
            other => other,
        }
    }
}

/// The mark a section's heading shows in place of its lines' marks:
/// the one that all of two or more lines have, or, for lines read through
/// one kind knowledge from different fields, that knowledge.  A section
/// with an unmarked line, or with marks from different places, keeps
/// each line's own.
pub fn shared<'a>(marks: impl IntoIterator<Item = Option<&'a Provenance>>) -> Option<Provenance> {
    let marks: Vec<&Provenance> = marks.into_iter().collect::<Option<_>>()?;
    let (first, rest) = marks.split_first()?;
    if rest.is_empty() || first.mark().is_none() {
        return None;
    }
    if rest.iter().all(|mark| mark == first) {
        return Some((*first).clone());
    }
    let Provenance::Kinds { knowledge, .. } = first else {
        return None;
    };
    let one = |mark: &&Provenance| matches!(mark, Provenance::Kinds { knowledge: k, .. } if k == knowledge);
    rest.iter().all(one).then(|| Provenance::Kinds {
        knowledge: knowledge.clone(),
        field: String::new(),
    })
}

/// The mark a group of a section's lines shows, when the section has
/// unmarked lines and two or more marked ones that share a mark: the
/// unmarked lines list first, and the marked ones follow under the one
/// mark, so it shows once in place of on every line.
pub fn grouped<'a>(marks: impl IntoIterator<Item = Option<&'a Provenance>>) -> Option<Provenance> {
    let (marked, unmarked): (Vec<_>, Vec<_>) = marks
        .into_iter()
        .partition(|mark| mark.is_some_and(|mark| mark.mark().is_some()));
    if unmarked.is_empty() {
        return None;
    }
    shared(marked)
}

/// The legend line for a card whose lines have `provenances`, which
/// states what each mark among them means, or none when no line is
/// marked.
pub fn legend<'a>(provenances: impl IntoIterator<Item = &'a Provenance>) -> Option<String> {
    let shown: Vec<&str> = provenances
        .into_iter()
        .filter_map(Provenance::mark)
        .collect();
    let meanings: Vec<String> = MARKS
        .iter()
        .filter(|(mark, _)| shown.contains(mark))
        .map(|(mark, meaning)| format!("{mark}: {meaning}"))
        .collect();
    let stored = MARKS[..STORED].iter().any(|(mark, _)| shown.contains(mark));
    let lead = match stored {
        true => "Marked lines are stored text, not read from the cluster. ",
        false => "",
    };
    (!meanings.is_empty()).then(|| {
        format!(
            "{lead}{}. Hover a mark for its source.",
            meanings.join("; ")
        )
    })
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    #[rstest]
    #[case::intent(
        Provenance::Intent(Stated::Annotation { field: "why".into(), on: "Deployment web".into() }),
        "Written by the people who run the cluster · from why on Deployment web"
    )]
    #[case::the_api(Provenance::Kubernetes, "from the Kubernetes API")]
    fn its_title_says_what_the_mark_means_and_where(
        #[case] provenance: Provenance,
        #[case] hover: &str,
    ) {
        assert_eq!(provenance.hover(), hover);
    }

    fn kind(field: &str) -> Provenance {
        Provenance::Kinds {
            knowledge: "widgets.widgets.example.com".into(),
            field: field.into(),
        }
    }

    #[rstest]
    #[case::one_line(vec![Some(kind("spec.a"))], None)]
    #[case::fields_of_one_knowledge(
        vec![Some(kind("spec.a")), Some(kind("status.b"))],
        Some("from the KindKnowledge widgets.widgets.example.com")
    )]
    #[case::the_same_annotation(vec![Some(annotation()), Some(annotation())], Some("from why on Deployment web"))]
    #[case::an_unmarked_line(vec![Some(kind("spec.a")), None], None)]
    #[case::kubernetes_lines(vec![Some(Provenance::Kubernetes), Some(Provenance::Kubernetes)], None)]
    #[case::different_places(vec![Some(kind("spec.a")), Some(annotation())], None)]
    fn marks_a_heading_when_every_line_shares_a_mark(
        #[case] marks: Vec<Option<Provenance>>,
        #[case] title: Option<&str>,
    ) {
        let found = shared(marks.iter().map(Option::as_ref));
        assert_eq!(found.map(|p| p.title()).as_deref(), title);
    }

    #[rstest]
    #[case::marked_beside_unmarked(
        vec![None, Some(kind("spec.a")), Some(kind("status.b"))],
        Some("from the KindKnowledge widgets.widgets.example.com")
    )]
    #[case::every_line_marked(vec![Some(kind("spec.a")), Some(kind("status.b"))], None)]
    #[case::one_marked(vec![None, Some(kind("spec.a"))], None)]
    #[case::different_places(vec![None, Some(kind("spec.a")), Some(annotation())], None)]
    fn groups_marked_lines_beside_unmarked_ones(
        #[case] marks: Vec<Option<Provenance>>,
        #[case] title: Option<&str>,
    ) {
        let found = grouped(marks.iter().map(Option::as_ref));
        assert_eq!(found.map(|p| p.title()).as_deref(), title);
    }

    fn annotation() -> Provenance {
        Provenance::Intent(Stated::Annotation {
            field: "why".into(),
            on: "Deployment web".into(),
        })
    }

    #[rstest]
    #[case::kubernetes(Provenance::Kubernetes, None, "from the Kubernetes API")]
    #[case::an_annotation(annotation(), Some("intent"), "from why on Deployment web")]
    #[case::an_overlay(
        Provenance::Intent(Stated::Overlay { field: "gotcha".into(), file: "overlays/web.yaml".into() }),
        Some("intent"),
        "from gotcha in the overlay overlays/web.yaml"
    )]
    #[case::an_intent_resource(
        Provenance::Intent(Stated::Resource { field: "docs".into(), name: "shop/db".into() }),
        Some("intent"),
        "from docs in the Intent shop/db"
    )]
    #[case::kind_knowledge(
        Provenance::Kinds { knowledge: "widgets.widgets.example.com".into(), field: "controller.image".into() },
        Some("kind"),
        "from controller.image in the KindKnowledge widgets.widgets.example.com"
    )]
    #[case::an_outside(
        Provenance::Intent(Stated::Outside { field: "why".into(), name: "the-nas".into() }),
        Some("intent"),
        "from why in the Outside the-nas"
    )]
    #[case::through_a_producer(
        annotation().through(Way::Producer { secret: "db-login".into() }),
        Some("intent"),
        "from why on Deployment web, through the Secret db-login it writes"
    )]
    #[case::from_a_secrets_annotations(
        annotation().through(Way::Annotations { secret: "db-login".into() }),
        Some("intent"),
        "from why on Deployment web, for the Secret db-login, read with --secret-annotations"
    )]
    #[case::kubernetes_is_not_read_through_a_secret(
        Provenance::Kubernetes.through(Way::Producer { secret: "db-login".into() }),
        None,
        "from the Kubernetes API"
    )]
    #[case::an_image(Provenance::Image(Some("web:1.2".into())), Some("image"), "from the description label of web:1.2")]
    #[case::a_guess(
        Provenance::Guess { field: "spec.serviceName".into() },
        Some("guess"),
        "a guess from the field spec.serviceName"
    )]
    #[case::the_image_above(
        Provenance::Image(None),
        Some("image"),
        "from the image's description label"
    )]
    fn marks_stored_text(
        #[case] provenance: Provenance,
        #[case] mark: Option<&str>,
        #[case] title: &str,
    ) {
        assert_eq!(
            (provenance.mark(), provenance.title().as_str()),
            (mark, title)
        );
    }

    #[rstest]
    #[case::nothing_marked(vec![Provenance::Kubernetes], None)]
    #[case::an_image_once(
        vec![Provenance::Image(None), Provenance::Kubernetes, Provenance::Image(None)],
        Some("Marked lines are stored text, not read from the cluster. image: from an image's labels. Hover a mark for its source.")
    )]
    #[case::both_in_the_legends_order(
        vec![Provenance::Image(None), annotation()],
        Some("Marked lines are stored text, not read from the cluster. intent: written by the people who run the cluster; image: from an image's labels. Hover a mark for its source.")
    )]
    #[case::a_guess_alone(
        vec![Provenance::Guess { field: "spec.a".into() }],
        Some("guess: from what a field of a custom resource is named. Hover a mark for its source.")
    )]
    fn explains_the_marks_a_card_shows(
        #[case] provenances: Vec<Provenance>,
        #[case] expected: Option<&str>,
    ) {
        assert_eq!(legend(&provenances).as_deref(), expected);
    }
}
