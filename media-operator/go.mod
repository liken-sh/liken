// One module, one package, one program. The Go version matches
// liken's, because the operator deploys onto liken clusters and
// follows the same build discipline: a static binary, no cgo.
module github.com/liken-sh/media-operator

go 1.26.5

require github.com/dhowden/tag v0.0.0-20240417053706-3d75831295e8
