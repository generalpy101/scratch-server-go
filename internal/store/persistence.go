package store

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"
)

const FILENAME = "data.godb"
const TEMPFILENAME = "data.gotempdb"

/*
	File structure:
	{
		checksum: "",
		data: []
	}
*/

const FileStructure = `
	{
		"checksum": "%s",
		"data": %s
	}
`

type PersistenceEngine struct {
	root      *os.Root
	datastore *Store
	running   bool
}

// fileContent is the envelope Persist writes, matching FileStructure.
// Used to read the stored checksum back.
type fileContent struct {
	Checksum string `json:"checksum"`
	Data     []Item `json:"data"`
}

func NewPersistenceEngine(rootDirName string, datastore *Store) *PersistenceEngine {
	root, err := os.OpenRoot(rootDirName)
	if err != nil {
		panic(err)
	}
	return &PersistenceEngine{
		root:      root,
		datastore: datastore,
		running:   false,
	}
}

func (e *PersistenceEngine) StartPersistence() {
	e.running = true
	for i := 0; i < 1; {

		time.Sleep(5 * time.Second)
		if e.running {
			if err := e.Persist(); err != nil {
				log.Printf("Persistence failed due to %s", err.Error())
			}
		}
	}
}

func (e *PersistenceEngine) StopPersistence() {
	e.running = false
}

func (e *PersistenceEngine) LoadData() error {
	if _, err := e.root.Stat(TEMPFILENAME); err == nil {
		e.root.Remove(TEMPFILENAME)
	} else {

	}
	content, err := e.root.ReadFile(FILENAME)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	existing := fileContent{}
	json.Unmarshal(content, &existing)

	for _, data := range existing.Data {
		e.datastore.Put(data.Kind, data.ID, data.Data)
	}

	return nil
}

// Persist writes the whole store to the file, but only when the data
// changed: it checksums the marshaled items and compares against the
// checksum stored in the file. Same checksum -> same data -> no write.
func (e *PersistenceEngine) Persist() error {
	// Marshal all items as a plain slice. The map key (kind + id) is
	// derivable from each Item itself, so only the values go to disk.
	items := e.datastore.GetOrderedItems()
	jsonRep, err := json.Marshal(items)
	if err != nil {
		return err
	}

	newCheckSum := md5.Sum(jsonRep)
	newCheckSumStr := hex.EncodeToString(newCheckSum[:])

	// Read the stored checksum. A missing/empty/corrupt file just
	// means "no stored checksum".
	content, err := e.root.ReadFile(FILENAME)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	existing := fileContent{}
	json.Unmarshal(content, &existing)

	if newCheckSumStr == existing.Checksum {
		// Data did not change, nothing to write.
		return nil
	}

	// Data changed: rewrite the full file with the new checksum.
	f, err := e.root.OpenFile(TEMPFILENAME, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = fmt.Fprintf(f, FileStructure, newCheckSumStr, jsonRep)
	// e.root.Rename(FILENAME, "root")
	e.root.Rename(TEMPFILENAME, FILENAME)
	return err
}

func main() {
	// Use a scratch dir so the test never touches real data.
	dir, err := os.MkdirTemp("", "godb-test")
	if err != nil {
		fmt.Println("could not create temp dir:", err)
		return
	}
	defer os.RemoveAll(dir)

	root, err := os.OpenRoot(dir)
	if err != nil {
		fmt.Println("could not open root:", err)
		return
	}
	defer root.Close()

	// Persist() reads the file before writing it, so it must exist.
	// Seed it with the empty file structure (and a fake checksum that
	// will not match, so the first Persist always writes).
	f, err := root.OpenFile(FILENAME, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		fmt.Println("could not seed file:", err)
		return
	}
	fmt.Fprintf(f, FileStructure, "123", "[]")
	f.Close()

	// Put some data in the store so Persist has something to work with.
	datastore := New()
	datastore.Put("todo", "", map[string]any{"title": "buy milk", "completed": false})
	datastore.Put("todo", "", map[string]any{"title": "finish stage 1", "completed": true})

	engine := &PersistenceEngine{
		root:      root,
		datastore: datastore,
	}

	if err := engine.Persist(); err != nil {
		fmt.Println("Persist failed:", err)
		return
	}

	// Show what the file looks like afterwards.
	content, err := root.ReadFile(FILENAME)
	if err != nil {
		fmt.Println("could not read back file:", err)
		return
	}
	fmt.Printf("file contents after first Persist:\n%s\n", content)

	// Second Persist with unchanged data: the checksum should match,
	// so the file must stay untouched (mtime unchanged).
	before, err := root.Stat(FILENAME)
	if err != nil {
		fmt.Println("could not stat file:", err)
		return
	}

	if err := engine.Persist(); err != nil {
		fmt.Println("second Persist failed:", err)
		return
	}

	after, err := root.Stat(FILENAME)
	if err != nil {
		fmt.Println("could not stat file:", err)
		return
	}

	if after.ModTime().Equal(before.ModTime()) {
		fmt.Println("second Persist: checksum matched, file untouched (correct)")
	} else {
		fmt.Println("second Persist: file was rewritten even though data did not change")
	}

	datastore.Put("todo", "", map[string]any{"title": "finish stage 1", "completed": true})

	if err := engine.Persist(); err != nil {
		fmt.Println("second Persist failed:", err)
		return
	}

	after, err = root.Stat(FILENAME)
	if err != nil {
		fmt.Println("could not stat file:", err)
		return
	}

	if after.ModTime().Equal(before.ModTime()) {
		fmt.Println("second Persist: checksum matched, file untouched (correct)")
	} else {
		fmt.Println("second Persist: file was rewritten even though data did not change")
	}
}
