//! Whether a watch event changes what a store holds.
//!
//! A watch delivers an event on every `resourceVersion` bump, and a
//! controller may write an object every few seconds without changing
//! anything a page draws: a `Job`'s count of ready pods on a store that
//! keeps only metadata, or a condition's heartbeat time that the trim
//! drops.  Each store trims its objects before they reach this
//! comparison, so a field the trim drops never counts as a change.
//! Signalling every event would rebuild the model as often as the
//! debounce lets it, for changes no page draws.
//!
//! Kind knowledge can name labels and annotations a tool rewrites
//! without changing anything, such as the ownership labels a GitOps
//! tool writes on a `Namespace` that two of its appliers both apply.  The
//! comparison leaves those keys out, and the store still keeps the
//! object as the API gives it, so a page drawn for another reason
//! reads the newest value.

use std::hash::Hash;
use std::sync::{Arc, RwLock};

use kube::Resource;
use kube::runtime::reflector::{ObjectRef, Store};
use kube::runtime::watcher;
use serde::Serialize;
use serde_json::Value;

#[cfg(test)]
use transect_resources::knowledge::gitops::GroupKind;
use transect_resources::knowledge::{IgnoreChange, MetaKey};

/// The keys that kind knowledge names as ones a tool rewrites without
/// changing anything, shared by every watch and replaced when the
/// resources reload.
#[derive(Clone, Default)]
pub struct Ignored(Arc<RwLock<Vec<IgnoreChange>>>);

impl Ignored {
    pub fn set(&self, ignored: Vec<IgnoreChange>) {
        *self.0.write().unwrap_or_else(|e| e.into_inner()) = ignored;
    }

    /// The keys ignored on objects of `kind` in `group`.
    pub fn keys(&self, group: &str, kind: &str) -> Vec<MetaKey> {
        let ignored = self.0.read().unwrap_or_else(|e| e.into_inner());
        let held = ignored
            .iter()
            .filter(|ignore| ignore.holds_for(group, kind));
        held.map(|ignore| ignore.key.clone()).collect()
    }
}

/// Whether `event` changes what `store` holds, but for the keys in
/// `ignored`: an object it lacks or holds in another form, a deletion,
/// or the end of a relist that found something other than what the
/// store holds.  The objects of a relist are applied together at its
/// end, so they are collected in `relist` until then.  Every watch on
/// one connection relists after that connection resets, and a relist
/// that finds what the store already holds changes nothing a page draws.
pub fn differs<K>(
    store: &Store<K>,
    dyntype: &K::DynamicType,
    event: &watcher::Event<K>,
    relist: &mut Vec<K>,
    ignored: &[MetaKey],
) -> bool
where
    K: Resource + Clone + Serialize + 'static,
    K::DynamicType: Eq + Hash + Clone,
{
    match event {
        watcher::Event::Apply(object) => !holds(store, dyntype, object, ignored),
        watcher::Event::Delete(_) => true,
        watcher::Event::Init => {
            relist.clear();
            false
        }
        watcher::Event::InitApply(object) => {
            relist.push(object.clone());
            false
        }
        watcher::Event::InitDone => {
            let listed = std::mem::take(relist);
            listed.len() != store.len()
                || !listed
                    .iter()
                    .all(|object| holds(store, dyntype, object, ignored))
        }
    }
}

/// Whether `store` holds an object that every page draws the same as
/// `object`.
fn holds<K>(store: &Store<K>, dyntype: &K::DynamicType, object: &K, ignored: &[MetaKey]) -> bool
where
    K: Resource + Clone + Serialize + 'static,
    K::DynamicType: Eq + Hash + Clone,
{
    let key = ObjectRef::from_obj_with(object, dyntype.clone());
    store
        .get(&key)
        .is_some_and(|held| same(held.as_ref(), object, ignored))
}

/// Whether every page draws two trimmed objects the same: equal in
/// every field the trim kept but the `resourceVersion`, which every
/// write bumps, and the keys in `ignored`.
pub fn same<K: Serialize>(held: &K, object: &K, ignored: &[MetaKey]) -> bool {
    unversioned(held, ignored) == unversioned(object, ignored)
}

fn unversioned<K: Serialize>(object: &K, ignored: &[MetaKey]) -> Value {
    let mut value = serde_json::to_value(object).unwrap_or(Value::Null);
    if let Some(meta) = value.get_mut("metadata").and_then(Value::as_object_mut) {
        meta.remove("resourceVersion");
        for key in ignored {
            let (map, key) = match key {
                MetaKey::Label(key) => ("labels", key),
                MetaKey::Annotation(key) => ("annotations", key),
            };
            if let Some(map) = meta.get_mut(map).and_then(Value::as_object_mut) {
                map.remove(key);
            }
        }
    }
    value
}

#[cfg(test)]
mod tests {
    use k8s_openapi::api::batch::v1::Job;
    use kube::core::{ApiResource, DynamicObject, PartialObjectMeta};
    use kube::runtime::reflector;
    use rstest::rstest;

    use super::*;
    use crate::cluster::trim;

    fn widget(meta: &str, status: &str) -> DynamicObject {
        let text = format!(
            "apiVersion: example.io/v1\nkind: Widget\nmetadata: {{name: a, namespace: shop, {meta}}}\nstatus: {status}"
        );
        let mut object: DynamicObject = serde_yaml::from_str(&text).unwrap();
        trim::conditions_and_columns(&mut object, &[], &[]);
        object
    }

    const READY: &str = "{conditions: [{type: Ready, status: 'True'}]}";

    #[rstest]
    #[case::only_the_version(widget("resourceVersion: '2'", READY), false)]
    #[case::a_status_field_the_trim_drops(
        widget(
            "resourceVersion: '2'",
            "{conditions: [{type: Ready, status: 'True'}], phase: Busy}"
        ),
        false
    )]
    #[case::a_condition(
        widget(
            "resourceVersion: '2'",
            "{conditions: [{type: Ready, status: 'False'}]}"
        ),
        true
    )]
    #[case::a_label(widget("resourceVersion: '2', labels: {app: a}", READY), true)]
    #[case::the_generation(widget("resourceVersion: '2', generation: 2", READY), true)]
    #[case::a_deletion(
        widget(
            "resourceVersion: '2', deletionTimestamp: '2026-01-01T00:00:00Z'",
            READY
        ),
        true
    )]
    fn signals_only_a_change_a_page_draws(#[case] object: DynamicObject, #[case] expected: bool) {
        let held = widget("resourceVersion: '1'", READY);
        assert_eq!(!same(&held, &object, &[]), expected);
    }

    const OWNER: &str = "tree/name";

    /// A `Widget` whose label `tree/name` is `owner`, and whose annotation
    /// `note` is `note`.
    fn owned(owner: &str, note: &str) -> DynamicObject {
        widget(
            &format!(
                "resourceVersion: '2', labels: {{{OWNER}: {owner}}}, annotations: {{note: {note}}}"
            ),
            READY,
        )
    }

    #[rstest]
    #[case::an_ignored_label(owned("b", "x"), vec![MetaKey::Label(OWNER.into())], false)]
    #[case::an_ignored_label_and_another_change(owned("b", "y"), vec![MetaKey::Label(OWNER.into())], true)]
    #[case::an_ignored_annotation(owned("a", "y"), vec![MetaKey::Annotation("note".into())], false)]
    #[case::a_label_with_the_annotations_name(owned("a", "y"), vec![MetaKey::Label("note".into())], true)]
    #[case::nothing_ignored(owned("b", "x"), vec![], true)]
    fn leaves_the_ignored_keys_out(
        #[case] object: DynamicObject,
        #[case] ignored: Vec<MetaKey>,
        #[case] expected: bool,
    ) {
        assert_eq!(!same(&owned("a", "x"), &object, &ignored), expected);
    }

    #[rstest]
    #[case::the_kind_named("", "Namespace", vec![MetaKey::Label(OWNER.into())])]
    #[case::another_kind("", "ConfigMap", vec![])]
    #[case::the_kind_in_another_group("example.test", "Namespace", vec![])]
    fn ignores_a_key_for_the_kinds_it_names(
        #[case] group: &str,
        #[case] kind: &str,
        #[case] expected: Vec<MetaKey>,
    ) {
        let ignored = Ignored::default();
        ignored.set(vec![IgnoreChange {
            key: MetaKey::Label(OWNER.into()),
            kinds: vec![GroupKind::new("", "Namespace")],
        }]);
        assert_eq!(ignored.keys(group, kind), expected);
    }

    /// A `Job` as its metadata store holds it, read from `yaml`.
    fn job(yaml: &str) -> PartialObjectMeta<Job> {
        let mut job: PartialObjectMeta<Job> = serde_yaml::from_str(yaml).unwrap();
        trim::meta(&mut job.metadata);
        job
    }

    #[rstest]
    #[case::a_status_write(
        "{apiVersion: batch/v1, kind: Job, metadata: {name: a, resourceVersion: '2', managedFields: [{manager: job-controller, time: '2026-01-01T00:00:09Z'}]}}",
        false
    )]
    #[case::an_annotation(
        "{apiVersion: batch/v1, kind: Job, metadata: {name: a, resourceVersion: '2', annotations: {note: x}}}",
        true
    )]
    fn signals_a_metadata_store_only_for_its_metadata(#[case] yaml: &str, #[case] expected: bool) {
        let held =
            job("{apiVersion: batch/v1, kind: Job, metadata: {name: a, resourceVersion: '1'}}");
        assert_eq!(!same(&held, &job(yaml), &[]), expected);
    }

    /// A `Widget` named `name`, at `version`, whose `Ready` is `ready`.
    fn named(name: &str, version: &str, ready: &str) -> DynamicObject {
        let mut object = widget(
            &format!("resourceVersion: '{version}'"),
            &format!("{{conditions: [{{type: Ready, status: '{ready}'}}]}}"),
        );
        object.metadata.name = Some(name.into());
        object
    }

    fn resource() -> ApiResource {
        ApiResource {
            group: "example.io".into(),
            version: "v1".into(),
            api_version: "example.io/v1".into(),
            kind: "Widget".into(),
            plural: "widgets".into(),
        }
    }

    /// Whether a relist that finds `listed`, after a store that held
    /// `a` and `b`, both ready, signals a change.
    fn relist_signals(listed: Vec<DynamicObject>) -> bool {
        let mut writer = reflector::store::Writer::new(resource());
        for object in [named("a", "1", "True"), named("b", "1", "True")] {
            writer.apply_watcher_event(&watcher::Event::Apply(object));
        }
        let store = writer.as_reader();
        let mut relist = Vec::new();
        let mut events = vec![watcher::Event::Init];
        events.extend(listed.into_iter().map(watcher::Event::InitApply));
        events.push(watcher::Event::InitDone);
        let signals = events
            .iter()
            .map(|event| differs(&store, &resource(), event, &mut relist, &[]));
        signals.collect::<Vec<_>>().contains(&true)
    }

    #[rstest]
    #[case::the_same_at_newer_versions(vec![named("a", "7", "True"), named("b", "8", "True")], false)]
    #[case::a_condition_changed(vec![named("a", "7", "True"), named("b", "8", "False")], true)]
    #[case::one_deleted_meanwhile(vec![named("a", "7", "True")], true)]
    #[case::one_created_meanwhile(
        vec![named("a", "1", "True"), named("b", "1", "True"), named("c", "1", "True")],
        true
    )]
    #[case::one_replaced(vec![named("a", "1", "True"), named("c", "1", "True")], true)]
    fn signals_a_relist_only_when_it_found_otherwise(
        #[case] listed: Vec<DynamicObject>,
        #[case] expected: bool,
    ) {
        assert_eq!(relist_signals(listed), expected);
    }
}
