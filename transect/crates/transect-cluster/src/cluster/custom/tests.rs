//! Which custom kinds are watched, and how, and what serves each one
//! while its watch starts again.

use k8s_openapi::apiextensions_apiserver::pkg::apis::apiextensions::v1::CustomResourceDefinition;
use rstest::rstest;

use super::*;
use transect_resources::knowledge::Column;

fn kind(name: &str) -> GroupKind {
    ("example.io".into(), name.into())
}

fn resource(name: &str) -> ApiResource {
    ApiResource {
        group: "example.io".into(),
        version: "v1".into(),
        api_version: "example.io/v1".into(),
        kind: name.into(),
        plural: format!("{}s", name.to_lowercase()),
    }
}

#[rstest]
#[case::an_owner(&["Database"], &[], &[], &[], &[], &[("Database", Holding::Whole, false, 0)])]
#[case::an_owner_discovered(&["Database"], &[], &[], &[], &["Database"], &[("Database", Holding::Whole, true, 0)])]
#[case::an_owner_with_a_crd(&["Database"], &["Database"], &[], &[], &[], &[("Database", Holding::Whole, false, 0)])]
#[case::any_other_crd(&[], &["Certificate"], &[], &[], &[], &[("Certificate", Holding::Trimmed, true, 0)])]
#[case::only_controlled(&[], &[], &["Issuer"], &[], &[], &[("Issuer", Holding::Trimmed, false, 0)])]
#[case::controlled_with_a_crd(&[], &["Issuer"], &["Issuer"], &[], &[], &[("Issuer", Holding::Trimmed, true, 0)])]
#[case::a_crd_with_fields_kept(&[], &["Tree"], &[], &["Tree"], &[], &[("Tree", Holding::Trimmed, false, 1)])]
#[case::a_crd_with_fields_kept_discovered(&[], &["Tree"], &[], &["Tree"], &["Tree"], &[("Tree", Holding::Trimmed, true, 1)])]
#[case::nothing(&[], &[], &[], &[], &[], &[])]
fn watches_whole_objects_only_where_fields_are_read(
    #[case] whole: &[&str],
    #[case] crds: &[&str],
    #[case] controlled: &[&str],
    #[case] kept: &[&str],
    #[case] discovered: &[&str],
    #[case] expected: &[(&str, Holding, bool, usize)],
) {
    let served: BTreeMap<GroupKind, (ApiResource, Vec<Column>)> = crds
        .iter()
        .map(|name| (kind(name), (resource(name), Vec::new())))
        .collect();
    let kept: BTreeMap<GroupKind, Vec<String>> = kept
        .iter()
        .map(|name| (kind(name), vec!["spec.path".to_string()]))
        .collect();
    let preferred = discovered
        .iter()
        .map(|name| (kind(name), resource(name)))
        .collect();
    let found = wants(
        whole.iter().map(|name| kind(name)),
        &served,
        controlled.iter().map(|name| kind(name)),
        &kept,
        &preferred,
    );
    let found: Vec<(GroupKind, Holding, bool, usize)> = found
        .into_iter()
        .map(|(group_kind, want)| {
            let resolved = want.resource.is_some();
            (group_kind, want.holding, resolved, want.fields.len())
        })
        .collect();
    let expected: Vec<(GroupKind, Holding, bool, usize)> = expected
        .iter()
        .map(|(name, holding, resolved, fields)| (kind(name), *holding, *resolved, *fields))
        .collect();
    assert_eq!(found, expected);
}

#[rstest]
fn watches_each_crd_at_its_storage_version() {
    let crd: CustomResourceDefinition = serde_yaml::from_str(
        "metadata: {name: widgets.example.io}\n\
         spec: {group: example.io, scope: Namespaced, names: {kind: Widget, plural: widgets}, \
         versions: [{name: v1beta1, served: true, storage: false}, {name: v1, served: true, storage: true}]}",
    )
    .unwrap();
    let found = served(&[Arc::new(crd)]);
    let versions: Vec<(&GroupKind, &str, &str)> = found
        .iter()
        .map(|(kind, (resource, _))| {
            (
                kind,
                resource.api_version.as_str(),
                resource.plural.as_str(),
            )
        })
        .collect();
    assert_eq!(
        versions,
        vec![(&kind("Widget"), "example.io/v1", "widgets")]
    );
}

/// A start that counts its calls and gives each kind a watch that
/// has listed only when `listed` is true, as of its start.
struct Starts {
    count: Arc<std::sync::atomic::AtomicUsize>,
    listed: Arc<AtomicBool>,
}

impl Starts {
    fn new() -> Self {
        Self {
            count: Arc::default(),
            listed: Arc::new(AtomicBool::new(true)),
        }
    }

    fn start(&self) -> impl Fn(&GroupKind, Want) -> Optional<Watched> + '_ {
        |(_, name): &GroupKind, want: Want| {
            self.count.fetch_add(1, Ordering::SeqCst);
            let listed = Arc::new(AtomicBool::new(self.listed.load(Ordering::SeqCst)));
            let resource = resource(name);
            let (changed, _) = watch::channel(());
            Optional::discover(
                name.clone(),
                &changed,
                || std::future::ready(Ok(())),
                move |()| {
                    // The writer is dropped, so a wait for the store to
                    // be ready ends at once, and the store holds nothing.
                    let store =
                        kube::runtime::reflector::store::Writer::new(resource.clone()).as_reader();
                    let task = tokio::spawn(std::future::pending::<()>()).abort_handle();
                    Watched {
                        resource,
                        holding: want.holding,
                        store,
                        listed,
                        _running: Arc::new(Running(task)),
                    }
                },
            )
        }
    }
}

fn want(holding: Holding) -> BTreeMap<GroupKind, Want> {
    let want = match holding {
        Holding::Whole => Want::whole(None),
        Holding::Trimmed => Want {
            holding,
            resource: Some(resource("Widget")),
            columns: Vec::new(),
            fields: Vec::new(),
        },
    };
    BTreeMap::from([(kind("Widget"), want)])
}

async fn settle() {
    tokio::time::sleep(Duration::from_secs(1)).await;
}

/// What the watch that serves Widgets holds, if one does.
fn serving(owners: &CustomOwners) -> Option<Holding> {
    let watched = owners.watched().into_iter().next();
    watched.map(|(_, watched)| watched.holding)
}

/// Marks every running watch as having listed.
fn list_all(owners: &CustomOwners) {
    for tracked in owners.kinds.lock().unwrap().values() {
        if let State::On(watched) = tracked.watch.state() {
            watched.listed.store(true, Ordering::SeqCst);
        }
    }
}

#[rstest]
#[tokio::test(start_paused = true)]
async fn a_kind_keeps_its_old_store_until_the_new_one_lists() {
    let (owners, starts) = (CustomOwners::default(), Starts::new());
    let (changed, _) = watch::channel(());
    owners.track(want(Holding::Trimmed), starts.start(), &changed);
    settle().await;
    starts.listed.store(false, Ordering::SeqCst);
    owners.track(want(Holding::Whole), starts.start(), &changed);
    settle().await;
    assert_eq!(serving(&owners), Some(Holding::Trimmed));
    list_all(&owners);
    assert_eq!(serving(&owners), Some(Holding::Whole));
}

#[rstest]
#[tokio::test(start_paused = true)]
async fn a_kind_once_whole_stays_whole() {
    let (owners, starts) = (CustomOwners::default(), Starts::new());
    let (changed, _) = watch::channel(());
    owners.track(want(Holding::Whole), starts.start(), &changed);
    settle().await;
    owners.track(want(Holding::Trimmed), starts.start(), &changed);
    settle().await;
    let count = starts.count.load(Ordering::SeqCst);
    assert_eq!((serving(&owners), count), (Some(Holding::Whole), 1));
}

#[rstest]
#[tokio::test(start_paused = true)]
async fn a_kind_nothing_names_stops() {
    let (owners, starts) = (CustomOwners::default(), Starts::new());
    let (changed, _) = watch::channel(());
    owners.track(want(Holding::Trimmed), starts.start(), &changed);
    settle().await;
    owners.track(BTreeMap::new(), starts.start(), &changed);
    assert_eq!(serving(&owners), None);
}

#[rstest]
#[case::listed(true, super::Watch::Listed)]
#[case::listing(false, super::Watch::Listing)]
#[tokio::test(start_paused = true)]
async fn says_how_far_each_watch_has_come(#[case] listed: bool, #[case] expected: super::Watch) {
    let (owners, starts) = (CustomOwners::default(), Starts::new());
    starts.listed.store(listed, Ordering::SeqCst);
    let (changed, _) = watch::channel(());
    owners.track(want(Holding::Trimmed), starts.start(), &changed);
    settle().await;
    let found = owners.watched_kinds();
    assert_eq!(found.get(&kind("Widget")), Some(&expected));
}
