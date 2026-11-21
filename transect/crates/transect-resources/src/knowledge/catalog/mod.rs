//! `transect`'s catalog of patterns. A primary with replicas, a work
//! queue, and requests are the same model in every project that has
//! one, so `transect` states each such pattern once and draws it the
//! same way for every kind that fills it. A kind's `pattern` names a
//! pattern of the catalog and says which of its references fills each
//! role, and the catalog states the rest: each role's label, shape, and
//! readings, the elevations, and what the pattern expects.
//!
//! A pattern is a YAML file in this directory, for its roles and its
//! elevations, and a Rust module of the same name, for the roles that it
//! derives and for its expectations. A role's `derive` names a function
//! of that module. This is the one place where data names code, because
//! a derived role and an expectation are logic, and no author outside
//! the binary writes the catalog.
//!
//! The catalog is in the binary, so a fault in a file is a fault of the
//! binary, and a test fails when a file does not load.

mod filled;
mod judged;
mod primary_with_replicas;

use std::sync::LazyLock;

use serde::Deserialize;

use super::KindKnowledge;
use super::elevation::{self, Elevation};
use super::role::{self, Expected, Look, Role};
pub use filled::fill;
pub use judged::{
    Cluster, Derivations, Derived, Judged, Line, Mark, Marked, Marks, Occurrence, Said,
};

/// One pattern of the catalog, as the catalog states it, before a kind
/// fills it.
#[derive(Debug)]
pub struct Definition {
    pub name: String,
    /// How a page names the pattern, such as "primary with replicas".
    pub title: String,
    roles: Vec<Template>,
    pub elevations: Vec<Elevation>,
    /// The role that states `failover`, when one does.
    failover: Option<String>,
    module: &'static Module,
}

/// One role of a pattern, before a kind fills it.
#[derive(Debug)]
struct Template {
    name: String,
    look: Look,
    source: Source,
}

/// Where the parts of a role of a pattern come from.
#[derive(Debug, PartialEq)]
enum Source {
    /// An entry of the kind's `references`, which the kind's `pattern`
    /// names. The entry names `kind` in `group`, which is empty for the
    /// core group, and with `list` it reads a list.
    Fill {
        required: bool,
        group: String,
        kind: String,
        list: bool,
    },
    /// The function of the pattern's module that this name names.
    Derive(String),
}

/// A function of a pattern's module that derives the parts of a role.
type Derive = fn(&Occurrence) -> Derived;

/// The code of one pattern: its file, the function that each `derive`
/// names, and its expectations.
#[derive(Debug)]
pub struct Module {
    file: &'static str,
    /// The function that a `derive` names, or none for a name that the
    /// module does not have.
    derive: fn(&str) -> Option<Derive>,
    /// The lines of what the pattern expects, and the parts that they
    /// mark, for an object whose derived roles are read.
    expect: fn(&Occurrence) -> (Vec<Line>, Marks),
}

/// The module of each pattern of the catalog.
const MODULES: &[&Module] = &[&primary_with_replicas::MODULE];

static DEFINITIONS: LazyLock<Vec<Definition>> = LazyLock::new(|| {
    let read = MODULES.iter().map(|module| load(module));
    read.map(|loaded| loaded.unwrap_or_else(|error| panic!("{error}")))
        .collect()
});

/// The patterns of the catalog.
pub fn definitions() -> &'static [Definition] {
    &DEFINITIONS
}

/// A pattern of the catalog as one kind fills it.
#[derive(Clone, Debug, PartialEq)]
pub struct Pattern {
    pub name: String,
    pub title: String,
    /// Each role of the pattern, with the fill that the kind states, or
    /// the derive of the pattern's module, in the catalog's order.
    pub roles: Vec<Role>,
    pub elevations: Vec<Elevation>,
    /// The role that states `failover`, when one does.
    pub failover: Option<String>,
}

impl Pattern {
    fn definition(&self) -> &'static Definition {
        let found = definitions().iter().find(|d| d.name == self.name);
        found.expect("a pattern is filled only from a definition of the catalog")
    }

    /// The path of the reference that fills the role that states
    /// `failover`, for a pattern that has one.
    pub fn failover(&self) -> Option<&str> {
        let failover = self.failover.as_deref()?;
        let role = self.roles.iter().find(|role| role.name == failover)?;
        role.fill.reference()
    }
}

/// The pattern that `knowledge` fills, as `transect` reads it for the
/// object `object` in `cluster`: the parts of each derived role, and
/// what the pattern expects. It is none for a kind with no pattern. The
/// page draws the derived parts, and the expectations read the same
/// parts, so the two agree.
pub fn judge(
    knowledge: &KindKnowledge,
    object: &transect_graph::edges::Object,
    cluster: &dyn Cluster,
) -> Option<Judged> {
    let pattern = knowledge.pattern.as_ref()?;
    let module = pattern.definition().module;
    let mut occurrence = Occurrence {
        object,
        knowledge,
        pattern,
        cluster,
        derived: Derivations::default(),
    };
    let derived = pattern
        .roles
        .iter()
        .filter_map(|role| match &role.fill.first {
            role::First::Derived(name) => {
                let derive =
                    (module.derive)(name).expect("the catalog loads only a derive its module has");
                Some((role.name.clone(), derive(&occurrence)))
            }
            _ => None,
        });
    occurrence.derived = derived.collect();
    let (lines, marks) = (module.expect)(&occurrence);
    Some(Judged {
        derived: occurrence.derived,
        lines,
        marks,
    })
}

/// A pattern of the catalog, as its file writes it.
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Written {
    name: String,
    title: String,
    roles: serde_yaml::Mapping,
    elevations: Vec<elevation::Written>,
}

/// Where a role of a pattern takes its parts from, as its file writes
/// it.
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct WrittenSource {
    fill: Option<String>,
    derive: Option<String>,
    kind: Option<String>,
    group: Option<String>,
    #[serde(default)]
    list: bool,
    #[serde(default)]
    failover: bool,
}

const SOURCE_KEYS: &[&str] = &["fill", "derive", "kind", "group", "list", "failover"];

/// The pattern that `module`'s file states.
fn load(module: &'static Module) -> Result<Definition, String> {
    let written: Written = serde_yaml::from_str(module.file).map_err(|e| e.to_string())?;
    let name = written.name.clone();
    read(written, module).map_err(|error| format!("the catalog's pattern {name}: {error}"))
}

fn read(written: Written, module: &'static Module) -> Result<Definition, String> {
    let mut roles = Vec::new();
    let mut failover: Option<String> = None;
    for (name, mut fields) in role::entries(Some(written.roles))? {
        let template = (|| {
            let source: WrittenSource =
                serde_yaml::from_value(role::take(&mut fields, SOURCE_KEYS).into())
                    .map_err(|error| error.to_string())?;
            let look = role::look::read(fields, &[])?;
            if source.failover {
                if let Some(earlier) = &failover {
                    return Err(format!(
                        "failover is on {earlier} already, and the loss of one role stops the pattern until the controller promotes another part"
                    ));
                }
                failover = Some(name.clone());
            }
            if let Expected::While(condition) = &look.expected {
                return Err(format!(
                    "expected is while {condition}, and a pattern describes every kind that fills it, so it names no condition of one kind"
                ));
            }
            Ok(Template {
                source: source_of(source, module)?,
                name: name.clone(),
                look,
            })
        })()
        .map_err(|e: String| format!("roles.{name}: {e}"))?;
        roles.push(template);
    }
    let names: Vec<&str> = roles.iter().map(|role| role.name.as_str()).collect();
    let elevations = elevation::read(written.elevations, &names)?;
    Ok(Definition {
        name: written.name,
        title: written.title,
        roles,
        elevations,
        failover,
        module,
    })
}

/// Where a role takes its parts from: the kind's `pattern`, which must
/// name an entry of `kind`, or a function of `module`.
fn source_of(written: WrittenSource, module: &Module) -> Result<Source, String> {
    match (written.fill, written.derive) {
        (Some(fill), None) => {
            let required = match fill.as_str() {
                "required" => true,
                "optional" => false,
                other => {
                    return Err(format!(
                        "fill is {other:?}, and it is \"required\" or \"optional\""
                    ));
                }
            };
            let kind = written
                .kind
                .ok_or("the role is filled by a reference, so it names the kind of its parts")?;
            Ok(Source::Fill {
                required,
                group: written.group.unwrap_or_default(),
                kind,
                list: written.list,
            })
        }
        (None, Some(derive)) => {
            if written.kind.is_some() || written.group.is_some() || written.list {
                return Err("a derived role takes its parts from its function, so it names no kind and no list".into());
            }
            match (module.derive)(&derive) {
                Some(_) => Ok(Source::Derive(derive)),
                None => Err(format!(
                    "derive is {derive:?}, which the pattern's module has no function for"
                )),
            }
        }
        _ => Err("the role has one of fill and derive".into()),
    }
}

#[cfg(test)]
mod tests;
