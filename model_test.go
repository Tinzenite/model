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
	_, err := Create(root, "peerid")
	if err != nil {
		t.Error(err)
	}
	// test illegal parameters
	_, err = Create(root, "")
	if err != shared.ErrIllegalParameters {
		t.Error("Expected", shared.ErrIllegalParameters, "got", err)
	}
	_, err = Create("", "peerid")
	if err != shared.ErrIllegalParameters {
		t.Error("Expected", shared.ErrIllegalParameters, "got", err)
	}
	_, err = Create("", "")
	if err != shared.ErrIllegalParameters {
		t.Error("Expected", shared.ErrIllegalParameters, "got", err)
	}
}

func TestLoad(t *testing.T) {
	t.SkipNow()
}

func TestModel_IsEmpty(t *testing.T) {
	root := makeTempDirectory()
	defer removeTempDirectory(root)
	// make model
	model, err := Create(root, "peerid")
	if err != nil {
		t.Error(err)
	}
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

/*
makeTempDirectory writes a temp directory and returns the path to it.
*/
func makeTempDirectory() string {
	root, _ := ioutil.TempDir("", "root")
	_, _ = ioutil.TempFile(root, "one")
	_, _ = ioutil.TempFile(root, "two")
	subdir, _ := ioutil.TempDir(root, "subdir")
	_, _ = ioutil.TempFile(subdir, "three")
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
