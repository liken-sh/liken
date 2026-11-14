package main

// rootliken.go keeps the library root free of the ledgers of a title. A
// .liken directory holds the ledgers of the titles in its folder, and the
// root holds no title: the walk reads titles only in the folders below it,
// and a rescan of the root walks the whole root instead. So no fact of a
// title belongs to the root, and nothing reads a ledger there. The close
// container of every library Job removes each file of the root's .liken
// directory except one: the titles directory.
//
// The titles directory at the root holds the Library's titles model, which
// the fit Job writes (titlesfitrun.go) and which belongs to the whole
// Library, not to a title. It also holds the candidate of a fit that runs
// while a close does, until the fit renames the candidate onto the model.
// Without the exception, each library Job would remove the model, and no fit
// would start again until the Library's count of marked videos grew by half.
//
// Two clusters can mount one volume. One cluster's close container then
// removes the other's root ledgers too, which costs that cluster nothing,
// because no fact of either cluster reads them.

// The close container's call. A failure is logged and never fails the run,
// because the next library Job removes the files again.
func (r *closeRun) removeRootLiken() {
	removed, err := r.writer.removeRootLiken(r.root)
	if err != nil {
		r.logf("could not clear the %s directory at the library root: %v", likenDirectory, err)
		return
	}
	if removed {
		r.logf("removed the files of the %s directory at the library root and kept the titles model, "+
			"because the root holds no title", likenDirectory)
	}
}
