package model

import (
	"io/ioutil"
	"os"
	"testing"

	"github.com/tinzenite/shared"
)

func TestCreate(t *testing.T) {
	root := makeTempDirectory()
	defer removeTempDirectory(root)
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
	root := makeTempDirectory()
	defer removeTempDirectory(root)
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
	root := makeTempDirectory()
	defer removeTempDirectory(root)
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
	root := makeTempDirectory()
	defer removeTempDirectory(root)
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
	root := makeTempDirectory()
	defer removeTempDirectory(root)
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
	// TODO complete. Must find a way of modifying single files so we can check
	// if they are tracked then. Should I split for CREATE, MODIFY, and REMOVE?
	t.Log("Incomplete test, TODO!")
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
func makeTempDirectory() string {
	root, _ := ioutil.TempDir("", ROOT)
	_, _ = ioutil.TempFile(root, ONE)
	_, _ = ioutil.TempFile(root, TWO)
	subdir, _ := ioutil.TempDir(root, SUBDIR)
	_, _ = ioutil.TempFile(subdir, THREE)
	// to make the dir valid:
	shared.MakeDotTinzenite(root)
	return root
}

/*
removeTempDirectory removes everything contained within the path.
*/
func removeTempDirectory(path string) {
	os.RemoveAll(path)
}
