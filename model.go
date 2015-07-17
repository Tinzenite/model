package model

import (
	"encoding/json"
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
	Root       string
	SelfID     string
	Tracked    map[string]bool
	Objinfo    map[string]staticinfo
	updatechan chan shared.UpdateMessage
}

/*
CreateModel creates a new model at the specified path for the given peer id. Will
not immediately update, must be explicitely called.
*/
func CreateModel(root, peerid string) (*Model, error) {
	if !IsTinzenite(root) {
		return nil, shared.ErrNotTinzenite
	}
	m := &Model{
		Root:    root,
		Tracked: make(map[string]bool),
		Objinfo: make(map[string]staticinfo),
		SelfID:  peerid}
	return m, nil
}

/*
LoadModel loads or creates a model for the given path, depending whether a
model.json exists for it already.
*/
func LoadModel(root string) (*Model, error) {
	if !IsTinzenite(root) {
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
PartialUpdate of the model state.

TODO Get concurrency to work here. Last time I had trouble with the Objinfo map.
*/
func (m *Model) PartialUpdate(scope string) error {
	if m.Tracked == nil || m.Objinfo == nil {
		return ErrNilInternalState
	}
	current, err := m.populateMap()
	var removed, created []string
	if err != nil {
		return err
	}
	// we'll need this for every create* op, so create only once:
	relPath := createPathRoot(m.Root)
	// now: compare old tracked with new version
	for path := range m.Tracked {
		// ignore if not in partial update path
		if !strings.HasPrefix(path, scope) {
			continue
		}
		_, ok := current[path]
		if ok {
			// paths that still exist must only be checked for MODIFY
			delete(current, path)
			m.applyModify(relPath.Apply(path), nil)
		} else {
			// REMOVED - paths that don't exist anymore have been removed
			removed = append(removed, path)
		}
	}
	// CREATED - any remaining paths are yet untracked in m.tracked
	for path := range current {
		// ignore if not in partial update path
		if !strings.HasPrefix(path, scope) {
			continue
		}
		created = append(created, path)
	}
	// update m.Tracked
	for _, path := range removed {
		m.applyRemove(relPath.Apply(path), nil)
	}
	for _, path := range created {
		// nil for version because new local object
		m.applyCreate(relPath.Apply(path), nil)
	}
	// finally also store the model for future loads.
	return m.Store()
}

/*
SyncModel TODO
*/
func (m *Model) SyncModel(root *ObjectInfo) ([]*UpdateMessage, error) {
	/*
		TODO: how to implement this.
		Maybe: make a check method that simply returns whether Tinzenite needs to
		fetch the file? Can then use ApplyUpdateMessage to trigger actual update...

		Will also need to work on how TINZENITE fetches the files (from multiple etc.)
	*/
	return nil, ErrUnsupported
}

/*
SyncObject returns an UpdateMessage of the change we may need to apply if
applicable. May return nil, that means that the update must not be applied (for
example if the object has not changed).
*/
func (m *Model) SyncObject(obj *ObjectInfo) (*UpdateMessage, error) {
	// we'll need the local path so create that up front
	path := createPath(m.Root, obj.Path)
	// modfiy
	_, exists := m.Tracked[path.FullPath()]
	if exists {
		// get staticinfo
		stin, ok := m.Objinfo[path.FullPath()]
		if !ok {
			return nil, errModelInconsitent
		}
		// sanity checks
		if stin.Identification != obj.Identification || stin.Directory != obj.directory {
			return nil, errMismatch
		}
		/*TODO what about directories?*/
		if stin.Content == obj.Content {
			/*TODO what about the version numbers?*/
			log.Println("No update required!")
			return nil, nil
		}
		return &UpdateMessage{
			Type:      MsgUpdate,
			Operation: OpModify,
			Object:    *obj}, nil
	}
	log.Println("Create and delete not yet implemented!")
	return nil, ErrUnsupported
}

/*
ApplyUpdateMessage takes an update message and applies it to the model. Should
be called after the file operation has been applied but before the next update!
*/
/*TODO catch shadow files*/
func (m *Model) ApplyUpdateMessage(msg *UpdateMessage) error {
	path := createPath(m.Root, msg.Object.Path)
	var err error
	switch msg.Operation {
	case OpCreate:
		err = m.applyCreate(path, &msg.Object)
	case OpModify:
		err = m.applyModify(path, &msg.Object)
	case OpRemove:
		err = m.applyRemove(path, &msg.Object)
	default:
		log.Printf("Unknown operation in UpdateMessage: %s\n", msg.Operation)
		return ErrUnsupported
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
func (m *Model) Register(v chan UpdateMessage) {
	m.updatechan = v
}

/*
Read builds the complete Objectinfo representation of this model to its full
depth. Incredibly fast because we only link objects based on the current state
of the model: hashes etc are not recalculated.
*/
func (m *Model) Read() (*ObjectInfo, error) {
	var allObjs sortable
	rpath := createPathRoot(m.Root)
	// getting all Objectinfos is very fast because the staticinfo already exists for all of them
	for fullpath := range m.Tracked {
		obj, err := m.getInfo(rpath.Apply(fullpath))
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
	m.fillInfo(root, allObjs)
	return root, nil
}

/*
Store the model to disk in the correct directory.
*/
func (m *Model) Store() error {
	path := m.Root + "/" + TINZENITEDIR + "/" + LOCALDIR + "/" + MODELJSON
	jsonBinary, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return ioutil.WriteFile(path, jsonBinary, FILEPERMISSIONMODE)
}

/*
GetInfo creates the Objectinfo for the given path, so long as the path is
contained in m.Tracked. Directories are NOT traversed!
*/
func (m *Model) GetInfo(path *relativePath) (*ObjectInfo, error) {
	_, exists := m.Tracked[path.FullPath()]
	if !exists {
		log.Printf("Error: %s\n", path.FullPath())
		return nil, ErrUntracked
	}
	// get staticinfo
	stin, exists := m.Objinfo[path.FullPath()]
	if !exists {
		log.Printf("Error: %s\n", path.FullPath())
		return nil, ErrUntracked
	}
	stat, err := os.Lstat(path.FullPath())
	if err != nil {
		return nil, err
	}
	// build object
	object := &ObjectInfo{
		Identification: stin.Identification,
		Name:           path.LastElement(),
		Path:           path.Subpath(),
		Shadow:         false,
		Version:        stin.Version}
	if stat.IsDir() {
		object.directory = true
		object.Content = ""
	} else {
		object.directory = false
		object.Content = stin.Content
	}
	return object, nil
}

/*
FillInfo takes an Objectinfo and a list of candidates and recursively fills its
Objects slice. If root is a file it simply returns root.
*/
func (m *Model) FillInfo(root *ObjectInfo, all []*ObjectInfo) *ObjectInfo {
	if !root.directory {
		// this may be an error, check later
		return root
	}
	rpath := createPath(m.Root, root.Path)
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
			log.Println("Not in mine!") // leave this line until you figure out why it never runs into this...
			continue
		}
		// if reached the object is in our subdir, so add and recursively fill
		root.Objects = append(root.Objects, m.fillInfo(obj, all))
	}
	return root
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
func (m *Model) partialPopulateMap(path string) (map[string]bool, error) {
	relPath := createPathRoot(m.Root).Apply(path)
	master, err := createMatcher(relPath.Rootpath())
	if err != nil {
		return nil, err
	}
	tracked := make(map[string]bool)
	filepath.Walk(relPath.FullPath(), func(subpath string, stat os.FileInfo, inerr error) error {
		// resolve matcher
		/*FIXME thie needlessly creates a lot of potential duplicates*/
		match := master.Resolve(relPath.Apply(subpath))
		// ignore on match
		if match.Ignore(subpath) {
			// SkipDir is okay even if file
			if stat.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		tracked[subpath] = true
		return nil
	})
	// doesn't directly assign to m.tracked on purpose so that we can reuse this
	// method elsewhere (for the current structure on m.Update())
	return tracked, nil
}

/*
applyCreate applies a create operation to the local model given that the file
exists. NOTE: In the case of a file, requires the object to exist in the TEMPDIR
named as the object indentification.
*/
func (m *Model) applyCreate(path *relativePath, remoteObject *ObjectInfo) error {
	// ensure no file has been written already
	localCreate := fileExists(path.FullPath())
	// sanity check if the object already exists locally
	_, ok := m.Tracked[path.FullPath()]
	if ok {
		log.Printf("Object at <%s> exists locally! Can not apply create!\n", path.FullPath())
		return errConflict
	}
	// NOTE: we don't explicitely check m.Objinfo because we'll just overwrite it if already exists
	var stin *staticinfo
	var err error
	// if remote create
	if remoteObject != nil {
		// create conflict
		if localCreate {
			return errConflict
		}
		// dirs are made directly, files have to be moved from temp
		if remoteObject.directory {
			err := makeDirectory(path.FullPath())
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
		stin.ApplyObjectInfo(remoteObject)
	} else {
		if !localCreate {
			return errIllegalFileState
		}
		// build staticinfo
		stin, err = createStaticInfo(path.FullPath(), m.SelfID)
		if err != nil {
			return err
		}
	}
	// add obj to local model
	m.Tracked[path.FullPath()] = true
	m.Objinfo[path.FullPath()] = *stin
	localObj, _ := m.getInfo(path)
	m.notify(OpCreate, path, localObj)
	return nil
}

/*
applyModify checks for modifications and if valid applies them to the local model.
Conflicts will result in deletion of the old file and two creations of both versions
of the conflict. NOTE: In the case of a file, requires the object to exist in the
TEMPDIR named as the object indentification.
*/
func (m *Model) applyModify(path *relativePath, remoteObject *ObjectInfo) error {
	// ensure file has been written
	if !fileExists(path.FullPath()) {
		return errIllegalFileState
	}
	// sanity check
	_, ok := m.Tracked[path.FullPath()]
	if !ok {
		log.Println("Object doesn't exist locally!")
		return errIllegalFileState
	}
	// fetch stin
	stin, ok := m.Objinfo[path.FullPath()]
	if !ok {
		return errModelInconsitent
	}
	// flag whether the local file has been modified
	localModified := m.isModified(path.FullPath())
	// check for remote modifications
	if remoteObject != nil {
		/*TODO implement conflict behaviour!*/
		// if remote change the local file may not have been modified
		if localModified {
			log.Println("Merge error! Untracked local changes!")
			return errConflict
		}
		// detect conflict
		ver, ok := stin.Version.Valid(remoteObject.Version, m.SelfID)
		if !ok {
			log.Println("Merge error!")
			/*TODO implement merge behavior in main.go*/
			return errConflict
		}
		// apply version update
		stin.Version = ver
		// if file apply file diff
		if !remoteObject.directory {
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
	err := stin.UpdateFromDisk(path.FullPath())
	if err != nil {
		return err
	}
	// apply updated
	m.Objinfo[path.FullPath()] = stin
	localObj, _ := m.getInfo(path)
	m.notify(OpModify, path, localObj)
	return nil
}

/*
applyRemove applies a remove operation.
*/
func (m *Model) applyRemove(path *relativePath, remoteObject *ObjectInfo) error {
	// check if local file has been removed
	localRemove := !fileExists(path.FullPath())
	var notifyObj *ObjectInfo
	// remote removal
	if remoteObject != nil {
		removeExists := fileExists(m.Root + "/" + TINZENITEDIR + "/" + REMOVEDIR + "/" + remoteObject.Identification)
		if removeExists {
			log.Println("Creation of remove object overtook deletion: apply deletion and modify remove object.")
		}
		notifyObj = remoteObject
	} else {
		if !localRemove {
			/*TODO must check newly tinignore added files that remain on disk! --> not an error!*/
			log.Println("Remove failed: file still exists!")
			return errIllegalFileState
		}
		// build a somewhat adequate object to send (important is only the ID anyway)
		stin := m.Objinfo[path.FullPath()]
		// just fill it with the info we have at hand
		notifyObj = &ObjectInfo{
			Identification: stin.Identification,
			Name:           path.LastElement(),
			Content:        stin.Content,
			Version:        stin.Version,
			directory:      stin.Directory}
	}
	/*TODO multiple peer logic*/
	delete(m.Tracked, path.FullPath())
	delete(m.Objinfo, path.FullPath())
	m.notify(OpRemove, path, notifyObj)
	return nil
}

/*
isModified checks whether a file has been modified.
*/
func (m *Model) isModified(path string) bool {
	stin, ok := m.Objinfo[path]
	if !ok {
		log.Println("staticinfo lookup failed!")
		return false
	}
	// no need for further work here
	if stin.Directory {
		return false
	}
	// if modtime still the same no need to hash again
	stat, err := os.Lstat(path)
	if err != nil {
		log.Println(err.Error())
		// Note that we don't return here because we can still continue without this check
	} else {
		if stat.ModTime() == stin.Modtime {
			return false
		}
	}
	hash, err := contentHash(path)
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
	temppath := m.Root + "/" + TINZENITEDIR + "/" + TEMPDIR + "/" + identification
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
func (m *Model) notify(op Operation, path *relativePath, obj *shared.ObjectInfo) {
	log.Printf("%s: %s\n", op, path.LastElement())
	if m.updatechan != nil {
		if obj == nil {
			log.Println("Failed to notify due to nil obj!")
			return
		}
		m.updatechan <- shared.CreateUpdateMessage(op, *obj)
	}
}
