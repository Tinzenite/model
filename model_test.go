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
	_, err := Create(root, PEERID)
	if err != nil {
		t.Error(err)
	}
	// test illegal parameters
	_, err = Create(root, "")
	if err != shared.ErrIllegalParameters {
		t.Error("Expected", shared.ErrIllegalParameters, "got", err)
	}
	_, err = Create("", PEERID)
	if err != shared.ErrIllegalParameters {
		t.Error("Expected", shared.ErrIllegalParameters, "got", err)
	}
	_, err = Create("", "")
	if err != shared.ErrIllegalParameters {
		t.Error("Expected", shared.ErrIllegalParameters, "got", err)
	}
}

func TestLoad(t *testing.T) {
	root := makeDefaultDirectory()
	defer removeTemp(root)
	// must first create, update, and store a model so that we can load it
	model, _ := Create(root, PEERID)
	model.Update()
	model.Store()
	// load
	loaded, err := Load(root)
	if err != nil {
		t.Log(err)
	}
	// sanity check
	if loaded.IsEmpty() {
		t.Log("Expected loaded to be non empty!")
	}
	// check with wrong parameter
	_, err = Load("")
	if err != shared.ErrIllegalParameters {
		t.Error("Expected", shared.ErrIllegalParameters, "got", err)
	}
}

func TestModel_IsEmpty(t *testing.T) {
	root := makeDefaultDirectory()
	defer removeTemp(root)
	// make model
	model, _ := Create(root, PEERID)
	// should be empty since we haven't updated model yet
	if model.IsEmpty() == false {
		t.Error("Expected IsEmpty to return true")
	}
	// update model to reflect contents
	_ = model.Update()
	// should now be non empty
	if model.IsEmpty() == true {
		t.Error("Expected IsEmpty to return false")
	}
}

func TestModel_IsTracked(t *testing.T) {
	root := makeDefaultDirectory()
	defer removeTemp(root)
	// make model
	model, _ := Create(root, PEERID)
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
	model, _ := Create(root, PEERID)
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
	model, _ := Create(root, PEERID)
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
	err = model.PartialUpdate("scope string")
	if err != nil {
		t.Error(err)
	}
	if model.IsTracked(fileFour.Name()) == false {
		t.Error("Expected file to be tracked")
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
