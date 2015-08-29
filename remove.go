package model

import (
	"io/ioutil"
	"log"
	"os"
	"time"

	"github.com/tinzenite/shared"
)

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

TODO this is buggy, fix it.
*/
func (m *Model) remoteRemove(path *shared.RelativePath, remoteObject *shared.ObjectInfo) error {
	log.Println("DEBUG: remote remove!")
	// sanity check
	if remoteObject == nil {
		return shared.ErrIllegalParameters
	}
	// get state information
	localFileExists := m.IsTracked(path.FullPath())
	removalExists := m.isRemoved(remoteObject.Identification)
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
	if !removalExists {
		m.warn("remote file removed but removedir doesn't exist! removing locally.")
		// if not we locally delete it
		// TODO is this correct? after all it doesn't exist --> JUST create removal dir?
		return m.localRemove(path)
	}
	// since remote removal --> write peer to done
	err := m.writeRemovalDir(remoteObject.Identification)
	if err != nil {
		m.log("updating removal dir failed!")
		return err
	}
	// if we get a removal from another peer that peer has seen the deletion, but
	// we'll be notified by the create method, so nothing to do here
	// TODO: why don't we notify again? Is the above correct?
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
		m.log("reading all removals failed")
		return err
	}
	// check for each removal
	for _, stat := range allRemovals {
		// update removal stats (including writing own peer into DONE for all of them)
		err = m.writeRemovalDir(stat.Name())
		if err != nil {
			log.Println("DEBUG: updating removal dir failed on checkRemove!", err)
			return err
		}
		// check if we can complete the removal
		err := m.completeTrackedRemoval(stat.Name())
		if err != nil {
			// notify of error but don't stop, rest can still be checked
			m.log("completeTrackedRemoval:", err.Error())
		}
		// warn of possible orphans
		if time.Since(stat.ModTime()) > removalTimeout {
			m.warn("Removal may be orphaned! ", stat.Name())
			/*TODO this may be called even if it has just been removed... do better logic!
			Also: is there something we can do in this case?*/
		}
		// warn of possibly unapplied removals:
		subPath, err := m.GetSubPath(stat.Name())
		// if err just skip the check (can happen if the file has been removed, so ok)
		if err == nil && m.IsTracked(m.Root+"/"+subPath) {
			m.warn("Removal may be unapplied!")
		}
	}
	// also remove old local remove notifies:
	localDir := m.Root + "/" + shared.TINZENITEDIR + "/" + shared.LOCALDIR + "/" + shared.REMOVESTOREDIR
	allLocals, err := ioutil.ReadDir(localDir)
	if err != nil {
		m.log("reading of local remove notifies failed!")
		return err
	}
	for _, stat := range allLocals {
		if time.Since(stat.ModTime()) > removalLocal {
			log.Println("DEBUG: local remove of notify can be done!")
			// TODO actually remove them here...
		}
	}
	return nil
}

/*
completeTrackedRemoval checks and if ok, removes the tracked removal dir, replacing
it with a local notify of the removal. This allows the tracked removal to be
purged. After a time out the local notify is also removed.
*/
func (m *Model) completeTrackedRemoval(identification string) error {
	removeDir := m.Root + "/" + shared.TINZENITEDIR + "/" + shared.REMOVEDIR
	// working directory
	objRemovePath := removeDir + "/" + identification
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
		// make local note of removal instead of tracked one so that we can remove it
		err := m.makeLocalRemove(identification)
		if err != nil {
			m.log("failed to write local remove note, will not complete removal!")
			return err
		}
		// HARD delete the entire dir: all peers should do the same (soft delete would make removal recursive)
		err = m.directRemove(shared.CreatePathRoot(m.Root).Apply(objRemovePath))
		if err != nil {
			m.log("Failed to direct remove!")
			return err
		}
		// note that other peers may not HARD delete it yet, but the isLocalRemoved check ensures that the dir isn't reintroduced
	}
	return nil
}

/*
writeRemovalDir is an internal function that writes all known peers to check
and the own peer to done, if not already existing.
*/
func (m *Model) writeRemovalDir(objIdentification string) error {
	removeDirectory := m.Root + "/" + shared.TINZENITEDIR + "/" + shared.REMOVEDIR + "/" + objIdentification
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
		m.log("DEBUG: Peer", peer, "is being written to", shared.REMOVECHECKDIR, ".")
		err = ioutil.WriteFile(path, []byte(""), shared.FILEPERMISSIONMODE)
		if err != nil {
			m.log("Couldn't write peer file", peer, "to", shared.REMOVECHECKDIR, "!")
			return err
		}
	}
	// write own peer into DONE
	path := removeDirectory + "/" + shared.REMOVEDONEDIR + "/" + m.SelfID
	// if already written don't rewrite
	if !shared.FileExists(path) {
		m.log("DEBUG: Own peer is being written to", shared.REMOVEDONEDIR, ".")
		// write own peer file also to done dir as removal already applied locally
		err = ioutil.WriteFile(path, []byte(""), shared.FILEPERMISSIONMODE)
		if err != nil {
			m.log("Couldn't write own peer file to", shared.REMOVEDONEDIR, "!", err.Error())
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
isRemoved checks whether a file is due for deletion or whether it has already
been locally removed completely.
*/
func (m *Model) isRemoved(identification string) bool {
	path := m.Root + "/" + shared.TINZENITEDIR + "/" + shared.REMOVEDIR + "/" + identification
	return shared.FileExists(path) || m.isLocalRemoved(identification)
}

/*
makeLocalRemove is used to locally remember which removals have been applied
already, meaning the shared tracking of a file deletion has been removed.
*/
func (m *Model) makeLocalRemove(identification string) error {
	path := m.Root + "/" + shared.TINZENITEDIR + "/" + shared.LOCALDIR + "/" + shared.REMOVESTOREDIR + "/" + identification
	return ioutil.WriteFile(path, []byte(""), shared.FILEPERMISSIONMODE)
}

/*
isLocalRemoved notes whether a deletion may be being reintroduced even though it
was completely accepted.
*/
func (m *Model) isLocalRemoved(identification string) bool {
	path := m.Root + "/" + shared.TINZENITEDIR + "/" + shared.LOCALDIR + "/" + shared.REMOVESTOREDIR + "/" + identification
	return shared.FileExists(path)
}
