package content

import (
	"log"
	"os"
)

type FileStore struct {
	root *os.Root
}

func OpenFileStore(directory string) *FileStore {
	store := FileStore{}
	root, err := os.OpenRoot(directory)

	if err != nil {
		log.Panicf("Error while opening directory as root %s\nError: %s\n", directory, err.Error())
	}

	store.root = root

	return &store
}

func (s *FileStore) CloseFileStore() {
	err := s.root.Close()

	if err != nil {
		log.Panicf("Error while closing file store: %s\n", err.Error())
	}
}

func (s *FileStore) Read(name string) ([]byte, error) {
	f, err := s.root.ReadFile(name)

	return f, err
}
