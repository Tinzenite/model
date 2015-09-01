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
	/*TODO make AllowLogging setable somewhere, for now always on*/
	AllowLogging bool
	updatechan   chan shared.UpdateMessage
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
Sync takes the root ObjectInfo of the foreign model and returns an amount of
UpdateMessages required to update the current model to the foreign model. These
must still be applied!
*/
func (m *Model) Sync(root *shared.ObjectInfo) ([]*shared.UpdateMessage, error) {
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
	// for all created paths...
	for _, subpath := range created {
		remObj, exists := foreignObjs[subpath]
		if !exists {
			m.warn("Created path", subpath, "doesn't exist in remote model!")
			continue
		}
		log.Println("DEBUG: creating", subpath)
		um := shared.CreateUpdateMessage(shared.OpCreate, *remObj)
		umList = append(umList, &um)
	}
	// for all modified paths...
	for _, subpath := range modified {
		localObj, err := m.GetInfo(shared.CreatePath(m.Root, subpath))
		if err != nil {
			m.log("SyncModel: failed to fetch local obj for modify check!")
			continue
		}
		remObj, exists := foreignObjs[subpath]
		if !exists {
			m.warn("SyncModel: Modified path", subpath, "doesn't exist in remote model!")
			continue
		}
		// check if same version – if not some modify has happened
		if !localObj.Version.Equal(remObj.Version) {
			if localObj.Directory {
				// shouldn't happen but catch to be sure
				m.warn("SyncModel: Found modified directory?!")
				// ignore!
				continue
			}
			log.Println("DEBUG: modifing", subpath)
			um := shared.CreateUpdateMessage(shared.OpModify, *remObj)
			umList = append(umList, &um)
		}
	}
	// for all removed paths...
	for _, subpath := range removed {
		localObj, err := m.GetInfo(shared.CreatePath(m.Root, subpath))
		if err != nil {
			m.log("SyncModel: failed to fetch local obj for remove check!")
			continue
		}
		// this works because the deletion files will already have been created, but the removal not applied to the local model yet
		if m.isRemoved(localObj.Identification) {
			// NOTE: we use localObj here because remote object won't exist since we need to remove it locally
			log.Println("DEBUG: removing", subpath)
			um := shared.CreateUpdateMessage(shared.OpRemove, *localObj)
			umList = append(umList, &um)
		}
		// NONE of the other paths are truly removed: the foreign model just doesn't know of them, so done
	}
	// sort so that dirs are listed before their contents
	return sortUpdateMessages(umList), nil
}

/*
Bootstrap takes a foreign model and bootstraps the current one correctly.
The foreign model will be used to determine all shared files. All other
differences can then be synchronized as before via the update messages return by
this function.
*/
func (m *Model) Bootstrap(root *shared.ObjectInfo) ([]*shared.UpdateMessage, error) {
	/*TODO for now just warn, should work though... :P */
	if !m.IsEmpty() {
		m.warn("bootstrap: non empty bootstrap!")
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
			// this means that we must fetch the file, so add to umList as CREATE
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
		// assign version
		localstin.Version = remoteObj.Version
		// set to local model
		m.StaticInfos[remoteSubpath] = localstin
		// if content not same, add update message as modify to bring both version to same content
		if localstin.Content != remoteObj.Content {
			// this will overwrite the local file! but here we want this behaviour, so all ok
			m.log("bootstrap: force updating <" + remoteSubpath + ">.")
			um := shared.CreateUpdateMessage(shared.OpModify, *remoteObj)
			umList = append(umList, &um)
		}
	}
	// done: we return all updates that we could not manually merge into our own model
	// sort so that dirs are listed before their contents
	return sortUpdateMessages(umList), nil
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
	var err error
	// TODO maybe filter externally because we may need to send messages back...
	err = m.filterMessage(msg)
	if err != nil {
		m.warn("Filter failed message!", err.Error())
		return err
	}
	path := shared.CreatePath(m.Root, msg.Object.Path)
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
	// basically if model has any files apart from those in the .tinzenite dir, it is not empty
	for subpath := range m.TrackedPaths {
		// root path is ignored
		if subpath == "" {
			continue
		}
		// .tinzenite and all subpaths are ignored
		if strings.HasPrefix(subpath, shared.TINZENITEDIR) {
			continue
		}
		// otherwise we know it is not empty
		// m.warn("Not empty because of", subpath)
		return false
	}
	// if we reach this --> empty
	return true
}

/*
IsTracked returns true if the given path is tracked by this model.
*/
func (m *Model) IsTracked(path string) bool {
	relPath := shared.CreatePathRoot(m.Root).Apply(path)
	_, pathExists := m.TrackedPaths[relPath.SubPath()]
	_, stinExists := m.StaticInfos[relPath.SubPath()]
	return pathExists && stinExists
}

/*
filterMessage checks a message for special cases. Will return an error if
something is not correct. Intended to be called for all external messages.
NOTE: is not called on direct calls of Apply*()!
*/
func (m *Model) filterMessage(um *shared.UpdateMessage) error {
	// check if removed --> if yes warn and ignore update (except if a remove operation)
	if m.isRemoved(um.Object.Identification) && um.Operation != shared.OpRemove {
		// TODO resend removal! NOTE: implement later once removal works correctly
		log.Println("DEBUG: TODO: resend removal!")
		return errObjectRemoved
	}
	// check if part of REMOVEDIR
	removePath := shared.TINZENITEDIR + "/" + shared.REMOVEDIR
	if strings.HasSuffix(removePath, um.Object.Path) {
		// if not a create operation, something is wrong
		if um.Operation != shared.OpCreate {
			// this also catches removals WITHIN the REMOVEDIR which shouldn't happen
			m.warn("Filter ran into disallowed operation!", um.Operation.String())
			return errFilter
		}
		// if the object has already been locally notified, the dir doesn't exist anymore
		if m.isLocalRemoved(um.Object.Identification) {
			log.Println("DEBUG: is locally removed already!")
			// TODO resend creation event for own peer and ignore?
			return errFilter
		}
		// otherwise ok, continue with other checks
	}
	// ensure parents exists so that operation is not on "hanging" object
	if !m.parentsExist(shared.CreatePath(m.Root, um.Object.Path)) {
		m.warn("Filter ran into hanging object!")
		return errParentObjectsMissing
	}
	// if not create, object must be tracked
	if um.Operation != shared.OpCreate {
		if !m.IsTracked(um.Object.Path) {
			m.warn("Filter found untracked object!")
			return errObjectUntracked
		}
	}
	// check for empty version on modify
	if um.Operation == shared.OpModify && um.Object.Version.IsEmpty() {
		m.warn("Filter found empty version on modify!")
		return errFilter
	}
	return nil
}

/*
ApplyCreate applies a create operation to the local model given that the file
exists. NOTE: In the case of a file, requires the object to exist in the TEMPDIR
named as the object indentification.
*/
func (m *Model) ApplyCreate(path *shared.RelativePath, remoteObject *shared.ObjectInfo) error {
	// NOTE that ApplyCreate does NOT call filterMessage itself!
	// ensure no file has been written already
	localExists := shared.FileExists(path.FullPath())
	// sanity check if the object already exists locally
	if m.IsTracked(path.FullPath()) {
		if localExists {
			// if tracked and file exists --> merge
			return shared.ErrConflict
		}
		// if tracked but file doesn't exist --> error
		m.warn("created object is already tracked but file doesn't exist!")
		return shared.ErrIllegalFileState
	}
	// we don't explicitely check m.Objinfo because we'll just overwrite it if already exists
	var stin *staticinfo
	var err error
	// if remote create
	if remoteObject != nil {
		// create conflict if locally exists
		if localExists {
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
		// local create
		if !localExists {
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
	localObj, err := m.GetInfo(path)
	if err != nil {
		m.warn("failed to retrieve created ObjectInfo for notify!")
	} else {
		m.notify(shared.OpCreate, localObj)
	}
	return nil
}

/*
ApplyModify checks for modifications and if valid applies them to the local model.
Conflicts will result in deletion of the old file and two creations of both versions
of the conflict. NOTE: In the case of a file, requires the object to exist in the
TEMPDIR named as the object indentification.
*/
func (m *Model) ApplyModify(path *shared.RelativePath, remoteObject *shared.ObjectInfo) error {
	// NOTE that ApplyModify does NOT call filterMessage itself!
	// TODO remove me once this bug is fixed NOTE FIXME
	// NOTE it IS the external message that is the problem. So how do I find out where it is sent?
	if remoteObject != nil && remoteObject.Version.IsEmpty() {
		log.Println("DEBUG: Yup, ignoring empty version!", remoteObject.Path)
		// quietly ignoring it for now...
		return nil
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
		// check for merge error
		if !stin.Version.Valid(remoteObject.Version, m.SelfID) {
			m.log("Merge error!")
			return shared.ErrConflict
		}
		// apply version update
		stin.Version = remoteObject.Version
		// if file apply file diff
		if !remoteObject.Directory {
			// apply the file op
			err := m.applyFile(stin.Identification, path.FullPath())
			if err != nil {
				return err
			}
		} else {
			/*TODO can this happen for directories? Only once move is implemented, right?*/
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
		log.Println("DEBUG: shouldn't happen: Directory modified!?")
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
	// NOTE that ApplyCreate does NOT call filterMessage itself!
	remoteRemove := remoteObject != nil
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
		err := m.ApplyCreate(relPath.Apply(subpath), nil)
		if err != nil {
			m.log("updateLocal: create error for", subpath)
			return err
		}
	}
	// then modifications
	for _, subpath := range modified {
		modPath := relPath.Apply(subpath)
		// if no modifications no need to try to apply any
		if m.isModified(modPath) {
			err := m.ApplyModify(modPath, nil)
			if err != nil {
				m.log("updateLocal: modify error for", subpath)
				return err
			}
		}
	}
	// finally deletions
	for _, subpath := range removed {
		err := m.ApplyRemove(relPath.Apply(subpath), nil)
		if err != nil {
			m.log("updateLocal: remove error for", subpath)
			return err
		}
	}
	// done
	return nil
}

/*
compareMaps checks the given path map and returns all operations that need to be
applied to the internal model to match the current path map. NOTE: the modified
list must still be checked if they actually WERE modified!
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
	// it is important to return these sorted: create dirs before their contents for example
	return sortPaths(created), sortPaths(modified), sortPaths(removed)
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
parentsExist takes the path and ensures that each parent object exists in the.
If this is not the case it returns false.
*/
func (m *Model) parentsExist(path *shared.RelativePath) bool {
	for !path.AtRoot() {
		path = path.Up()
		if !m.IsTracked(path.FullPath()) {
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
	if obj == nil {
		m.warn("notify: called with invalid obj!")
		return
	}
	// TODO this catches a bug which shouldn't even be turning up, FIXME
	if obj.Version.IsEmpty() && op == shared.OpModify {
		m.warn("notify: object for " + obj.Path + " has empty version on " + op.String() + " operation!")
		return
	}
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
