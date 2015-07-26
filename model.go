package model

import (
	"encoding/json"
	"errors"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tinzenite/shared"
)

/*
Model of a directory and its contents.
*/
type Model struct {
	Root         string
	SelfID       string
	TrackedPaths map[string]bool
	StaticInfos  map[string]staticinfo
	updatechan   chan shared.UpdateMessage
}

/*
CreateModel creates a new model at the specified path for the given peer id. Will
not immediately update, must be explicitely called.
*/
func CreateModel(root, peerid string) (*Model, error) {
	if !shared.IsTinzenite(root) {
		return nil, shared.ErrNotTinzenite
	}
	m := &Model{
		Root:         root,
		TrackedPaths: make(map[string]bool),
		StaticInfos:  make(map[string]staticinfo),
		SelfID:       peerid}
	return m, nil
}

/*
LoadModel loads or creates a model for the given path, depending whether a
model.json exists for it already.
*/
func LoadModel(root string) (*Model, error) {
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
Update the complete model state.
*/
func (m *Model) Update() error {
	return m.PartialUpdate(m.Root)
}

/*
PartialUpdate of the model state. Scope is the the FULL path of the scope in
absolute terms!

TODO Get concurrency to work here. Last time I had trouble with the Objinfo map.
TODO Make scope a RelativePath?
*/
func (m *Model) PartialUpdate(scope string) error {
	if m.TrackedPaths == nil || m.StaticInfos == nil {
		return shared.ErrNilInternalState
	}
	current, err := m.populateMap()
	if err != nil {
		return err
	}
	// we'll need this for every create* op, so create only once:
	relPath := shared.CreatePathRoot(m.Root)
	// now: compare old tracked with new version
	var removed, created []string
	for subpath := range m.TrackedPaths {
		// ignore if not in partial update path AND not part of path to scope
		if !strings.HasPrefix(m.Root+"/"+subpath, scope) && !strings.Contains(scope, m.Root+"/"+subpath) {
			continue
		}
		_, ok := current[subpath]
		if ok {
			// paths that still exist must only be checked for MODIFY
			delete(current, subpath)
			m.ApplyModify(relPath.Apply(subpath), nil)
		} else {
			// REMOVED - paths that don't exist anymore have been removed
			removed = append(removed, subpath)
		}
	}
	// CREATED - any remaining paths are yet untracked in m.tracked
	for subpath := range current {
		// ignore if not in partial update path AND not part of path to scope
		if !strings.HasPrefix(m.Root+"/"+subpath, scope) && !strings.Contains(scope, m.Root+"/"+subpath) {
			continue
		}
		created = append(created, subpath)
	}
	// update m.Tracked
	for _, subpath := range removed {
		m.ApplyRemove(relPath.Apply(subpath), nil)
	}
	for _, subpath := range created {
		// nil for version because new local object
		m.ApplyCreate(relPath.Apply(subpath), nil)
	}
	// ensure that removes are handled
	err = m.checkRemove()
	if err != nil {
		return err
	}
	// finally also store the model for future loads.
	return m.Store()
}

/*
SyncModel TODO
*/
func (m *Model) SyncModel(root *shared.ObjectInfo) ([]*shared.UpdateMessage, error) {
	/*
		TODO: how to implement this.
		Maybe: make a check method that simply returns whether Tinzenite needs to
		fetch the file? Can then use ApplyUpdateMessage to trigger actual update...

		Will also need to work on how TINZENITE fetches the files (from multiple etc.)
	*/
	return nil, shared.ErrUnsupported
}

/*
SyncObject returns an UpdateMessage of the change we may need to apply if
applicable. May return nil, that means that the update must not be applied (for
example if the object has not changed).
*/
func (m *Model) SyncObject(obj *shared.ObjectInfo) (*shared.UpdateMessage, error) {
	// we'll need the local path so create that up front
	path := shared.CreatePath(m.Root, obj.Path)
	// modfiy
	_, exists := m.TrackedPaths[path.SubPath()]
	if exists {
		// get staticinfo
		stin, ok := m.StaticInfos[path.SubPath()]
		if !ok {
			return nil, errModelInconsitent
		}
		// sanity checks
		if stin.Identification != obj.Identification || stin.Directory != obj.Directory {
			return nil, errMismatch
		}
		/*TODO what about directories?*/
		if stin.Content == obj.Content {
			/*TODO what about the version numbers?*/
			log.Println("No update required!")
			return nil, nil
		}
		um := shared.CreateUpdateMessage(shared.OpModify, *obj)
		return &um, nil
	}
	log.Println("Create and delete not yet implemented!")
	return nil, shared.ErrUnsupported
}

/*
ApplyUpdateMessage takes an update message and applies it to the model. Should
be called after the file operation has been applied but before the next update!
*/
/*TODO catch shadow files*/
func (m *Model) ApplyUpdateMessage(msg *shared.UpdateMessage) error {
	path := shared.CreatePath(m.Root, msg.Object.Path)
	var err error
	switch msg.Operation {
	case shared.OpCreate:
		err = m.ApplyCreate(path, &msg.Object)
	case shared.OpModify:
		err = m.ApplyModify(path, &msg.Object)
	case shared.OpRemove:
		err = m.ApplyRemove(path, &msg.Object)
	default:
		log.Printf("Unknown operation in UpdateMessage: %s\n", msg.Operation)
		return shared.ErrUnsupported
	}
	if err != nil {
		return err
	}
	// store updates to disk
	return m.Store()
}

/*
Register the channel over which UpdateMessage can be received. Tinzenite will
only ever write to this channel, never read.
*/
func (m *Model) Register(v chan shared.UpdateMessage) {
	m.updatechan = v
}

/*
Read builds the complete Objectinfo representation of this model to its full
depth. Incredibly fast because we only link objects based on the current state
of the model: hashes etc are not recalculated.
*/
func (m *Model) Read() (*shared.ObjectInfo, error) {
	var allObjs shared.Sortable
	rpath := shared.CreatePathRoot(m.Root)
	// getting all Objectinfos is very fast because the staticinfo already exists for all of them
	for fullpath := range m.TrackedPaths {
		obj, err := m.GetInfo(rpath.Apply(fullpath))
		if err != nil {
			log.Println(err.Error())
			continue
		}
		allObjs = append(allObjs, obj)
	}
	// sort so that we can linearly run through based on the path
	sort.Sort(allObjs)
	// build the tree!
	root := allObjs[0]
	/*build tree recursively*/
	m.FillInfo(root, allObjs)
	return root, nil
}

/*
Store the model to disk in the correct directory.
*/
func (m *Model) Store() error {
	path := m.Root + "/" + shared.TINZENITEDIR + "/" + shared.LOCALDIR + "/" + shared.MODELJSON
	jsonBinary, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return ioutil.WriteFile(path, jsonBinary, shared.FILEPERMISSIONMODE)
}

/*
FilePath returns the full path of whatever object satisfies the identification.
*/
func (m *Model) FilePath(identification string) (string, error) {
	for path, stin := range m.StaticInfos {
		if stin.Identification == identification {
			return m.Root + "/" + path, nil
		}
	}
	return "", errors.New("corresponding file for id <" + identification + "> not found")
}

/*
GetIdentification returns the ID of an object at the given path.
*/
func (m *Model) GetIdentification(path *shared.RelativePath) (string, error) {
	stin, ok := m.StaticInfos[path.SubPath()]
	if !ok {
		return "", shared.ErrUntracked
	}
	return stin.Identification, nil
}

/*
GetInfo creates the Objectinfo for the given path, so long as the path is
contained in m.Tracked. Directories are NOT traversed!
*/
func (m *Model) GetInfo(path *shared.RelativePath) (*shared.ObjectInfo, error) {
	_, exists := m.TrackedPaths[path.SubPath()]
	if !exists {
		log.Printf("Error: %s\n", path.FullPath())
		return nil, shared.ErrUntracked
	}
	// get staticinfo
	stin, exists := m.StaticInfos[path.SubPath()]
	if !exists {
		log.Printf("Error: %s\n", path.FullPath())
		return nil, shared.ErrUntracked
	}
	stat, err := os.Lstat(path.FullPath())
	if err != nil {
		return nil, err
	}
	// build object
	object := &shared.ObjectInfo{
		Identification: stin.Identification,
		Name:           path.LastElement(),
		Path:           path.SubPath(),
		Shadow:         false,
		Version:        stin.Version}
	if stat.IsDir() {
		object.Directory = true
		object.Content = ""
	} else {
		object.Directory = false
		object.Content = stin.Content
	}
	return object, nil
}

/*
FillInfo takes an Objectinfo and a list of candidates and recursively fills its
Objects slice. If root is a file it simply returns root.
*/
func (m *Model) FillInfo(root *shared.ObjectInfo, all []*shared.ObjectInfo) *shared.ObjectInfo {
	if !root.Directory {
		// this may be an error, check later
		return root
	}
	rpath := shared.CreatePath(m.Root, root.Path)
	for _, obj := range all {
		if obj == root {
			// skip self
			continue
		}
		path := rpath.Apply(m.Root + "/" + obj.Path)
		if path.Depth() != rpath.Depth()+1 {
			// ignore any out of depth objects
			continue
		}
		if !strings.Contains(path.FullPath(), rpath.FullPath()) {
			// not in my directory
			continue
		}
		// if reached the object is in our subdir, so add and recursively fill
		root.Objects = append(root.Objects, m.FillInfo(obj, all))
	}
	return root
}

/*
ApplyCreate applies a create operation to the local model given that the file
exists. NOTE: In the case of a file, requires the object to exist in the TEMPDIR
named as the object indentification.
*/
func (m *Model) ApplyCreate(path *shared.RelativePath, remoteObject *shared.ObjectInfo) error {
	// ensure no file has been written already
	localCreate := shared.FileExists(path.FullPath())
	// sanity check if the object already exists locally
	_, ok := m.TrackedPaths[path.SubPath()]
	if ok {
		log.Printf("Object at <%s> exists locally! Can not apply create!\n", path.FullPath())
		return shared.ErrConflict
	}
	// NOTE: we don't explicitely check m.Objinfo because we'll just overwrite it if already exists
	var stin *staticinfo
	var err error
	// if remote create
	if remoteObject != nil {
		// create conflict
		if localCreate {
			return shared.ErrConflict
		}
		// dirs are made directly, files have to be moved from temp
		if remoteObject.Directory {
			err := shared.MakeDirectory(path.FullPath())
			if err != nil {
				return err
			}
		} else {
			// apply file op
			err := m.applyFile(remoteObject.Identification, path.FullPath())
			if err != nil {
				return err
			}
		}
		// build staticinfo
		stin, err = createStaticInfo(path.FullPath(), m.SelfID)
		if err != nil {
			return err
		}
		// apply external attributes
		stin.applyObjectInfo(remoteObject)
	} else {
		if !localCreate {
			return shared.ErrIllegalFileState
		}
		// build staticinfo
		stin, err = createStaticInfo(path.FullPath(), m.SelfID)
		if err != nil {
			return err
		}
	}
	// add obj to local model
	m.TrackedPaths[path.SubPath()] = true
	m.StaticInfos[path.SubPath()] = *stin
	localObj, _ := m.GetInfo(path)
	m.notify(shared.OpCreate, path, localObj)
	return nil
}

/*
ApplyModify checks for modifications and if valid applies them to the local model.
Conflicts will result in deletion of the old file and two creations of both versions
of the conflict. NOTE: In the case of a file, requires the object to exist in the
TEMPDIR named as the object indentification.
*/
func (m *Model) ApplyModify(path *shared.RelativePath, remoteObject *shared.ObjectInfo) error {
	// ensure file has been written
	if !shared.FileExists(path.FullPath()) {
		return shared.ErrIllegalFileState
	}
	// sanity check
	_, ok := m.TrackedPaths[path.SubPath()]
	if !ok {
		log.Println("Object doesn't exist locally!")
		return shared.ErrIllegalFileState
	}
	// fetch stin
	stin, ok := m.StaticInfos[path.SubPath()]
	if !ok {
		return errModelInconsitent
	}
	// flag whether the local file has been modified
	localModified := m.isModified(path)
	// check for remote modifications
	if remoteObject != nil {
		/*TODO implement conflict behaviour!*/
		// if remote change the local file may not have been modified
		if localModified {
			log.Println("Merge error! Untracked local changes!")
			return shared.ErrConflict
		}
		// detect conflict
		ver, ok := stin.Version.Valid(remoteObject.Version, m.SelfID)
		if !ok {
			log.Println("Merge error!")
			return shared.ErrConflict
		}
		// apply version update
		stin.Version = ver
		// if file apply file diff
		if !remoteObject.Directory {
			// apply the file op
			err := m.applyFile(stin.Identification, path.FullPath())
			if err != nil {
				return err
			}
		} else {
			/*TODO can this happen for directories? Only once move is implemented, right?*/
			log.Println("WARNING: modify not implemented for directories!")
		}
	} else {
		if !localModified {
			// nothing to do, done
			return nil
		}
		// update version for local change
		stin.Version.Increase(m.SelfID)
	}
	// update hash and modtime
	err := stin.updateFromDisk(path.FullPath())
	if err != nil {
		return err
	}
	// apply updated
	m.StaticInfos[path.SubPath()] = stin
	localObj, _ := m.GetInfo(path)
	m.notify(shared.OpModify, path, localObj)
	return nil
}

/*
ApplyRemove applies a remove operation.

TODO implement me next! First correctly for local changes, then for external!
*/
func (m *Model) ApplyRemove(path *shared.RelativePath, remoteObject *shared.ObjectInfo) error {
	remoteRemove := remoteObject != nil
	localFileExists := shared.FileExists(path.FullPath())
	log.Println("Remote?", remoteRemove, ": Local exists?", localFileExists)
	// if locally initiated, just apply
	if !remoteRemove {
		// if not a remote remove the deletion must be applied locally
		return m.localRemove(path)
	}
	// local remove
	if localFileExists {
		// remove file
		err := os.Remove(path.FullPath())
		if err != nil {
			log.Println("Couldn't remove file")
			return err
		}
		// apply local deletion
		err = m.localRemove(path)
		if err != nil {
			log.Println("local remove didn't work")
			return err
		}
	}
	// if we get a removal from another peer has that peer seen the deletion?
	/*TODO write peer file for this case... notify?*/
	return nil
}

/*
checkRemove checks whether a remove can be finally applied and purged from the
model dependent on the peers in done and check.
*/
func (m *Model) checkRemove() error {
	removeDir := m.Root + "/" + shared.TINZENITEDIR + "/" + shared.REMOVEDIR
	allRemovals, err := ioutil.ReadDir(removeDir)
	if err != nil {
		log.Println("reading all removals failed")
		return err
	}
	// check for each removal
	for _, stat := range allRemovals {
		objRemovePath := removeDir + "/" + stat.Name()
		allCheck, err := ioutil.ReadDir(objRemovePath + "/" + shared.REMOVECHECKDIR)
		if err != nil {
			log.Println("Failed reading check peer list!")
			return err
		}
		// test whether we can remove it
		complete := true
		for _, peerStat := range allCheck {
			if !shared.FileExists(objRemovePath + "/" + shared.REMOVEDONEDIR + "/" + peerStat.Name()) {
				complete = false
				break
			}
		}
		if complete {
			err := m.directRemove(shared.CreatePathRoot(m.Root).Apply(objRemovePath))
			if err != nil {
				log.Println("Failed to direct remove!")
				return err
			}
		}
	}
	return nil
}

/*
localRemove initiates a deletion locally, creating all necessary files and
removing the file from the model.
*/
func (m *Model) localRemove(path *shared.RelativePath) error {
	removeDirectory := m.Root + "/" + shared.TINZENITEDIR + "/" + shared.REMOVEDIR
	// get stin for notify
	stin := m.StaticInfos[path.SubPath()]
	// remove from model
	err := m.directRemove(path)
	if err != nil {
		return err
	}
	// make directories
	err = shared.MakeDirectories(removeDirectory+"/"+stin.Identification, shared.REMOVECHECKDIR, shared.REMOVEDONEDIR)
	if err != nil {
		log.Println("Making dir error")
		return err
	}
	// write peer list to check which must all be notified of removal
	peers, err := m.readPeers()
	if err != nil {
		log.Println("Failed to read peers")
		return err
	}
	for _, peer := range peers {
		err = ioutil.WriteFile(removeDirectory+"/"+stin.Identification+"/"+shared.REMOVECHECKDIR+"/"+peer, []byte(""), shared.FILEPERMISSIONMODE)
		if err != nil {
			log.Println("Couldn't write peer file", peer, "to check!")
			return err
		}
	}
	// write own peer file also to done dir
	err = ioutil.WriteFile(removeDirectory+"/"+stin.Identification+"/"+shared.REMOVEDONEDIR+"/"+m.SelfID, []byte(""), shared.FILEPERMISSIONMODE)
	if err != nil {
		log.Println("Couldn't write own peer file to done!")
		return err
	}
	// make sure deletion is caught
	err = m.PartialUpdate(removeDirectory) /*TODO can this cause recursion? CHECK!*/
	if err != nil {
		log.Println("Error partial updating!")
		return err
	}
	// send notify
	notifyObj := &shared.ObjectInfo{
		Identification: stin.Identification,
		Name:           path.LastElement(),
		Content:        stin.Content,
		Version:        stin.Version,
		Directory:      stin.Directory}
	m.notify(shared.OpRemove, path, notifyObj)
	return nil
}

/*
directRemove removes the given path from the model immediately without notifying
the update. NOTE: Do not use unless this is the behaviour you want! This method
is specifically a part of the normal applyRemove method, do not call outside
of it!
*/
func (m *Model) directRemove(path *shared.RelativePath) error {
	objList, err := m.partialPopulateMap(path.FullPath())
	if err != nil {
		log.Println("partialPopulateMap failed in directRemove")
		return err
	}
	// iterate over each path
	for obj := range objList {
		relPath := path.Apply(obj)
		// if it still exists --> remove
		if shared.FileExists(relPath.FullPath()) {
			err := os.RemoveAll(relPath.FullPath())
			if err != nil {
				log.Println("directRemove failed to remove the file itself!")
				return err
			}
		}
		// remove from model
		delete(m.TrackedPaths, relPath.SubPath())
		delete(m.StaticInfos, relPath.SubPath())
	}
	return nil
}

/*
isModified checks whether a file has been modified.
*/
func (m *Model) isModified(path *shared.RelativePath) bool {
	stin, ok := m.StaticInfos[path.SubPath()]
	if !ok {
		log.Println("Staticinfo lookup failed for", path.SubPath(), "!")
		return false
	}
	// no need for further work here
	if stin.Directory {
		return false
	}
	// if modtime still the same no need to hash again
	stat, err := os.Lstat(path.FullPath())
	if err != nil {
		log.Println(err.Error())
		// Note that we don't return here because we can still continue without this check
	} else {
		if stat.ModTime() == stin.Modtime {
			return false
		}
	}
	hash, err := shared.ContentHash(path.FullPath())
	if err != nil {
		log.Println(err.Error())
		return false
	}
	// if same --> no changes, so done
	if hash == stin.Content {
		return false
	}
	// otherwise a change has happened
	return true
}

/*
applyFile from temp dir to correct path. Checks and executes the move.
*/
func (m *Model) applyFile(identification string, path string) error {
	// path to were the modified file sits before being applied
	temppath := m.Root + "/" + shared.TINZENITEDIR + "/" + shared.TEMPDIR + "/" + identification
	// check that it exists
	_, err := os.Lstat(temppath)
	if err != nil {
		return errMissingUpdateFile
	}
	// move file from temp to correct path, overwritting old version
	return os.Rename(temppath, path)
}

/*
Notify the channel of the operation for the object at path.
*/
func (m *Model) notify(op shared.Operation, path *shared.RelativePath, obj *shared.ObjectInfo) {
	log.Printf("%s: %s\n", op, path.LastElement())
	if m.updatechan != nil {
		if obj == nil {
			log.Println("Failed to notify due to nil obj!")
			return
		}
		m.updatechan <- shared.CreateUpdateMessage(op, *obj)
	}
}

/*
populateMap for the m.root path with all file and directory contents, with the
matcher applied if applicable.
*/
func (m *Model) populateMap() (map[string]bool, error) {
	return m.partialPopulateMap(m.Root)
}

/*
partialPopulateMap for the given path with all file and directory contents within
the given path, with the matcher applied if applicable.
*/
func (m *Model) partialPopulateMap(rootPath string) (map[string]bool, error) {
	relPath := shared.CreatePathRoot(m.Root).Apply(rootPath)
	master, err := CreateMatcher(relPath.RootPath())
	if err != nil {
		return nil, err
	}
	tracked := make(map[string]bool)
	filepath.Walk(relPath.FullPath(), func(subpath string, stat os.FileInfo, inerr error) error {
		// sanity check
		thisPath := relPath.Apply(subpath)
		if thisPath.FullPath() != subpath {
			log.Println("Failed to walk due to wrong path!", thisPath.FullPath())
			return nil
		}
		// resolve matcher
		/*FIXME thie needlessly creates a lot of potential duplicates*/
		match := master.Resolve(thisPath)
		// ignore on match
		if match.Ignore(thisPath.FullPath()) {
			// SkipDir is okay even if file
			if stat.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		// tracked contains path beneath root, so use SubPath as key
		tracked[thisPath.SubPath()] = true
		return nil
	})
	// doesn't directly assign to m.tracked on purpose so that we can reuse this
	// method elsewhere (for the current structure on m.Update())
	return tracked, nil
}

/*
readPeers reads all the peers from the .tinzenite directory and returns a list
of their IDs.
*/
func (m *Model) readPeers() ([]string, error) {
	var IDs []string
	peers, err := shared.LoadPeers(m.Root)
	if err != nil {
		return nil, err
	}
	for _, peer := range peers {
		IDs = append(IDs, peer.Identification)
	}
	return IDs, nil
}
