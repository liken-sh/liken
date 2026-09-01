package main

// walk.go reads a library's title folders with a fixed pool of workers. Almost
// all of a walk is waiting: every folder costs a directory read, a sidecar
// read, and a stat of each file, and each of those is a round trip to a network
// volume. Folders share no state, and their rows are written by key, so the
// order they are read in changes nothing, and eight folders read at once wait
// eight times less.
//
// A worker takes one directory and classifies it. A title folder is scanned
// into its rows. A grouping folder is read, and its child directories go back
// to the pool. So the descent through a movies volume's grouping folders is
// parallel with the scanning, and no one worker classifies folders for the
// rest.

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"iter"
	"os"
	"path/filepath"
	"sync"
)

// How many folders the walk reads at once. Eight requests in flight keep a
// network volume busy, without a burst large enough to slow the players that
// read the same server, and the scanner then holds at most eight folders and
// one flush buffer. It is a var so a test drives one worker and eight over the
// same tree.
var walkWorkers = 8

// walkDirectory is one directory waiting for a worker. The depth travels with
// it, because the descent runs in the workers and no one goroutine counts the
// levels for the rest.
type walkDirectory struct {
	path  string
	depth int
}

// folderRule is what one kind needs to walk a directory: a test for a title
// folder, how to scan one into its rows, which names to skip, and how deep to
// descend. The movies rule reads a folder's contents to tell a title folder
// from a grouping folder. Every directory under a series root is a series, so
// the series rule answers yes to all of them and descends no further. The pool
// below is the same code for both kinds.
type folderRule struct {
	isTitle  func(dir string) bool
	scan     func(dir string, result *walkResult)
	ignore   ignoreSet
	maxDepth int
}

// read classifies one directory. A title folder is scanned into its rows. A
// grouping folder hands its child directories back to the pool.
//
// A directory the walk cannot read marks the pass incomplete, wherever it is in
// the tree. The walk would otherwise miss every title under it, and the prune
// would then delete their rows as departed. The one exception is a directory
// below the root that no longer exists, which is a title deleted while the walk
// ran. That is an ordinary event on a live volume, and the next walk reports
// the deletion.
//
// A directory past the depth cap is unread in the same way, so it marks
// the pass incomplete rather than returning silently.
func (r folderRule) read(dir walkDirectory) (*walkResult, []walkDirectory) {
	if dir.depth > 0 && r.isTitle(dir.path) {
		folder := &walkResult{}
		r.scan(dir.path, folder)
		return folder, nil
	}
	if dir.depth > r.maxDepth {
		return unreadDirectory(dir.path, fmt.Errorf("deeper than the cap of %d grouping folders", r.maxDepth)), nil
	}
	entries, err := os.ReadDir(dir.path)
	if err != nil {
		if dir.depth > 0 && errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return unreadDirectory(dir.path, err), nil
	}
	var children []walkDirectory
	for _, entry := range entries {
		// Two lists keep a directory out of the walk: skipName, the
		// closed list of dot-names and service directories that hold no
		// media anywhere, and the ignore set, the folders this Library
		// names. Neither one is read, so neither one marks the pass
		// incomplete.
		if !entry.IsDir() || skipName(entry.Name()) || r.ignore.skips(entry.Name()) {
			continue
		}
		children = append(children, walkDirectory{
			path:  filepath.Join(dir.path, entry.Name()),
			depth: dir.depth + 1,
		})
	}
	return nil, children
}

// unreadDirectory is what one directory the walk could not read leaves
// behind: the incomplete mark that holds the prune back, and the path and the
// error the collector logs. A summary that says only that some read failed
// leaves a person with nothing to fix.
func unreadDirectory(path string, err error) *walkResult {
	return &walkResult{readError: true, readFailures: []walkReadFailure{{path: path, err: err}}}
}

// walkTree streams the title folders under a root to the caller's loop, which
// is the walk's one collector: it appends the rows, sums the counts, and
// flushes a full buffer. The channel is unbuffered, so a worker waits for the
// collector to take its folder, and the rows in flight are the eight the
// workers hold and no more.
func walkTree(ctx context.Context, root string, rule folderRule) iter.Seq[*walkResult] {
	return func(yield func(*walkResult) bool) {
		pool := newWalkPool(walkDirectory{path: root})
		folders := make(chan *walkResult)
		var workers sync.WaitGroup
		for range walkWorkers {
			workers.Add(1)
			go func() {
				defer workers.Done()
				pool.work(ctx, rule, folders)
			}()
		}
		go func() {
			workers.Wait()
			close(folders)
		}()
		// The pool stops and the folders drain, whether the collector read
		// the whole tree or stopped early. The drain releases a worker
		// waiting to hand over a folder, and the channel closes only after
		// every worker has returned, so no worker outlives the walk.
		defer func() {
			pool.stop()
			for range folders {
			}
		}()

		for folder := range folders {
			if !yield(folder) {
				return
			}
		}
	}
}

// walkPool holds the directories waiting for a worker, and the count of the
// directories the walk has not finished.
//
// The count is what ends the walk. A closed channel cannot end it, because a
// worker makes more work: it reads a grouping folder and hands back its
// children. A worker adds those children before it marks its own directory
// finished, so the count reaches zero only when the whole tree is read.
type walkPool struct {
	mutex       sync.Mutex
	ready       *sync.Cond
	waiting     []walkDirectory
	outstanding int
	stopped     bool
}

func newWalkPool(start walkDirectory) *walkPool {
	pool := &walkPool{waiting: []walkDirectory{start}, outstanding: 1}
	pool.ready = sync.NewCond(&pool.mutex)
	return pool
}

// work is one worker. It takes a directory, reads it, hands the rows to the
// collector, and queues whatever children the directory held, until the pool
// empties or the context ends. The context is read between folders, so a
// shutdown stops the walk within one folder and never inside one.
func (p *walkPool) work(ctx context.Context, rule folderRule, folders chan<- *walkResult) {
	for {
		if ctx.Err() != nil {
			p.stop()
			return
		}
		dir, taken := p.take()
		if !taken {
			return
		}
		folder, children := rule.read(dir)
		if folder != nil {
			folders <- folder
		}
		p.add(children)
		p.finish()
	}
}

// take waits for a directory and reports false once the walk has stopped. A
// worker waits only while another worker is still reading, because a wait
// means the queue is empty and the count is not yet zero.
func (p *walkPool) take() (walkDirectory, bool) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	for {
		if p.stopped {
			return walkDirectory{}, false
		}
		if len(p.waiting) > 0 {
			dir := p.waiting[len(p.waiting)-1]
			p.waiting = p.waiting[:len(p.waiting)-1]
			return dir, true
		}
		p.ready.Wait()
	}
}

// add queues the child directories one grouping folder held, and counts them
// as outstanding before the parent reports itself finished.
func (p *walkPool) add(children []walkDirectory) {
	if len(children) == 0 {
		return
	}
	p.mutex.Lock()
	defer p.mutex.Unlock()
	p.waiting = append(p.waiting, children...)
	p.outstanding += len(children)
	p.ready.Broadcast()
}

// finish marks one directory read. The directory that brings the count to zero
// is the last in the tree, so the pool stops there and every worker returns.
func (p *walkPool) finish() {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	p.outstanding--
	if p.outstanding == 0 {
		p.stopped = true
		p.ready.Broadcast()
	}
}

// stop ends every worker, whether the tree was read or not. A cancelled
// context and a collector that stopped reading both end the walk this way.
func (p *walkPool) stop() {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	p.stopped = true
	p.ready.Broadcast()
}
