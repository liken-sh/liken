module github.com/chrisguidry/liken/operator

go 1.26.4

// The machine package lives in this repo, not on a module proxy; the
// replace directive is what lets this module build standalone.
replace github.com/chrisguidry/liken/machine => ../machine

require github.com/chrisguidry/liken/machine v0.0.0-00010101000000-000000000000

require (
	go.yaml.in/yaml/v2 v2.4.2 // indirect
	sigs.k8s.io/yaml v1.6.0 // indirect
)
