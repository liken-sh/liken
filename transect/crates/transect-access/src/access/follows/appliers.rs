//! The steps from changing what a GitOps tool applies: an account that
//! may change an applier, or the source it fetches through, may do
//! what the applier applies as.

use super::super::account::{AppliesAs, Hop, Where};
use super::super::capability::GitOpsKind;
use super::super::routes::{Names, admits};
use super::{Places, Steps};

/// The accounts that an account may act as through each applier it may
/// change.  `writes` lists the GitOps kinds the account may change, and
/// where.  Each step's hop is `through` when the writes come through
/// another step, such as a role it may bind, and the applier itself
/// otherwise.
pub(super) fn applies_through(
    writes: &[(GitOpsKind, Where, Names)],
    through: Option<&Hop>,
    places: &Places,
    steps: &mut Steps,
) {
    for ((group, plural), at, names) in writes {
        for applier in &places.appliers {
            let object = &applier.object;
            let namespace = object.namespace.as_deref().unwrap_or_default();
            let mut step = |account: &super::Account, how: AppliesAs| {
                let own = Hop::Applies {
                    account: account.clone(),
                    applier: object.clone(),
                    how,
                };
                let hop = through.cloned().unwrap_or(own);
                steps.acts_as.push((account.clone(), hop));
            };
            let writes_it = object.group == *group
                && applier.plural == *plural
                && at.covers(namespace)
                && admits(names, &object.name);
            if writes_it {
                let how = match applier.named {
                    true => AppliesAs::Names,
                    false => AppliesAs::Controller,
                };
                for account in &applier.accounts {
                    step(account, how.clone());
                }
                let Some(field) = &applier.field else {
                    continue;
                };
                let here = places.accounts.get(namespace).into_iter().flatten();
                for account in here.filter(|a| !applier.accounts.contains(a)) {
                    step(
                        account,
                        AppliesAs::MayName {
                            field: field.clone(),
                        },
                    );
                }
                let unset = applier.fallback.iter();
                for account in unset.filter(|a| !applier.accounts.contains(a)) {
                    step(
                        account,
                        AppliesAs::Unset {
                            field: field.clone(),
                        },
                    );
                }
                continue;
            }
            let Some((source, source_plural)) = &applier.source else {
                continue;
            };
            let source_namespace = source.namespace.as_deref().unwrap_or_default();
            let writes_its_source = source.group == *group
                && source_plural == plural
                && at.covers(source_namespace)
                && admits(names, &source.name);
            if writes_its_source {
                for account in &applier.accounts {
                    let how = AppliesAs::Through {
                        source: source.clone(),
                    };
                    step(account, how);
                }
            }
        }
    }
}
