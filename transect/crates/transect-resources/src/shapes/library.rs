//! The library's shapes: the shapes that 00-design.md names, the `tag`
//! of plan 13, and the shapes that a pattern of `transect`'s catalog
//! draws. Each is a file in `library/`, written in the form that an
//! author writes, and embedded in the binary.

use std::sync::{Arc, LazyLock};

use super::form::read;
use super::{Origin, Shape};

/// Each file of the library, by name. A test fails when a file in
/// `library/` is missing from this list.
const FILES: &[(&str, &str)] = &[
    ("box.yaml", include_str!("library/box.yaml")),
    ("disc.yaml", include_str!("library/disc.yaml")),
    ("drum.yaml", include_str!("library/drum.yaml")),
    ("handheld.yaml", include_str!("library/handheld.yaml")),
    ("screen.yaml", include_str!("library/screen.yaml")),
    ("socket.yaml", include_str!("library/socket.yaml")),
    ("speaker.yaml", include_str!("library/speaker.yaml")),
    ("tag.yaml", include_str!("library/tag.yaml")),
];

/// The shape that a role draws when it names none, and when the shape
/// it names is not loaded.
pub const DEFAULT: &str = "box";

static SHAPES: LazyLock<Vec<Arc<Shape>>> = LazyLock::new(|| {
    let read = FILES.iter().map(|(file, text)| load(file, text));
    read.map(|shape| Arc::new(shape.unwrap_or_else(|error| panic!("{error}"))))
        .collect()
});

/// The library's shapes, by name.
pub fn shapes() -> &'static [Arc<Shape>] {
    &SHAPES
}

/// The library's `box`.
pub fn default() -> Arc<Shape> {
    let found = shapes().iter().find(|shape| shape.name == DEFAULT);
    found.expect("the library has a box").clone()
}

fn load(file: &str, text: &str) -> Result<Shape, String> {
    let value = serde_yaml::from_str(text).map_err(|error| format!("{file}: {error}"))?;
    let shape = read(value, Origin::Library).map_err(|error| format!("{file}: {error}"))?;
    match file.strip_suffix(".yaml") == Some(shape.name.as_str()) {
        true => Ok(shape),
        false => Err(format!(
            "{file} defines {}, not the shape the file is named for",
            shape.name
        )),
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    #[rstest]
    fn loads_every_file_of_the_library() {
        let failed: Vec<String> = FILES
            .iter()
            .filter_map(|(file, text)| load(file, text).err())
            .collect();
        assert_eq!(failed, Vec::<String>::new());
    }

    #[rstest]
    fn embeds_every_file_in_the_directory() {
        let directory = concat!(env!("CARGO_MANIFEST_DIR"), "/src/shapes/library");
        let mut found: Vec<String> = std::fs::read_dir(directory)
            .unwrap()
            .map(|entry| entry.unwrap().file_name().to_string_lossy().into_owned())
            .collect();
        found.sort();
        let listed: Vec<&str> = FILES.iter().map(|(file, _)| *file).collect();
        assert_eq!(found, listed);
    }

    #[rstest]
    fn names_the_shape_for_its_file() {
        assert!(
            load("disc.yaml", FILES[0].1)
                .unwrap_err()
                .contains("defines box")
        );
    }

    #[rstest]
    fn draws_a_box_by_default() {
        assert_eq!(default().name, DEFAULT);
    }
}
