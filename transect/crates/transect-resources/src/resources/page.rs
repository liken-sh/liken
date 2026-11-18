//! The `/resources` page.  It lists each file under `--resources`, the
//! documents in it by kind and name, and each document's conditions:
//! the condition from loading it, and the conditions from comparing it
//! to the live cluster.

use askama::Template;

use super::{Condition, Directory, DocId};

#[derive(Template)]
#[template(path = "resources/page.html", blocks = ["main"])]
pub struct ResourcesPage {
    /// The directory, or none when `transect` was started without one.
    pub dir: Option<String>,
    pub files: Vec<FileRow>,
}

#[derive(Debug, PartialEq)]
pub struct FileRow {
    pub path: String,
    pub problem: Option<String>,
    pub documents: Vec<DocumentRow>,
}

#[derive(Debug, PartialEq)]
pub struct DocumentRow {
    pub kind: String,
    pub name: String,
    pub conditions: Vec<Condition>,
}

impl ResourcesPage {
    /// The page for `loaded`, with each condition in `live` added to the
    /// document at its `DocId`.  A condition whose `DocId` matches no
    /// document is dropped.
    pub fn build(loaded: &Directory, live: Vec<(DocId, Condition)>) -> Self {
        let mut files: Vec<FileRow> = loaded
            .files
            .iter()
            .map(|file| FileRow {
                path: file.path.clone(),
                problem: file.problem.clone(),
                documents: file
                    .documents
                    .iter()
                    .map(|document| DocumentRow {
                        kind: document.kind.clone(),
                        name: document.name.clone(),
                        conditions: document.conditions.clone(),
                    })
                    .collect(),
            })
            .collect();
        for (doc, condition) in live {
            let found = files.get_mut(doc.file);
            let document = found.and_then(|file| file.documents.get_mut(doc.document));
            if let Some(document) = document {
                document.conditions.push(condition);
            }
        }
        Self {
            dir: loaded.dir.clone(),
            files,
        }
    }

    pub fn events_url(&self) -> &'static str {
        "/events/resources"
    }

    /// One line that counts the documents and files, or says that
    /// `transect` was started without a directory.
    pub fn summary(&self) -> String {
        let Some(dir) = &self.dir else {
            return "transect was started with no --resources directory.".into();
        };
        let documents: Vec<&DocumentRow> =
            self.files.iter().flat_map(|file| &file.documents).collect();
        let trouble = documents.iter().filter(|d| d.trouble()).count();
        let unread = self.files.iter().filter(|f| f.problem.is_some()).count();
        let mut line = format!(
            "{} in {} under {dir}",
            count(documents.len(), "document"),
            count(self.files.len(), "file")
        );
        if trouble > 0 {
            line.push_str(&format!(", {trouble} with a condition to read"));
        }
        if unread > 0 {
            line.push_str(&format!(", {} not read", count(unread, "file")));
        }
        line.push('.');
        line
    }
}

impl DocumentRow {
    pub fn trouble(&self) -> bool {
        self.conditions.iter().any(Condition::trouble)
    }
}

fn count(n: usize, noun: &str) -> String {
    match n {
        1 => format!("1 {noun}"),
        n => format!("{n} {noun}s"),
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::super::parse::{Read, sort};
    use super::*;

    fn loaded(files: &[(&str, &str)]) -> Directory {
        let read = files.iter().map(|(path, text)| Read {
            path: path.to_string(),
            text: Ok(text.to_string()),
        });
        Directory {
            dir: Some("res".into()),
            ..sort(read.collect())
        }
    }

    #[rstest]
    fn adds_the_live_conditions_to_their_documents() {
        let loaded = loaded(&[(
            "web.yaml",
            "apiVersion: apps/v1\nkind: Deployment\nmetadata: {namespace: shop, name: web}\n",
        )]);
        let live = vec![(DocId::default(), Condition::unmatched("no Deployment web"))];
        let page = ResourcesPage::build(&loaded, live);
        let reasons: Vec<&str> = page.files[0].documents[0]
            .conditions
            .iter()
            .map(|c| c.reason)
            .collect();
        assert_eq!(reasons, vec!["Loaded", "NoMatch"]);
    }

    #[rstest]
    #[case::none(&[], "0 documents in 0 files under res.")]
    #[case::clean(
        &[("a.yaml", "apiVersion: v1\nkind: Namespace\nmetadata: {name: shop}\n")],
        "1 document in 1 file under res."
    )]
    #[case::trouble(
        &[
            ("a.yaml", "apiVersion: transect.liken.sh/v1alpha1\nkind: Nope\n"),
            ("b.yaml", "kind: [unclosed"),
        ],
        "1 document in 2 files under res, 1 with a condition to read, 1 file not read."
    )]
    fn sums_up_the_directory(#[case] files: &[(&str, &str)], #[case] summary: &str) {
        assert_eq!(
            ResourcesPage::build(&loaded(files), vec![]).summary(),
            summary
        );
    }
}
