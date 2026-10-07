package main

// rootliken.go keeps the library root free of a .liken directory. A .liken
// directory holds the ledgers of the titles in its folder, and the root holds
// no title: the walk reads titles only in the folders below it, and a rescan
// of the root walks the whole root instead. So no fact belongs to the root,
// and nothing reads a .liken directory there. The close container of every
// library Job removes it, with whatever files it holds.
//
// Two clusters can mount one volume. One cluster's close container then
// removes the other's directory too, which costs that cluster nothing,
// because no fact of either cluster reads it.

// The close container's call. A failure is logged and never fails the run,
// because the next library Job removes the directory again.
func (r *closeRun) removeRootLiken() {
	removed, err := r.writer.removeRootLiken(r.root)
	if err != nil {
		r.logf("could not remove the %s directory at the library root: %v", likenDirectory, err)
		return
	}
	if removed {
		r.logf("removed the %s directory at the library root, because the root holds no title", likenDirectory)
	}
}
