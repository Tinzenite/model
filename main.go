package model

import (
	"encoding/json"
	"io/ioutil"
	"sort"
	"time"

	"github.com/tinzenite/shared"
)

/*
Create a new model at the specified path for the given peer id. Will not
immediately update, must be explicitely called.
*/
func Create(root string, peerid string, storePath string) (*Model, error) {
	if root == "" || peerid == "" || storePath == "" {
		return nil, shared.ErrIllegalParameters
	}
	if !shared.IsTinzenite(root) {
		return nil, shared.ErrNotTinzenite
	}
	m := &Model{
		RootPath:     root,
		TrackedPaths: make(map[string]bool),
		StaticInfos:  make(map[string]staticinfo),
		SelfID:       peerid,
		StorePath:    storePath}
	return m, nil
}

/*
LoadFrom the given path a model.
*/
func LoadFrom(path string) (*Model, error) {
	if path == "" {
		return nil, shared.ErrIllegalParameters
	}
	var m *Model
	data, err := ioutil.ReadFile(path + "/" + shared.MODELJSON)
	if err != nil {
		return nil, err
	}
	// load as json
	err = json.Unmarshal(data, &m)
	if err != nil {
		return nil, err
	}
	return m, nil
}

/*
Build creates a model from an ObjectInfo tree. NOTE: not to be used for creating
models that work on a file system without setting valid paths!
*/
func Build(object *shared.ObjectInfo) *Model {
	m := &Model{
		RootPath:     "",
		TrackedPaths: make(map[string]bool),
		StaticInfos:  make(map[string]staticinfo),
		SelfID:       "",
		StorePath:    ""}
	// we only call time.Now() once because we don't need perfect accuracy
	modtime := time.Now()
	// apply each object to the model
	object.ForEach(func(this shared.ObjectInfo) {
		m.TrackedPaths[this.Path] = true
		m.StaticInfos[this.Path] = staticinfo{
			Identification: this.Identification,
			Directory:      this.Directory,
			Content:        this.Content,
			Modtime:        modtime,
			Version:        this.Version}
	})
	// done, so return model
	return m
}

/*
sortObjects sorts an array of ObjectInfo by the path length. This ensures that
all updates will be sent in the correct order.
*/
func sortUpdateMessages(list []*shared.UpdateMessage) []*shared.UpdateMessage {
	sortable := shared.SortableUpdateMessage(list)
	sort.Sort(sortable)
	return []*shared.UpdateMessage(sortable)
}

/*
sortPaths sorts an array of strings representing paths by length. This ensures
that directories will always be handled before their contents.
*/
func sortPaths(list []string) []string {
	sortable := shared.SortableString(list)
	sort.Sort(sortable)
	return []string(sortable)
}
