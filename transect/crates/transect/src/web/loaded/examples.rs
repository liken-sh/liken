//! Loads `examples/` and the knowledge that each component of the
//! repository ships in its own `transect/` directory, with the loader
//! that `--resources` uses.  So an example or an operator's knowledge
//! that fails to load or gets a trouble condition fails a test before a
//! reader copies it or a cluster mounts it.

use std::path::{Path, PathBuf};

use rstest::rstest;
use serde::Deserialize;

use super::Loaded;
use transect_graph::edges::Plurals;
use transect_resources::knowledge::{KindKnowledge, Kinds};
use transect_resources::resources::load;
use transect_resources::shapes::Shapes;

fn examples() -> PathBuf {
    PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../../examples")
}

/// The top of the `liken` repository, which contains `transect/` and
/// every other component.
fn repository() -> PathBuf {
    let root = PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../../..");
    root.canonicalize().unwrap()
}

/// The `transect/` directory of each component at the top of the
/// repository that has one with YAML in it.  The test finds them by
/// reading the repository, so a new operator's knowledge is checked
/// without a change here.
fn first_party() -> Vec<PathBuf> {
    let components = std::fs::read_dir(repository()).unwrap();
    let mut found: Vec<PathBuf> = components
        .map(|entry| entry.unwrap().path().join("transect"))
        .filter(|directory| has_yaml(directory))
        .collect();
    found.sort();
    found
}

fn has_yaml(directory: &Path) -> bool {
    let Ok(entries) = std::fs::read_dir(directory) else {
        return false;
    };
    let paths = entries.map(|entry| entry.unwrap().path());
    paths.into_iter().any(|path| has_yaml_extension(&path))
}

/// Each fault that loading `directory` finds, one line each: a file
/// that does not load, a document with a trouble condition, a kind or a
/// role whose shape neither the directory nor the library has, a wire
/// that meets a shape with no port on the facing edge, or a directory
/// with no files.  Each part is drawn in its role's shape or the
/// default here, because a directory alone does not name the kind that
/// each `KindKnowledge` describes, so a wire to a part that takes the
/// shape of its kind is checked when a cluster serves the kind.
fn trouble(directory: &Path) -> Vec<String> {
    let loaded: Loaded = load(directory);
    let shown = directory.display();
    let mut trouble = Vec::new();
    if loaded.directory.files.is_empty() {
        trouble.push(format!("{shown} contains no files"));
    }
    for file in &loaded.directory.files {
        if let Some(problem) = &file.problem {
            trouble.push(format!("{shown}: {}: {problem}", file.path));
        }
        for document in &file.documents {
            for condition in document.conditions.iter().filter(|c| c.trouble()) {
                let (path, text) = (&file.path, condition.text());
                let named = format!("{} {}", document.kind, document.name);
                trouble.push(format!("{shown}: {path} {named}: {text}"));
            }
        }
    }
    let shapes = loaded.shapes();
    for known in &loaded.kinds {
        let mut known = known.clone();
        let own = known.find_shape(&shapes);
        let (unknown, unported) = known.draw_with(&shapes, |_, _| None);
        for message in own.into_iter().chain(unknown).chain(unported) {
            trouble.push(format!("{shown}: {}: {message}", known.name));
        }
    }
    trouble
}

#[rstest]
fn every_example_loads_with_no_condition() {
    let trouble = trouble(&examples());
    assert!(trouble.is_empty(), "{}", trouble.join("\n"));
}

#[rstest]
fn the_knowledge_of_every_component_loads_with_no_condition() {
    let directories = first_party();
    assert!(
        !directories.is_empty(),
        "no directory at the top of {} has a transect/ directory with YAML in it",
        repository().display()
    );
    let trouble: Vec<String> = directories.iter().flat_map(|d| trouble(d)).collect();
    assert!(trouble.is_empty(), "{}", trouble.join("\n"));
}

/// The YAML files under `directory` and every directory in it.
fn yaml_under(directory: &Path) -> Vec<PathBuf> {
    let Ok(entries) = std::fs::read_dir(directory) else {
        return Vec::new();
    };
    let mut found = Vec::new();
    for path in entries.map(|entry| entry.unwrap().path()) {
        if path.is_dir() {
            found.extend(yaml_under(&path));
        } else if has_yaml_extension(&path) {
            found.push(path);
        }
    }
    found
}

fn has_yaml_extension(path: &Path) -> bool {
    let extension = path.extension().and_then(|extension| extension.to_str());
    matches!(extension, Some("yaml" | "yml"))
}

/// The kind and the plural of each custom resource definition in the
/// `deploy/` directory of each component, as a cluster that runs every
/// component serves them.
fn served() -> Plurals {
    let components = std::fs::read_dir(repository()).unwrap();
    let deploys = components.map(|entry| entry.unwrap().path().join("deploy"));
    let mut plurals = Plurals::new();
    for path in deploys.flat_map(|deploy| yaml_under(&deploy)) {
        let text = std::fs::read_to_string(&path).unwrap();
        for document in serde_yaml::Deserializer::from_str(&text) {
            let Ok(value) = serde_yaml::Value::deserialize(document) else {
                continue;
            };
            if value["kind"].as_str() != Some("CustomResourceDefinition") {
                continue;
            }
            let (Some(group), Some(plural), Some(kind)) = (
                value["spec"]["group"].as_str(),
                value["spec"]["names"]["plural"].as_str(),
                value["spec"]["names"]["kind"].as_str(),
            ) else {
                continue;
            };
            plurals.insert((group.into(), kind.into()), plural.into());
        }
    }
    plurals
}

/// The knowledge of every component and of `examples/`, resolved
/// together as a cluster that runs every component reads it, so a
/// `namedBy`, a `through`, or an `elevation` that names knowledge nowhere
/// in the repository fails, and so does a wire to a part that takes the
/// shape of its kind and has no port.
#[rstest]
fn the_knowledge_of_every_component_resolves_together() {
    let mut directories = first_party();
    directories.push(examples());
    let loaded: Vec<Loaded> = directories.iter().map(|d| load(d)).collect();
    let knowledge: Vec<KindKnowledge> = loaded.iter().flat_map(|l| l.kinds.clone()).collect();
    let shapes: Vec<_> = loaded.iter().flat_map(|l| l.shapes.clone()).collect();
    let mut kinds = Kinds::resolve(&knowledge, &served(), &[]);
    kinds.find_shapes(&Shapes::with(&shapes));
    let faults = ["Unresolved", "UnknownShape", "NoPort"];
    let trouble: Vec<String> = kinds
        .conditions
        .iter()
        .filter(|(_, condition)| faults.contains(&condition.reason))
        .map(|(_, condition)| condition.text())
        .collect();
    assert!(trouble.is_empty(), "{}", trouble.join("\n"));
}

#[rstest]
fn every_example_project_has_a_readme() {
    let projects = std::fs::read_dir(examples()).unwrap();
    let projects = projects.map(|entry| entry.unwrap().path());
    let missing: Vec<String> = projects
        .filter(|path| path.is_dir() && !path.join("README.md").is_file())
        .map(|path| path.display().to_string())
        .collect();
    assert!(
        missing.is_empty(),
        "these example projects have no README.md: {missing:?}"
    );
}
