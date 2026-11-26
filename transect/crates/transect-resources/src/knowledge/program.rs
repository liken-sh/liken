//! Program knowledge is a `transect` resource that describes a program
//! that a project ships: the image that it runs from, the token
//! audiences that it accepts, and the audiences that it adds to pods at
//! admission.  A program that defines no custom resource, such as a
//! webhook relay or a transcript store, has no `KindKnowledge`, so this
//! is the only knowledge of it.  The project that ships the program
//! writes it.

use serde::Deserialize;

use crate::resources::DocId;
use transect_graph::edges::Provenance;
use transect_graph::image;
use transect_graph::untrusted::capped;

/// A card shows at most this many characters of a summary.
const SUMMARY_CHARS: usize = 300;

#[derive(Clone, Debug, PartialEq)]
pub struct ProgramKnowledge {
    pub doc: DocId,
    /// The program's name, which the pages show.
    pub name: String,
    pub summary: Option<String>,
    /// The names of the images that run the program, without a tag or a
    /// digest.  `transect_graph::image::matches` compares them.
    pub images: Vec<String>,
    pub accepts: Vec<Accepts>,
    pub injects: Vec<Injects>,
}

/// An audience whose tokens the program takes.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Accepts {
    pub audience: String,
    /// The phrase of the edge from a caller to the program, such as
    /// "asks for a credential".
    pub verb: String,
    /// The kind whose objects say what the program gives to which
    /// `ServiceAccount`.
    pub grants: Option<Grants>,
}

/// The kind and the field whose objects name the `ServiceAccount`s that
/// a program gives something to.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Grants {
    pub kind: KindOf,
    /// The path of the kind's `references` entry that names the
    /// account, such as `spec.serviceAccounts[]`.
    pub path: String,
}

/// A kind by its group, empty for the core group, and its name.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct KindOf {
    pub group: String,
    pub kind: String,
}

/// An audience that the program adds at admission to each pod with a
/// label.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Injects {
    pub pod_label: String,
    pub audience: String,
    /// The kind of the object, in the pod's namespace, that the label's
    /// value names.
    pub names: Option<KindOf>,
}

#[derive(Deserialize)]
struct Document {
    metadata: Metadata,
    spec: Spec,
}

#[derive(Deserialize)]
struct Metadata {
    name: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Spec {
    summary: Option<String>,
    image: Option<Images>,
    #[serde(default)]
    accepts: Vec<WrittenAccepts>,
    #[serde(default)]
    injects: Vec<WrittenInjects>,
}

/// `image`, as one repository or a list of them.
#[derive(Deserialize)]
#[serde(untagged)]
enum Images {
    One(String),
    Many(Vec<String>),
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct WrittenAccepts {
    audience: Option<String>,
    verb: Option<String>,
    grants: Option<WrittenKind>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields, rename_all = "camelCase")]
struct WrittenInjects {
    pod_label: Option<String>,
    audience: Option<String>,
    names: Option<WrittenKind>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct WrittenKind {
    kind: String,
    group: Option<String>,
    path: Option<String>,
}

pub fn parse(doc: DocId, value: serde_yaml_ng::Value) -> Result<ProgramKnowledge, String> {
    let document: Document = serde_yaml_ng::from_value(value).map_err(|error| error.to_string())?;
    let spec = document.spec;
    let images = match spec.image {
        None => Vec::new(),
        Some(Images::One(image)) => vec![image],
        Some(Images::Many(images)) => images,
    };
    let images: Vec<String> = images.into_iter().filter(|i| !i.is_empty()).collect();
    if images.is_empty() {
        return Err("the program has no image, so transect cannot find where it runs".into());
    }
    if let Some(pinned) = images.iter().find(|entry| image::pinned(entry)) {
        return Err(format!(
            "the image {pinned} has a tag or a digest, so it would match one release only"
        ));
    }
    let accepts =
        spec.accepts.into_iter().enumerate().map(|(at, written)| {
            accepts(written).map_err(|error| format!("accepts[{at}]: {error}"))
        });
    let injects =
        spec.injects.into_iter().enumerate().map(|(at, written)| {
            injects(written).map_err(|error| format!("injects[{at}]: {error}"))
        });
    Ok(ProgramKnowledge {
        doc,
        name: document.metadata.name,
        summary: spec.summary.and_then(|text| capped(&text, SUMMARY_CHARS)),
        images,
        accepts: accepts.collect::<Result<_, _>>()?,
        injects: injects.collect::<Result<_, _>>()?,
    })
}

fn accepts(written: WrittenAccepts) -> Result<Accepts, String> {
    let audience = filled(written.audience).ok_or("the entry has no audience")?;
    let verb = filled(written.verb).ok_or("the entry has no verb, the phrase of its edge")?;
    let grants = written.grants.map(|kind| {
        let path =
            filled(kind.path).ok_or("grants has no path, the field that names the account")?;
        let kind = KindOf {
            group: kind.group.unwrap_or_default(),
            kind: kind.kind,
        };
        Ok::<_, String>(Grants { kind, path })
    });
    Ok(Accepts {
        audience,
        verb,
        grants: grants.transpose()?,
    })
}

fn injects(written: WrittenInjects) -> Result<Injects, String> {
    let pod_label = filled(written.pod_label).ok_or("the entry has no podLabel")?;
    let audience = filled(written.audience).ok_or("the entry has no audience")?;
    let names = written.names.map(|kind| match kind.path {
        Some(_) => Err("names reads the label's value, so it has no path"),
        None => Ok(KindOf {
            group: kind.group.unwrap_or_default(),
            kind: kind.kind,
        }),
    });
    Ok(Injects {
        pod_label,
        audience,
        names: names.transpose()?,
    })
}

fn filled(text: Option<String>) -> Option<String> {
    text.filter(|text| !text.is_empty())
}

impl ProgramKnowledge {
    /// Whether a container of `image` runs the program: one of `images`
    /// names it.
    pub fn runs(&self, running: &str) -> bool {
        self.images
            .iter()
            .any(|entry| image::matches(running, entry))
    }

    /// The entry of `accepts` for `audience`.
    pub fn accepting(&self, audience: &str) -> Option<&Accepts> {
        self.accepts
            .iter()
            .find(|accepts| accepts.audience == audience)
    }

    /// The provenance of an edge to the program from the template at
    /// `template`, whose token is for `audience`.
    pub fn provenance(&self, audience: &str, template: &str) -> Provenance {
        Provenance::Program {
            program: self.name.clone(),
            audience: audience.into(),
            template: template.into(),
        }
    }
}

#[cfg(test)]
mod tests;
