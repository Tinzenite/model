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
	"time"

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
	/*TODO make AllowLogging setable somewhere, for now always on*/
	AllowLogging bool
	updatechan   chan shared.UpdateMessage
}

/*
Create a new model at the specified path for the given peer id. Will not
immediately update, must be explicitely called.
*/
func Create(root, peerid string) (*Model, error) {
	if !shared.IsTinzenite(root) {
		return nil, shared.ErrNotTinzenite
	}
	m := &Model{
		Root:         root,
		TrackedPaths: make(map[string]bool),
		StaticInfos:  make(map[string]staticinfo),
		SelfID:       peerid,
		AllowLogging: true}
	return m, nil
}

/*
Load a model for the given path, depending whether a model.json exists for it
already.
*/
func Load(root string) (*Model, error) {
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
*/
func (m *Model) PartialUpdate(scope string) error {
	// update local model
	err := m.updateLocal(scope)
	if err != nil {
		return err
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
SyncModel takes the root ObjectInfo of the foreign model and returns an amount of
UpdateMessages required to update the current model to the foreign model. These
must still be applied!
*/
func (m *Model) SyncModel(root *shared.ObjectInfo) ([]*shared.UpdateMessage, error) {
	// we'll need the simple lists of the foreign model for both cases
	foreignPaths := make(map[string]bool)
	foreignObjs := make(map[string]*shared.ObjectInfo)
	root.ForEach(func(obj shared.ObjectInfo) {
		// write to paths
		foreignPaths[obj.Path] = true
		// strip of children and write to objects
		obj.Objects = nil
		foreignObjs[obj.Path] = &obj
	})
	// make sure that the .TINZENITEDIR is compatible!
	authPath := shared.TINZENITEDIR + "/" + shared.ORGDIR + "/" + shared.AUTHJSON
	foreignAuth, exists := foreignObjs[authPath]
	if !exists {
		m.log("SyncModel: auth doesn't exist in foreign model!")
		return nil, shared.ErrIllegalFileState
	}
	localAuth, err := m.GetInfo(shared.CreatePath(m.Root, authPath))
	if err != nil {
		m.log("SyncModel: local model doesn't have auth!")
		return nil, shared.ErrIllegalFileState
	}
	if foreignAuth.Content != localAuth.Content {
		m.log("SyncModel: models seem to be incompatible!")
		return nil, errIncompatibleModel
	}
	// compare to local version
	created, modified, removed := m.compareMaps(m.Root, foreignPaths)
	// build update messages
	var umList []*shared.UpdateMessage
	for _, subpath := range created {
		remObj, exists := foreignObjs[subpath]
		if !exists {
			m.warn("Created path", subpath, "doesn't exist in remote model!")
			continue
		}
		um := shared.CreateUpdateMessage(shared.OpCreate, *remObj)
		umList = append(umList, &um)
	}
	for _, subpath := range modified {
		localObj, err := m.GetInfo(shared.CreatePath(m.Root, subpath))
		if err != nil {
			m.log("SyncModel: failed to fetch local obj for modify check!")
			continue
		}
		remObj, exists := foreignObjs[subpath]
		if !exists {
			m.warn("Modified path", subpath, "doesn't exist in remote model!")
			continue
		}
		// check if same – if not some modify has happened
		if !localObj.Equal(remObj) {
			um := shared.CreateUpdateMessage(shared.OpModify, *remObj)
			umList = append(umList, &um)
		}
	}
	for _, subpath := range removed {
		localObj, err := m.GetInfo(shared.CreatePath(m.Root, subpath))
		if err != nil {
			m.log("SyncModel: failed to fetch local obj for remove check!")
			continue
		}
		// this works because the deletion files will already have been created, but the removal not applied to the local model yet
		if m.isRemoved(localObj.Identification) {
			// NOTE: we use localObj here because remote object won't exist since we need to remove it locally
			um := shared.CreateUpdateMessage(shared.OpRemove, *localObj)
			umList = append(umList, &um)
		}
	}
	// sort so that dirs are listed before their contents
	return m.sortUpdateMessages(umList), nil
}

/*
BootstrapModel takes a foreign model and bootstraps the current one correctly.
The foreign model will be used to determine all shared files. All other
differences can then be synchronized as before via the update messages return by
this function.
*/
func (m *Model) BootstrapModel(root *shared.ObjectInfo) ([]*shared.UpdateMessage, error) {
	/*TODO for now just warn, should work though... :P */
	if !m.IsEmpty() {
		m.warn("bootstrap: non empty bootstrap! Need to test this yet...")
	}
	m.log("Bootstrapping from remote model.")
	// we'll need the simple lists of the foreign model
	foreignObjs := make(map[string]*shared.ObjectInfo)
	root.ForEach(func(obj shared.ObjectInfo) {
		// strip of children and write to objects
		obj.Objects = nil
		foreignObjs[obj.Path] = &obj
	})
	// list of all updates that will survive the bootstrap and need to be fetched
	var umList []*shared.UpdateMessage
	// take over remote .TINZENITEDIR IDs for own
	for _, remoteObj := range foreignObjs {
		// get path
		remoteSubpath := remoteObj.Path
		// check whether object exists locally (should be case for all .TINZENITEDIR files that we already have locally)
		_, exists := m.TrackedPaths[remoteSubpath]
		if !exists {
			// this means that we must fetch the file, so add to umList
			um := shared.CreateUpdateMessage(shared.OpCreate, *remoteObj)
			umList = append(umList, &um)
			// continue with next object
			continue
		}
		// if it exists overwrite the corresponding stin
		localstin, exists := m.StaticInfos[remoteSubpath]
		if !exists {
			// shouldn't happen but just in case...
			m.log("bootstrap:", "local model tracked and stin not in sync!")
			return nil, shared.ErrIllegalFileState
		}
		// assign other ID always (otherwise cummulative merge won't work)
		localstin.Identification = remoteObj.Identification
		// set to local model
		m.StaticInfos[remoteSubpath] = localstin
		// if content or version not same, add update message as modify
		_, valid := localstin.Version.Valid(remoteObj.Version, m.SelfID)
		if localstin.Content != remoteObj.Content || !valid {
			// this will overwrite the local file! but here we want this behaviour, so all ok
			m.log("bootstrap: force updating", remoteSubpath, ".")
			um := shared.CreateUpdateMessage(shared.OpModify, *remoteObj)
			umList = append(umList, &um)
		}
	}
	// done: we return all updates that we could not manually merge into our own model
	// sort so that dirs are listed before their contents
	return m.sortUpdateMessages(umList), nil
}

/*
HasUpdate checks whether the update has already been applied locally. Used to
avoid getting updates that originated from us.
*/
func (m *Model) HasUpdate(um *shared.UpdateMessage) bool {
	// get local version
	stin, exists := m.StaticInfos[um.Object.Path]
	// depends on operation!
	switch um.Operation {
	case shared.OpRemove:
		// we have the update if the object doesn't exist anymore
		return !exists
	case shared.OpModify:
		// check version to determine whether we have the update
		return stin.Version.Equal(um.Object.Version)
	case shared.OpCreate:
		// if the object already exists we have it
		return exists
	default:
		m.warn("HasUpdate checking unknown operation!")
		return false
	}
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
		m.log("Unknown operation in UpdateMessage:", msg.Operation.String())
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
GetSubPath returns the sub path of whatever object satisfies the identification.
*/
func (m *Model) GetSubPath(identification string) (string, error) {
	for path, stin := range m.StaticInfos {
		if stin.Identification == identification {
			return path, nil
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
GetInfoFrom takes an identification and returns the corresponding shared.ObjectInfo.
*/
func (m *Model) GetInfoFrom(identification string) (*shared.ObjectInfo, error) {
	subpath, err := m.GetSubPath(identification)
	if err != nil {
		return nil, err
	}
	return m.GetInfo(shared.CreatePath(m.Root, subpath))
}

/*
GetInfo creates the Objectinfo for the given path, so long as the path is
contained in m.Tracked. Directories are NOT traversed!
*/
func (m *Model) GetInfo(path *shared.RelativePath) (*shared.ObjectInfo, error) {
	_, exists := m.TrackedPaths[path.SubPath()]
	if !exists {
		m.log("GetInfo: path not tracked!", path.SubPath())
		return nil, shared.ErrUntracked
	}
	// get staticinfo
	stin, exists := m.StaticInfos[path.SubPath()]
	if !exists {
		m.log("GetInfo: stin not tracked!", path.SubPath())
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
Object's slice. If root is a file it simply returns root.
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
IsEmpty returns true if the model is empty SAVE for the .tinzenite files.
*/
func (m *Model) IsEmpty() bool {
	// we could do some cool stuff on m.Tracked etc, but...
	count, err := shared.CountFiles(m.Root)
	if err != nil {
		m.log("IsEmpty:", err.Error())
		return false
	}
	// ...just check whether root contains one dir and it is the TINZENITEDIR
	return count == 1 && shared.FileExists(m.Root+"/"+shared.TINZENITEDIR)
}

/*
ApplyCreate applies a create operation to the local model given that the file
exists. NOTE: In the case of a file, requires the object to exist in the TEMPDIR
named as the object indentification.
*/
func (m *Model) ApplyCreate(path *shared.RelativePath, remoteObject *shared.ObjectInfo) error {
	// ensure parents exists so that create is not "hanging" object
	if !m.parentsExist(path) {
		return errParentObjectsMissing
	}
	// ensure no file has been written already
	localCreate := shared.FileExists(path.FullPath())
	// sanity check if the object already exists locally
	_, ok := m.TrackedPaths[path.SubPath()]
	if ok {
		// m.log("Object exists locally! Can not apply create!", path.FullPath())
		return shared.ErrConflict
	}
	// NOTE: we don't explicitely check m.Objinfo because we'll just overwrite it if already exists
	var stin *staticinfo
	var err error
	// if remote create
	if remoteObject != nil {
		// check if removed --> if yes warn and ignore update
		if m.isRemoved(remoteObject.Identification) {
			m.warn("received create for object pending removal!")
			return nil
		}
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
	m.notify(shared.OpCreate, localObj)
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
		m.log("Object doesn't exist locally!")
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
		/*TODO Check whether modification must even be applied?*/
		// if remote change the local file may not have been modified
		if localModified {
			m.log("Merge error! Untracked local changes!")
			return shared.ErrConflict
		}
		// detect conflict
		ver, ok := stin.Version.Valid(remoteObject.Version, m.SelfID)
		if !ok {
			m.log("Merge error!")
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
			/*TODO can this happen for directories? Only once move is implemented, right?
			Update: it can. Why?*/
			_ = "breakpoint"
			m.warn("modify not implemented for directories!")
		}
	} else {
		if !localModified {
			// nothing to do, done (shouldn't be called but doesn't harm anything)
			m.warn("modify should not be called if nothing actually changed!")
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
	// TODO: DEBUG
	if stin.Directory {
		log.Println("Directory modified!?")
		_ = "breakpoint"
	}
	// apply updated
	m.StaticInfos[path.SubPath()] = stin
	localObj, _ := m.GetInfo(path)
	m.notify(shared.OpModify, localObj)
	return nil
}

/*
ApplyRemove applies a remove operation.
*/
func (m *Model) ApplyRemove(path *shared.RelativePath, remoteObject *shared.ObjectInfo) error {
	remoteRemove := remoteObject != nil
	// check that deletion logic is sane (don't want to create deletion on deletion)
	if strings.HasPrefix(path.SubPath(), shared.TINZENITEDIR+"/"+shared.REMOVEDIR+"/") {
		m.warn("removedir objects should not leak with removal!")
		return nil
	}
	// safe guard against unwanted deletions
	if path.RootPath() != m.Root || path.SubPath() == "" {
		m.warn("trying to remove illegal path, will ignore!", path.FullPath())
		return nil
	}
	// if locally initiated, just apply
	if !remoteRemove {
		// if not a remote remove the deletion must be applied locally
		return m.localRemove(path)
	}
	return m.remoteRemove(path, remoteObject)
}

/*
updateLocal updates the local model for the given scope.
*/
func (m *Model) updateLocal(scope string) error {
	if m.TrackedPaths == nil || m.StaticInfos == nil {
		return shared.ErrNilInternalState
	}
	current, err := m.populateMap()
	if err != nil {
		return err
	}
	// now get differences
	created, modified, removed := m.compareMaps(scope, current)
	// will need this for every Op so create only once
	relPath := shared.CreatePathRoot(m.Root)
	// first check creations
	for _, subpath := range created {
		m.ApplyCreate(relPath.Apply(subpath), nil)
	}
	// then modifications
	for _, subpath := range modified {
		modPath := relPath.Apply(subpath)
		// if no modifications no need to try to apply any
		if m.isModified(modPath) {
			m.ApplyModify(modPath, nil)
		}
	}
	// finally deletions
	for _, subpath := range removed {
		m.ApplyRemove(relPath.Apply(subpath), nil)
	}
	// done
	return nil
}

/*
compareMaps checks the given path map and returns all operations that need to be
applied to the internal model to match the current path map.
*/
func (m *Model) compareMaps(scope string, current map[string]bool) ([]string, []string, []string) {
	// now: compare old tracked with new version
	var created, modified, removed []string
	for subpath := range m.TrackedPaths {
		// ignore if not in partial update path AND not part of path to scope
		if !strings.HasPrefix(m.Root+"/"+subpath, scope) && !strings.Contains(scope, m.Root+"/"+subpath) {
			continue
		}
		_, ok := current[subpath]
		if ok {
			// paths that still exist must only be checked for MODIFY
			delete(current, subpath)
			modified = append(modified, subpath)
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
	return created, modified, removed
}

/*
checkRemove checks whether a remove can be finally applied and purged from the
model dependent on the peers in done and check.

TODO check for orphans and warn? Check for removals that haven't been applied?
NOTE: this method still requires some work!
*/
func (m *Model) checkRemove() error {
	removeDir := m.Root + "/" + shared.TINZENITEDIR + "/" + shared.REMOVEDIR
	allRemovals, err := ioutil.ReadDir(removeDir)
	if err != nil {
		m.log("reading all removals failed")
		return err
	}
	// check for each removal
	for _, stat := range allRemovals {
		// update removal stats
		err := m.writeRemovalDir(stat.Name())
		if err != nil {
			log.Println("DEBUG: updating removal dir failed on checkRemove!", err)
			/*TODO: this fails because the dir is created AFTERWARDS – why and how do I fix this? NOTE: TAMINO TODO*/
			return err
		}
		// working directory
		objRemovePath := removeDir + "/" + stat.Name()
		// read all peers to check for
		allCheck, err := ioutil.ReadDir(objRemovePath + "/" + shared.REMOVECHECKDIR)
		if err != nil {
			m.log("Failed reading check peer list!")
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
				m.log("Failed to direct remove!")
				return err
			}
		}
		// warn of possible orphans
		if time.Since(stat.ModTime()) > removalTimeout {
			m.warn("Removal may be orphaned! ", stat.Name())
			/*TODO this may be called even if it has just been removed... do better logic!
			Also: is there something we can do in this case?*/
		}
	}
	return nil
}

/*
localRemove initiates a deletion locally, creating all necessary files and
removing the file from the model.
*/
func (m *Model) localRemove(path *shared.RelativePath) error {
	// get stin for notify
	stin, exists := m.StaticInfos[path.SubPath()]
	if !exists {
		m.log("LocalRemove: stin is missing!")
		return shared.ErrIllegalFileState
	}
	// sanity check
	if m.isRemoved(stin.Identification) {
		// shouldn't happen but let's be sure
		m.warn("LocalRemove: file already removed!")
		return nil
	}
	// direct remove
	err := m.directRemove(path)
	if err != nil {
		return err
	}
	// write peers
	err = m.writeRemovalDir(stin.Identification)
	if err != nil {
		m.log("failed to update removal dir for", stin.Identification)
		return err
	}
	// update removal dir here so that creations etc are sent before notify below!
	err = m.updateLocal(m.Root + "/" + shared.TINZENITEDIR + "/" + shared.REMOVEDIR + "/" + stin.Identification)
	if err != nil {
		m.warn("partial update on local remove failed!")
		// but continue on because the changes will be synchronized later then anyway
	}
	// send notify
	notifyObj := &shared.ObjectInfo{
		Identification: stin.Identification,
		Name:           path.LastElement(),
		Path:           path.SubPath(),
		Content:        stin.Content,
		Version:        stin.Version,
		Directory:      stin.Directory}
	m.notify(shared.OpRemove, notifyObj)
	return nil
}

/*
remoteRemove handles a remote call of remove.
*/
func (m *Model) remoteRemove(path *shared.RelativePath, remoteObject *shared.ObjectInfo) error {
	// TODO make this better...
	if remoteObject == nil {
		return errors.New("NIL REMOTE OBJECT; NOT ALLOWED")
	}
	localFileExists := shared.FileExists(path.FullPath())
	// if still exists locally remove it
	if localFileExists {
		// remove file (removedir should already exist, so nothing else to do)
		err := m.directRemove(path)
		if err != nil {
			m.log("couldn't remove file", path.FullPath())
			return err
		}
	}
	// sanity check that removedir exists
	if !m.isRemoved(remoteObject.Identification) {
		m.warn("remote file removed but removedir doesn't exist! removing locally.")
		// if not we locally delete it
		return m.localRemove(path)
	}
	// since remote removal --> write peer to done
	err := m.writeRemovalDir(remoteObject.Identification)
	if err != nil {
		m.log("updating removal dir failed!")
		return err
	}
	// if we get a removal from another peer that peer seen the deletion, but we'll be notified by the create method, so nothing to do here
	return nil
}

/*
writeRemovalDir is an internal function that writes all known peers to check
and the own peer to done, if not already existing. NOTE: will not update the
model to avoid recursion: this must be done manually.
*/
func (m *Model) writeRemovalDir(identification string) error {
	removeDirectory := m.Root + "/" + shared.TINZENITEDIR + "/" + shared.REMOVEDIR + "/" + identification
	// make directories if don't exist
	if !shared.FileExists(removeDirectory) {
		err := shared.MakeDirectories(removeDirectory, shared.REMOVECHECKDIR, shared.REMOVEDONEDIR)
		if err != nil {
			m.log("making removedir error")
			return err
		}
	}
	// write peer list to check which must all be notified of removal
	peers, err := m.readPeers()
	if err != nil {
		m.log("Failed to read peers")
		return err
	}
	for _, peer := range peers {
		path := removeDirectory + "/" + shared.REMOVECHECKDIR + "/" + peer
		// if already written don't rewrite
		if shared.FileExists(path) {
			continue
		}
		err = ioutil.WriteFile(path, []byte(""), shared.FILEPERMISSIONMODE)
		if err != nil {
			m.log("Couldn't write peer file", peer, "to check!")
			return err
		}
	}
	path := removeDirectory + "/" + shared.REMOVEDONEDIR + "/" + m.SelfID
	// if already written don't rewrite
	if !shared.FileExists(path) {
		// write own peer file also to done dir as removal already applied locally
		err = ioutil.WriteFile(path, []byte(""), shared.FILEPERMISSIONMODE)
		if err != nil {
			m.log("Couldn't write own peer file to done!", err.Error())
			return err
		}
	}
	// update model accordingly and return
	return m.updateLocal(removeDirectory)
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
		m.log("partialPopulateMap failed in directRemove")
		return err
	}
	// iterate over each path
	for obj := range objList {
		relPath := path.Apply(obj)
		// if it still exists --> remove
		if shared.FileExists(relPath.FullPath()) {
			err := os.RemoveAll(relPath.FullPath())
			if err != nil {
				m.log("directRemove failed to remove the file itself!")
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
		m.log("Staticinfo lookup failed for", path.SubPath(), "!")
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
isRemoved checks whether a file is due for deletion.
*/
func (m *Model) isRemoved(identification string) bool {
	return shared.FileExists(m.Root + "/" + shared.TINZENITEDIR + "/" + shared.REMOVEDIR + "/" + identification)
}

/*
parentsExist takes the path and ensures that each parent object exists in the.
If this is not the case it returns false.
*/
func (m *Model) parentsExist(path *shared.RelativePath) bool {
	for !path.AtRoot() {
		path = path.Up()
		_, exists := m.TrackedPaths[path.SubPath()]
		if !exists {
			return false
		}
	}
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
func (m *Model) notify(op shared.Operation, obj *shared.ObjectInfo) {
	if obj == nil || obj.Path == "" {
		m.warn("notify: called with invalid obj!")
		return
	}
	log.Printf("Notify %s: %s\n", op, obj.Name)
	if m.updatechan != nil {
		if obj == nil {
			m.log("Failed to notify due to nil obj!")
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
			m.log("Failed to walk due to wrong path!", thisPath.FullPath())
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
sortObjects sorts an array of ObjectInfo by the path length. This ensures that
all updates will be sent in the correct order.
*/
func (m *Model) sortUpdateMessages(list []*shared.UpdateMessage) []*shared.UpdateMessage {
	sortable := shared.SortableUpdateMessage(list)
	sort.Sort(sortable)
	return []*shared.UpdateMessage(sortable)
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

/*
Log function that respects the AllowLogging flag.
*/
func (m *Model) log(msg ...string) {
	if m.AllowLogging {
		toPrint := []string{"Model:"}
		toPrint = append(toPrint, msg...)
		log.Println(strings.Join(toPrint, " "))
	}
}

/*
Warn function.
*/
func (m *Model) warn(msg ...string) {
	toPrint := []string{"Model:", "WARNING:"}
	toPrint = append(toPrint, msg...)
	log.Println(strings.Join(toPrint, " "))
}
