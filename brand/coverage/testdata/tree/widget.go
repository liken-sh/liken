package widget

// Add returns the sum of a and b, and b alone for a negative a.
func Add(a, b int) int {
	if a < 0 {
		println("negative")
		return b
	}
	return a + b
}
