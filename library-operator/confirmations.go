package main

// The confirmations table is the proof that a Job's rows reached a
// standing catalog. A Corrosion agent drops the broadcasts it has not
// sent when it receives SIGTERM, and its peers fill a gap only by pulling
// from the agent that holds the rows, so a Job's agent must not exit
// until a standing pod holds what the Job wrote. The proof is the
// writer's own db version: the confirmer in each catalog pod reads the
// cr-sqlite bookkeeping to see whether its copy holds every version that
// writer made up to the one the run names, and writes a confirmations
// row when it does. Counts are never compared, because two copies' counts
// say nothing about whether one holds the other's writes.

import (
	"context"
	"strings"
	"time"
)

// UpsertConfirmation records that one catalog pod holds every
// version one Job's run named, up to the version the row carries. The
// conflict target is the whole primary key, and the update names no key
// column, because cr-sqlite reads a change to a key column as a delete and
// a create. A retried pod of the same Job writes a later version, and this
// upsert moves the row to it rather than leaving a row that proves the
// version before it.
func (c *Catalog) UpsertConfirmation(ctx context.Context, library, worker, job, confirmer string,
	version int64, at time.Time) error {
	_, err := c.apply(ctx, []statement{{
		sql: `INSERT INTO confirmations (library, worker, job, confirmer, confirmed, version) ` +
			`VALUES (?, ?, ?, ?, ?, ?) ` +
			`ON CONFLICT (library, worker, job, confirmer) DO UPDATE SET ` +
			`confirmed = excluded.confirmed, version = excluded.version`,
		params: []any{library, worker, job, confirmer, runSeconds(at), version},
	}})
	return err
}

// DeleteConfirmations takes every confirmation of one library, the
// way DeleteRuns takes its runs. The table holds a handful of rows per
// library, so this is never the batch a table of items needs.
func (c *Catalog) DeleteConfirmations(ctx context.Context, library string) (int, error) {
	return c.apply(ctx, []statement{{
		sql:    `DELETE FROM confirmations WHERE library = ?`,
		params: []any{library},
	}})
}

// PruneConfirmations takes the rows of every other Job of the same
// library and worker, so the table holds the newest run of each worker and
// never one row per Job that ever ran.
func (c *Catalog) pruneConfirmations(ctx context.Context, library, worker, keepJob string) (int, error) {
	return c.apply(ctx, []statement{{
		sql:    `DELETE FROM confirmations WHERE library = ? AND worker = ? AND job != ?`,
		params: []any{library, worker, keepJob},
	}})
}

// ConfirmedBy reports whether one pod has already confirmed one run
// at one version, which is what keeps a confirmer from writing its row
// again on every change the runs table streams. A retried pod of the same
// Job writes a version of its own, and that run reads as unconfirmed here
// until this pod holds the versions behind it.
func (c *Catalog) confirmedBy(ctx context.Context, library, worker, job, confirmer string,
	version int64) (bool, error) {
	count, err := c.queryInt(ctx,
		`SELECT count(*) FROM confirmations `+
			`WHERE library = ? AND worker = ? AND job = ? AND confirmer = ? AND version = ?`,
		[]any{library, worker, job, confirmer, version})
	return count > 0, err
}

// The two cr-sqlite reads the proof is made of. crsql_db_versions
// holds the newest version this copy has applied of each writer, and
// __corro_bookkeeping_gaps holds the ranges it knows it is missing. A
// receiving agent applies a writer's versions out of order and records the
// ones behind as gaps, so the newest version alone says nothing about the
// versions under it.
const (
	heldVersionQuery = `SELECT db_version FROM crsql_db_versions WHERE hex(site_id) = ?`
	versionGapQuery  = `SELECT count(*) FROM __corro_bookkeeping_gaps WHERE hex(actor_id) = ? AND start <= ?`
	openGapQuery     = `SELECT count(*) FROM __corro_bookkeeping_gaps`
)

// VersionsHeld answers whether this copy holds everything one
// writer wrote up to one version: a version at least that high, and no gap
// that reaches back over it. A write that named no actor is one no copy can
// prove, and reads as not held.
func versionsHeld(ctx context.Context, catalog *Catalog, actor string, version int64) (bool, error) {
	id := actorHex(actor)
	if id == "" {
		return false, nil
	}
	held, err := catalog.queryInt(ctx, heldVersionQuery, []any{id})
	if err != nil || int64(held) < version {
		return false, err
	}
	gaps, err := catalog.queryInt(ctx, versionGapQuery, []any{id, version})
	return gaps == 0 && err == nil, err
}

// OpenGaps counts every range this copy knows it is missing,
// whoever wrote it. A copy with none holds every version it has heard of.
func (c *Catalog) openGaps(ctx context.Context) (int, error) {
	return c.queryInt(ctx, openGapQuery, nil)
}

// ActorHex renders an agent id the way hex(site_id) reads it. The
// transaction response names the actor as a dashed uuid and the column
// holds the sixteen bytes, so the comparison drops the dashes and raises
// the case.
func actorHex(actor string) string {
	return strings.ToUpper(strings.ReplaceAll(actor, "-", ""))
}
