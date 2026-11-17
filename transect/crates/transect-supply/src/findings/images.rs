//! The words of the findings about running images: those with no source
//! label, and those whose labels transect cannot read, each listed by
//! registry.

use crate::images::labels::{REVISION, SOURCE};
use crate::images::{Gap, Unreadable};

pub fn unlabelled_title(gaps: &[Gap]) -> String {
    match gaps.iter().map(|gap| gap.unlabelled).sum() {
        1 => "1 running image has no source label".into(),
        images => format!("{images} running images have no source label"),
    }
}

pub fn unlabelled_body() -> String {
    format!(
        "Without {SOURCE} and {REVISION}, transect cannot link a container to the commit it runs. \
         An image built here gets them from its build; one from elsewhere, from its publisher."
    )
}

/// "3 of the 18 images read", after the registry's name.
pub fn gap_line(gap: &Gap) -> String {
    format!("{} of the {} images read", gap.unlabelled, gap.read)
}

pub fn unreadable_title(unreadable: &[Unreadable]) -> String {
    let registries = {
        let mut names: Vec<&str> = unreadable
            .iter()
            .map(|found| found.registry.as_str())
            .collect();
        names.dedup();
        names.len()
    };
    match registries {
        1 => format!(
            "transect cannot read image labels from {}",
            unreadable[0].registry
        ),
        registries => format!("transect cannot read image labels from {registries} registries"),
    }
}

pub fn unreadable_body() -> &'static str {
    "Their containers show no commit until transect can read them. It tries each again later, waiting longer after each failure."
}

/// "2 images: the registry wants credentials transect does not have",
/// and "read from the mirror …" when transect read a mirror.
pub fn unreadable_line(found: &Unreadable) -> String {
    let images = match found.images {
        1 => "1 image".to_string(),
        images => format!("{images} images"),
    };
    match &found.mirror {
        Some(mirror) => format!("{images}: {}, read from {mirror}", found.failure.note()),
        None => format!("{images}: {}", found.failure.note()),
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;
    use crate::images::Failure;

    fn gap(registry: &str, unlabelled: usize, read: usize) -> Gap {
        Gap {
            registry: registry.into(),
            unlabelled,
            read,
        }
    }

    fn unreadable(registry: &str, images: usize, failure: Failure) -> Unreadable {
        Unreadable {
            registry: registry.into(),
            images,
            failure,
            mirror: None,
        }
    }

    #[rstest]
    #[case::one(vec![gap("registry.example", 1, 4)], "1 running image has no source label")]
    #[case::across_registries(
        vec![gap("registry.example", 3, 4), gap("other.example", 2, 9)],
        "5 running images have no source label"
    )]
    fn titles_the_unlabelled(#[case] gaps: Vec<Gap>, #[case] title: &str) {
        assert_eq!(unlabelled_title(&gaps), title);
    }

    #[rstest]
    fn lines_a_registrys_gap() {
        assert_eq!(
            gap_line(&gap("registry.example", 3, 18)),
            "3 of the 18 images read"
        );
    }

    #[rstest]
    #[case::one_registry(
        vec![unreadable("private.example", 3, Failure::NeedsCredentials)],
        "transect cannot read image labels from private.example"
    )]
    #[case::one_registry_two_reasons(
        vec![
            unreadable("private.example", 3, Failure::NeedsCredentials),
            unreadable("private.example", 1, Failure::NotFound),
        ],
        "transect cannot read image labels from private.example"
    )]
    #[case::two_registries(
        vec![
            unreadable("private.example", 3, Failure::NeedsCredentials),
            unreadable("pages.example", 1, Failure::NotARegistry),
        ],
        "transect cannot read image labels from 2 registries"
    )]
    fn titles_the_unreadable(#[case] found: Vec<Unreadable>, #[case] title: &str) {
        assert_eq!(unreadable_title(&found), title);
    }

    #[rstest]
    #[case::one(
        unreadable("pages.example", 1, Failure::NotARegistry),
        "1 image: the host does not answer as a registry"
    )]
    #[case::several(
        unreadable("private.example", 3, Failure::NeedsCredentials),
        "3 images: the registry requires credentials that transect does not have"
    )]
    fn lines_a_registrys_failure(#[case] found: Unreadable, #[case] line: &str) {
        assert_eq!(unreadable_line(&found), line);
    }

    #[rstest]
    fn names_the_mirror_it_read() {
        let found = Unreadable {
            mirror: Some("the mirror https://mirror.example".into()),
            ..unreadable("images.example", 2, Failure::NotFound)
        };
        assert_eq!(
            unreadable_line(&found),
            "2 images: the registry no longer has this digest, read from the mirror https://mirror.example"
        );
    }
}
