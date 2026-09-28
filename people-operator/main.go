// This repository ships a CRD and no controller. The module exists
// so `make test` can prove the CRD's shape.
package main

import "fmt"

func main() {
	fmt.Println("people.liken.sh Person")
}
