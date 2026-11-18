//! An account that may change a GitOps applier, or the source that the
//! applier fetches from, may act as the account that the applier
//! applies objects as.

use super::super::account::{AppliesAs, Hop, Where};
use super::super::capability::GitOpsKind;
use super::super::routes::{Names, admits};
use super::{Places, Steps};

/// Adds to `steps` each account that an account may act as through an
/// applier it may change.  `writes` lists the GitOps kinds the account
/// may change, and where.  Each step records the hop `through` when
/// the account got those writes from an earlier step, such as a role
/// it may bind, and `Hop::Applies` otherwise.
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
