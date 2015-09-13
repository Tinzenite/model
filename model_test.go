package model

import (
	"io/ioutil"
	"os"
	"testing"

	"github.com/tinzenite/shared"
)

func TestCreate(t *testing.T) {
	root := makeDefaultDirectory()
	defer removeTemp(root)
	// test normal legal create
	_, err := Create(root, PEERID, root+"/"+shared.STOREMODELDIR)
	if err != nil {
		t.Error(err)
	}
	// test illegal parameters
	_, err = Create(root, "", root+"/"+shared.STOREMODELDIR)
	if err != shared.ErrIllegalParameters {
		t.Error("Expected", shared.ErrIllegalParameters, "got", err)
	}
	_, err = Create("", PEERID, "/"+shared.STOREMODELDIR)
	if err != shared.ErrIllegalParameters {
		t.Error("Expected", shared.ErrIllegalParameters, "got", err)
	}
	_, err = Create(root, PEERID, "")
	if err != shared.ErrIllegalParameters {
		t.Error("Expected", shared.ErrIllegalParameters, "got", err)
	}
	_, err = Create("", "", "")
	if err != shared.ErrIllegalParameters {
		t.Error("Expected", shared.ErrIllegalParameters, "got", err)
	}
}

func TestLoad(t *testing.T) {
	root := makeDefaultDirectory()
	defer removeTemp(root)
	// must first create, update, and store a model so that we can load it
	model, _ := Create(root, PEERID, root+"/"+shared.STOREMODELDIR)
	model.Update()
	model.Store()
	// load
	loaded, err := LoadFrom(root + "/" + shared.STOREMODELDIR)
	if err != nil {
		t.Log("Load failed:", err)
	}
	// sanity check
	if loaded.IsEmpty() {
		t.Log("Expected loaded to be non empty!")
	}
	// check with wrong parameter
	_, err = LoadFrom("")
	if err != shared.ErrIllegalParameters {
		t.Error("Expected", shared.ErrIllegalParameters, "got", err)
	}
}

func TestModel_IsEmpty(t *testing.T) {
	root := makeDefaultDirectory()
	defer removeTemp(root)
	// make model
	model, _ := Create(root, PEERID, root+"/"+shared.STOREMODELDIR)
	// should be empty since we haven't updated model yet
	if !model.IsEmpty() {
		t.Error("Expected IsEmpty to return true")
	}
	// update model to reflect contents
	_ = model.Update()
	// should now be non empty
	if model.IsEmpty() {
		t.Error("Expected IsEmpty to return false")
	}
}

func TestModel_IsTracked(t *testing.T) {
	root := makeDefaultDirectory()
	defer removeTemp(root)
	// make model
	model, _ := Create(root, PEERID, root+"/"+shared.STOREMODELDIR)
	// shouldn't be tracked yet
	if model.IsTracked(root) == true {
		t.Error("Expected IsTracked to return true")
	}
	model.Update()
	// now should be tracked
	if model.IsTracked(root) == false {
		t.Error("Expected IsTracked to return false")
	}
}

func TestModel_Update(t *testing.T) {
	root := makeDefaultDirectory()
	defer removeTemp(root)
	// create model
	model, _ := Create(root, PEERID, root+"/"+shared.STOREMODELDIR)
	// test default update
	err := model.Update()
	if err != nil {
		t.Error(err)
	}
	// test create update NOTE: EXAMPLE for now only, improve!
	fileFour, _ := ioutil.TempFile(root, FOUR)
	if model.IsTracked(fileFour.Name()) == true {
		t.Error("Expected file to be untracked")
	}
	err = model.Update()
	if err != nil {
		t.Error(err)
	}
	if model.IsTracked(fileFour.Name()) == false {
		t.Error("Expected file to be tracked")
	}
}

func TestModel_PartialUpdate(t *testing.T) {
	root := makeDefaultDirectory()
	defer removeTemp(root)
	// create model
	model, _ := Create(root, PEERID, root+"/"+shared.STOREMODELDIR)
	_ = model.Update()
	// make subdir which we want tracked and file we don't want tracked
	subdir := makeTempDir(root, "track")
	track := makeTempFile(subdir, "track.me")
	untracked := makeTempFile(root, "dnt.txt")
	paths := []string{subdir, track, untracked}
	for _, path := range paths {
		if model.IsTracked(path) == true {
			t.Error("Expected file", path, "to be untracked")
		}
	}
	// now partial update the subdir
	err := model.PartialUpdate(subdir)
	if err != nil {
		t.Error(err)
	}
	// test that all but untracked are now tracked
	for _, path := range paths {
		if path == untracked {
			if model.IsTracked(untracked) == true {
				t.Error("Expected file untracked to be untracked")
			}
			continue
		}
		if model.IsTracked(path) == false {
			t.Error("Expected file ", path, "to be tracked")
		}
	}
}

// ------------------------- UTILITY FUNCTIONS ---------------------------------

// PEERID is the peerid used for testing.
const PEERID = "testing"

// This are the names of the objects used for testing.
const (
	ROOT   = "root"
	SUBDIR = "subdir"
	ONE    = "one"
	TWO    = "two"
	THREE  = "three"
	FOUR   = "four"
)

/*
makeTempDirectory writes a temp directory and returns the path to it.
*/
func makeDefaultDirectory() string {
	root, _ := ioutil.TempDir("", ROOT)
	_ = makeTempFile(root, ONE)
	_ = makeTempFile(root, TWO)
	subdir := makeTempDir(root, SUBDIR)
	_ = makeTempFile(subdir, THREE)
	// to make the dir valid:
	shared.MakeDotTinzenite(root)
	return root
}

func makeTempFile(path, name string) string {
	file, _ := ioutil.TempFile(path, name)
	return file.Name()
}

func makeTempDir(path, name string) string {
	subdir, _ := ioutil.TempDir(path, name)
	return subdir
}

/*
removeTempDirectory removes everything contained within the path.
*/
func removeTemp(path string) {
	os.RemoveAll(path)
}
