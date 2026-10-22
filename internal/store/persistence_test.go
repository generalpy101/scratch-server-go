package store

import "testing"

// TestPersistence runs the persistence engine's manual test driver.
//
//	go test ./internal/store/ -run TestPersistence -v
func TestPersistence(t *testing.T) {
	main()
}
