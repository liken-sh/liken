//! Where an edge or a card line came from.  A line that does not come
//! from the API shows a mark, so a reader can tell what the cluster
//! reports from what someone wrote about it or what `transect` guessed.
//! Facts from the API, which are most of the map, show no mark.

/// The source of an edge or a card line.
#[derive(Clone, Debug, Default, PartialEq, Eq, Hash, PartialOrd, Ord)]
pub enum Provenance {
    /// Read from the Kubernetes API.
    #[default]
    Kubernetes,
    /// Written by the people who run the cluster.
    Intent(Stated),
    /// Read through a `KindKnowledge` that an operator ships.  `field`
    /// is the field of the knowledge, or the path in the object, that
    /// gave the fact.
    Kinds { knowledge: String, field: String },
    /// Read through the `DeviceKnowledge` for a DRA driver.  `attribute`
    /// is the device attribute that gave the fact, and `label` is the
    /// verb that a page shows for the edge.
    Devices {
        driver: String,
        attribute: String,
        label: String,
    },
    /// Read from the description at the top of a custom kind's CRD
    /// schema.  The operator ships it with the kind, so it takes the
    /// same mark as kind knowledge.
    Definition { crd: String },
    /// Read from an image's labels.  The image is named when the line
    /// is not shown under that image.
    Image(Option<String>),
    /// Guessed from the name of a field of a custom resource whose kind
    /// has no kind knowledge.  `field` is the field's path.
    Guess { field: String },
    /// Read from the labels and annotations that a CRD shares with a
    /// workload, which show that they were installed together.  `labels`
    /// lists each one that matched, as `key=value`.
    Package { crd: String, labels: Vec<String> },
}

/// Where a piece of intent was written.
#[derive(Clone, Debug, PartialEq, Eq, Hash, PartialOrd, Ord)]
pub enum Stated {
    /// An annotation on the object, as "Deployment web".
    Annotation { field: String, on: String },
    /// An overlay file that supplies the same field as an annotation.
    Overlay { field: String, file: String },
    /// An `Intent` resource, as "shop/the-database".
    Resource { field: String, name: String },
    /// An `Outside` resource, by its name.
    Outside { field: String, name: String },
    /// An `IngressController` resource, by its name.
    IngressController { field: String, name: String },
    /// Intent written for a `Secret`, and so for the workloads that read
    /// it.  `transect` never reads a `Secret`'s data, and reads its
    /// annotations only with `--secret-annotations`.
    Secret { stated: Box<Stated>, way: Way },
}

/// How `transect` found the intent for a `Secret`.
#[derive(Clone, Debug, PartialEq, Eq, Hash, PartialOrd, Ord)]
pub enum Way {
    /// From the object that writes the `Secret`, such as a custom
    /// resource of a secrets operator.  `secret` is the `Secret`'s name.
    Producer { secret: String },
    /// From the `Secret`'s own annotations.  `secret` is the `Secret`'s
    /// name.
    Annotations { secret: String },
}

/// The text of each mark, and its meaning in the legend.
const MARKS: [(&str, &str); 4] = [
    ("intent", "written by the people who run the cluster"),
    ("kind", "from the kind knowledge"),
    ("image", "from an image's labels"),
    ("guess", "from what a field of a custom resource is named"),
];

/// The first `STORED` marks are for text that someone stored.  A guess
/// is read from the cluster, from the name of a field, so the legend
/// does not call it stored text.
const STORED: usize = 3;

impl Provenance {
    /// The text of this fact's mark, or none when the fact has no mark.
    pub fn mark(&self) -> Option<&'static str> {
        match self {
            Self::Kubernetes | Self::Package { .. } => None,
            Self::Intent(_) => Some(MARKS[0].0),
            Self::Kinds { .. } | Self::Devices { .. } | Self::Definition { .. } => Some(MARKS[1].0),
            Self::Image(_) => Some(MARKS[2].0),
            Self::Guess { .. } => Some(MARKS[3].0),
        }
    }

    /// The hover text of the mark: what the mark means, then where the
    /// fact came from.
    pub fn hover(&self) -> String {
        let meaning = MARKS.iter().find(|(mark, _)| Some(*mark) == self.mark());
        match meaning {
            Some((_, meaning)) => format!("{} · {}", capitalized(meaning), self.title()),
            None => self.title(),
        }
    }

    /// A phrase that says where the fact came from.
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
    /// A phrase that says where the intent was written, for the hover
    /// text of a mark.
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
    /// This provenance as found through a `Secret` in the way `way`.
    /// Only intent can come through a `Secret`, so any other provenance
    /// is returned unchanged.
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

/// The provenance that a section's heading shows in place of the marks
/// on its lines, when two or more lines all have the same marked
/// provenance.  Lines from different fields of one `KindKnowledge`
/// share that knowledge, with no field.  Otherwise it returns none, and
/// each line keeps its own mark.
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

/// The provenance for the marked lines of a section that also has
/// unmarked lines, as `shared` finds it for the marked lines alone.  The
/// section shows the unmarked lines first and the marked lines after
/// them under one mark.  It returns none when every line is marked.
pub fn grouped<'a>(marks: impl IntoIterator<Item = Option<&'a Provenance>>) -> Option<Provenance> {
    let (marked, unmarked): (Vec<_>, Vec<_>) = marks
        .into_iter()
        .partition(|mark| mark.is_some_and(|mark| mark.mark().is_some()));
    if unmarked.is_empty() {
        return None;
    }
    shared(marked)
}

/// The legend line for a card whose lines have `provenances`.  It
/// explains each mark that appears, and returns none when no line has a
/// mark.
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
    fn hover_text_says_what_the_mark_means_and_where_it_came_from(
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
    fn marks_a_heading_when_every_line_shares_a_provenance(
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
    #[case::a_line_shown_under_its_image(
        Provenance::Image(None),
        Some("image"),
        "from the image's description label"
    )]
    fn marks_and_titles_each_provenance(
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
    #[case::marks_in_the_order_of_the_table(
        vec![Provenance::Image(None), annotation()],
        Some("Marked lines are stored text, not read from the cluster. intent: written by the people who run the cluster; image: from an image's labels. Hover a mark for its source.")
    )]
    #[case::a_guess_alone(
        vec![Provenance::Guess { field: "spec.a".into() }],
        Some("guess: from what a field of a custom resource is named. Hover a mark for its source.")
    )]
    fn explains_the_marks_on_a_card(
        #[case] provenances: Vec<Provenance>,
        #[case] expected: Option<&str>,
    ) {
        assert_eq!(legend(&provenances).as_deref(), expected);
    }
}
