//! The audiences that the pod templates of an object present, and the
//! programs that accept them. A template mounts a projected
//! `ServiceAccount` token for an audience, such as `lanyard`, and the
//! program that receives the token checks it with a `TokenReview`. So the
//! audiences say which programs the pods of the template call, even
//! while no pod runs.
//!
//! The pages read a `Presents` and draw it: the lanes that a sequence
//! adds, and the columns of the page of a kind. They do not read the
//! templates or the knowledge of programs themselves, so the code that
//! finds the audiences can change without a change to the drawings.

use transect_graph::edges::Object;

/// What the templates of one object present.
#[derive(Clone, Debug, Default, PartialEq)]
pub struct Presents {
    /// The audiences that the templates present, each once.
    pub audiences: Vec<String>,
    /// Each program that accepts one of the audiences.
    pub accepting: Vec<Accepting>,
}

/// One program that accepts an audience.
#[derive(Clone, Debug, PartialEq)]
pub struct Accepting {
    pub audience: String,
    /// The program's name, which the pages show.
    pub program: String,
    /// What a caller does with the token, such as "asks for a
    /// credential".
    pub verb: String,
    /// The workloads that run the program.
    pub runs: Vec<Object>,
}

impl Presents {
    /// The programs that accept `audience`, in the order of their
    /// names.
    pub fn accepting(&self, audience: &str) -> Vec<&Accepting> {
        let mut found: Vec<&Accepting> = self
            .accepting
            .iter()
            .filter(|accepting| accepting.audience == audience)
            .collect();
        found.sort_by(|a, b| a.program.cmp(&b.program));
        found
    }
}

/// One column of the objects of a kind: an audience that the templates
/// of any of the objects present.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Column {
    /// The names of the programs that accept the audience, or the
    /// audience itself when no loaded knowledge accepts it.
    pub label: String,
    pub audience: String,
    /// Whether a loaded program accepts the audience.
    pub known: bool,
}

/// The columns for the objects whose templates present `each`: one for
/// each audience that any of them presents, in the order of the
/// audiences' names.
pub fn columns(each: &[&Presents]) -> Vec<Column> {
    let mut audiences: Vec<&str> = each
        .iter()
        .flat_map(|presents| presents.audiences.iter().map(String::as_str))
        .collect();
    audiences.sort_unstable();
    audiences.dedup();
    let column = |audience: &str| {
        let mut programs: Vec<&str> = each
            .iter()
            .flat_map(|presents| presents.accepting(audience))
            .map(|accepting| accepting.program.as_str())
            .collect();
        programs.sort_unstable();
        programs.dedup();
        Column {
            label: match programs.is_empty() {
                true => audience.to_string(),
                false => programs.join(", "),
            },
            audience: audience.to_string(),
            known: !programs.is_empty(),
        }
    };
    audiences.into_iter().map(column).collect()
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    fn accepting(audience: &str, program: &str) -> Accepting {
        Accepting {
            audience: audience.into(),
            program: program.into(),
            verb: "calls".into(),
            runs: Vec::new(),
        }
    }

    fn presents(audiences: &[&str], accepting: Vec<Accepting>) -> Presents {
        Presents {
            audiences: audiences.iter().map(|a| a.to_string()).collect(),
            accepting,
        }
    }

    fn column(label: &str, audience: &str, known: bool) -> Column {
        Column {
            label: label.into(),
            audience: audience.into(),
            known,
        }
    }

    #[rstest]
    #[case::none(vec![], vec![])]
    #[case::each_audience_once_by_name(
        vec![
            presents(&["shed", "gate"], vec![accepting("gate", "latch")]),
            presents(&["gate"], vec![accepting("gate", "latch")]),
        ],
        vec![column("latch", "gate", true), column("shed", "shed", false)]
    )]
    #[case::two_programs_of_one_audience(
        vec![presents(&["gate"], vec![accepting("gate", "latch"), accepting("gate", "hinge")])],
        vec![column("hinge, latch", "gate", true)]
    )]
    fn makes_a_column_for_each_audience_that_any_object_presents(
        #[case] each: Vec<Presents>,
        #[case] expected: Vec<Column>,
    ) {
        let each: Vec<&Presents> = each.iter().collect();
        assert_eq!(columns(&each), expected);
    }
}
