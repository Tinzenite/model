package model

import (
	"encoding/json"
	"io/ioutil"
	"sort"

	"github.com/tinzenite/shared"
)

/*
Create a new model at the specified path for the given peer id. Will not
immediately update, must be explicitely called.
*/
func Create(root string, peerid string, storePath string) (*Model, error) {
	if root == "" || peerid == "" {
		return nil, shared.ErrIllegalParameters
	}
	if !shared.IsTinzenite(root) {
		return nil, shared.ErrNotTinzenite
	}
	m := &Model{
		Root:         root,
		TrackedPaths: make(map[string]bool),
		StaticInfos:  make(map[string]staticinfo),
		SelfID:       peerid,
		AllowLogging: true,
		storePath:    storePath}
	return m, nil
}

/*
Load a model for the given path, depending whether a model.json exists for it
already.
*/
func Load(root string) (*Model, error) {
	if root == "" {
		return nil, shared.ErrIllegalParameters
	}
	if !shared.IsTinzenite(root) {
		return nil, shared.ErrNotTinzenite
	}
	var m *Model
	data, err := ioutil.ReadFile(root + "/" + shared.TINZENITEDIR + "/" + shared.LOCALDIR + "/" + shared.MODELJSON)
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
