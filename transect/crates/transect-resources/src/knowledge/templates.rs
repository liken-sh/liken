//! The fields of a kind that hold pod templates, as a `KindKnowledge`
//! states them in `templates`.  An operator makes pods from these
//! fields, and a pod mounts the tokens that its template asks for, so a
//! template says which programs the pods can call before any pod runs.
//! The pods of an agent's turn run only while the turn runs, so most of
//! the time the template is the only place to read this from.

use serde::Deserialize;

use super::Reference;
use super::references::target_of;

/// One field of the kind that holds a pod template, or a part of one,
/// in the form of a `PodTemplateSpec`.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Template {
    /// The field path, such as `spec.template`.
    pub path: String,
    /// The word that the pages show for the pods made from the field,
    /// such as `turn` or `hook`.
    pub used: String,
    /// The audiences of the tokens that the kind's controller adds to
    /// every pod that it makes from the field.
    pub injects: Vec<String>,
    /// The path of a reference of the same document, whose object's
    /// template of the same `use` the controller merges into this one.
    pub merge: Option<String>,
    /// The field that names the `ServiceAccount` that these pods run as.
    pub runs_as: Option<String>,
}

/// One entry of `templates`, as written.
#[derive(Deserialize)]
#[serde(deny_unknown_fields, rename_all = "camelCase")]
pub(super) struct Written {
    path: Option<String>,
    #[serde(rename = "use")]
    used: Option<String>,
    #[serde(default)]
    injects: Vec<String>,
    merge: Option<String>,
    runs_as: Option<String>,
}

/// The templates of `written`, checked against `references`, the
/// references of the same document.  A `merge` must be the path of an
/// entry that names a kind, because `transect` follows that reference
/// to the object whose template it merges.
pub(super) fn read(
    written: Vec<Written>,
    references: &[Reference],
) -> Result<Vec<Template>, String> {
    let read = |(at, written): (usize, Written)| {
        one(written, references).map_err(|error| format!("templates[{at}]: {error}"))
    };
    written.into_iter().enumerate().map(read).collect()
}

fn one(written: Written, references: &[Reference]) -> Result<Template, String> {
    let path = written.path.filter(|path| !path.is_empty());
    let path = path.ok_or("the entry has no path, so it names no field")?;
    let used = written.used.filter(|used| !used.is_empty());
    let used = used.ok_or("the entry has no use, the word that the pages show for its pods")?;
    if let Some(empty) = written.injects.iter().position(String::is_empty) {
        return Err(format!(
            "injects[{empty}] is empty, and an audience is a name"
        ));
    }
    if let Some(merge) = &written.merge
        && target_of(references, merge).is_none()
    {
        return Err(format!(
            "merge is {merge}, which is the path of no entry of references that names a kind"
        ));
    }
    Ok(Template {
        path,
        used,
        injects: written.injects,
        merge: written.merge,
        runs_as: written.runs_as,
    })
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::super::fixtures::knowledge;
    use super::super::parse;
    use super::*;
    use crate::resources::DocId;

    fn read(spec: &str) -> Result<Vec<Template>, String> {
        let text = format!("metadata: {{name: duties.simmer.foo}}\nspec: {spec}\n");
        let value = serde_yaml_ng::from_str(&text).unwrap();
        parse(DocId::default(), value).map(|known| known.templates)
    }

    #[rstest]
    fn reads_every_field_of_a_template() {
        let templates = read(
            "{references: [{path: spec.mandate, kind: Mandate, group: simmer.foo}], templates: [{path: spec.template, use: turn, merge: spec.mandate, injects: [simmer], runsAs: status.serviceAccount}]}",
        );
        let expected = Template {
            path: "spec.template".into(),
            used: "turn".into(),
            injects: vec!["simmer".into()],
            merge: Some("spec.mandate".into()),
            runs_as: Some("status.serviceAccount".into()),
        };
        assert_eq!(templates, Ok(vec![expected]));
    }

    #[rstest]
    #[case::no_path(
        "{templates: [{use: turn}]}",
        "templates[0]: the entry has no path, so it names no field"
    )]
    #[case::no_use(
        "{templates: [{path: spec.template}]}",
        "templates[0]: the entry has no use, the word that the pages show for its pods"
    )]
    #[case::an_empty_audience(
        "{templates: [{path: spec.template, use: turn, injects: ['']}]}",
        "templates[0]: injects[0] is empty, and an audience is a name"
    )]
    #[case::a_merge_of_no_reference(
        "{templates: [{path: spec.template, use: turn, merge: spec.mandate}]}",
        "templates[0]: merge is spec.mandate, which is the path of no entry of references that names a kind"
    )]
    #[case::a_merge_of_a_value(
        "{references: [{path: spec.mandate}], templates: [{path: a, use: turn}, {path: spec.template, use: turn, merge: spec.mandate}]}",
        "templates[1]: merge is spec.mandate, which is the path of no entry of references that names a kind"
    )]
    fn refuses_a_template_it_cannot_read(#[case] spec: &str, #[case] message: &str) {
        assert_eq!(read(spec), Err(message.into()));
    }

    #[rstest]
    #[case::templates("{templates: [{path: spec.template, use: turn}]}", true)]
    #[case::none("{summary: Runs.}", false)]
    fn a_template_needs_the_whole_object(#[case] spec: &str, #[case] reads: bool) {
        let known = knowledge("duties.simmer.foo", spec);
        assert_eq!(known.reads_fields(), reads);
    }
}
