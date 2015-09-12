package model

import (
	"io/ioutil"
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
		// shouldn't happen but let's be sure; warn at least
		m.warn("LocalRemove: file removal already begun!")
	}
	// direct remove
	err := m.directRemove(path)
	if err != nil {
		m.log("LocalRemove: failed to directly remove file!")
		return err
	}
	// write peers to check and own peer to done
	err = m.UpdateRemovalDir(stin.Identification, m.SelfID)
	if err != nil {
		m.log("failed to update removal dir for", stin.Identification)
		return err
	}
	// update removal dir here so that creations etc are sent before notify below!
	err = m.updateLocal(m.RootPath + "/" + shared.TINZENITEDIR + "/" + shared.REMOVEDIR + "/" + stin.Identification)
	if err != nil {
		m.warn("partial update on local remove failed!")
		// but continue on because the changes will be synchronized later then anyway
	}
	// update version
	stin.Version.Increase(m.SelfID)
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
	// warn if remove dir didn't exist yet
	if !removalExists {
		// we'll create it anyway if it doesn't exist, so all ok but warn
		m.warn("remote file removed but removedir didn't yet exist!")
	}
	// since remote removal --> write own peer to done
	err := m.UpdateRemovalDir(remoteObject.Identification, m.SelfID)
	if err != nil {
		m.log("updating removal dir failed!")
		return err
	}
	// send notify (reuse remoteObject)
	m.notify(shared.OpRemove, remoteObject)
	return nil
}

/*
checkRemove checks whether a remove can be finally applied and purged from the
model dependent on the peers in done and check.
*/
func (m *Model) checkRemove() error {
	removeDir := m.RootPath + "/" + shared.TINZENITEDIR + "/" + shared.REMOVEDIR
	allRemovals, err := ioutil.ReadDir(removeDir)
	if err != nil {
		m.log("reading all removals failed")
		return err
	}
	// check for each removal
	for _, stat := range allRemovals {
		// update removal stats and write own peer to them
		err = m.UpdateRemovalDir(stat.Name(), m.SelfID)
		if err != nil {
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
		if err == nil && m.IsTracked(m.RootPath+"/"+subPath) {
			m.warn("Removal may be unapplied!", subPath)
		}
	}
	// also remove old local remove notifies:
	localDir := m.RootPath + "/" + shared.TINZENITEDIR + "/" + shared.LOCALDIR + "/" + shared.REMOVESTOREDIR
	allLocals, err := ioutil.ReadDir(localDir)
	if err != nil {
		m.log("reading of local remove notifies failed!")
		return err
	}
	for _, stat := range allLocals {
		if time.Since(stat.ModTime()) > removalLocal {
			// remove notify
			err := os.Remove(localDir + "/" + stat.Name())
			if err != nil {
				m.warn("Failed to remove notify object:", err.Error())
			}
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
	removeDir := m.RootPath + "/" + shared.TINZENITEDIR + "/" + shared.REMOVEDIR
	// working directory
	objRemovePath := removeDir + "/" + identification
	// read all peers to check for
	allCheck, err := ioutil.ReadDir(objRemovePath + "/" + shared.REMOVECHECKDIR)
	if err != nil {
		m.log("Failed reading check peer list!")
		return err
	}
	// Test whether we can remove it. This means all peers must have been written
	// AND modify time has reached timeout. Timeout is required to avoid removing
	// removedirs before every peer has a chance of actually noticing they are complete!
	complete := true
	for _, peerStat := range allCheck {
		checkPath := objRemovePath + "/" + shared.REMOVEDONEDIR + "/" + peerStat.Name()
		exists, err := shared.FileExists(checkPath)
		if err != nil {
			// if any error we are done, so break
			m.log("Failed checking for peer:", err.Error())
			complete = false
			break
		}
		// if a peer doesn't exist yet the removal is NOT yet complete, so break
		if !exists {
			complete = false
			break
		}
	}
	// remove if all peers have written their peer info in REMOVEDONEDIR AND timeout reached (see above)
	if complete {
		// make local note of removal instead of tracked one so that we can remove it
		err := m.makeLocalRemove(identification)
		if err != nil {
			m.log("failed to write local remove note, will not complete removal!")
			return err
		}
		// HARD delete the entire dir: all peers should do the same (soft delete would make removal recursive)
		err = m.directRemove(shared.CreatePathRoot(m.RootPath).Apply(objRemovePath))
		if err != nil {
			m.log("Failed to direct remove!")
			return err
		}
		// note that other peers may not HARD delete it yet, but the isLocalRemoved check ensures that the dir isn't reintroduced
	}
	return nil
}

/*
UpdateRemovalDir is an internal function that writes all known peers to check.
Also, if given, it will add the given peer to the REMOVEDONEDIR.
*/
func (m *Model) UpdateRemovalDir(objIdentification, peerIdentification string) error {
	removeDirectory := m.RootPath + "/" + shared.TINZENITEDIR + "/" + shared.REMOVEDIR + "/" + objIdentification
	// make directories if don't exist
	if exists, _ := shared.DirectoryExists(removeDirectory); !exists {
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
		if exists, _ := shared.FileExists(path); exists {
			continue
		}
		err = ioutil.WriteFile(path, []byte(""), shared.FILEPERMISSIONMODE)
		if err != nil {
			m.log("Couldn't write peer file", peer, "to", shared.REMOVECHECKDIR, "!")
			return err
		}
	}
	// if peerIdentification isn't empty, write that peer to DONE
	if peerIdentification != "" {
		path := removeDirectory + "/" + shared.REMOVEDONEDIR + "/" + peerIdentification
		// if already written don't rewrite
		if exists, _ := shared.FileExists(path); !exists {
			// write own peer file also to done dir as removal already applied locally
			err = ioutil.WriteFile(path, []byte(""), shared.FILEPERMISSIONMODE)
			if err != nil {
				m.log("Couldn't write peer file to", shared.REMOVEDONEDIR, "!", err.Error())
				return err
			}
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
		if exists, _ := shared.ObjectExists(relPath.FullPath()); exists {
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
	path := m.RootPath + "/" + shared.TINZENITEDIR + "/" + shared.REMOVEDIR + "/" + identification
	exists, _ := shared.FileExists(path)
	return exists || m.isLocalRemoved(identification)
}

/*
makeLocalRemove is used to locally remember which removals have been applied
already, meaning the shared tracking of a file deletion has been removed.
*/
func (m *Model) makeLocalRemove(identification string) error {
	path := m.RootPath + "/" + shared.TINZENITEDIR + "/" + shared.LOCALDIR + "/" + shared.REMOVESTOREDIR + "/" + identification
	return ioutil.WriteFile(path, []byte(""), shared.FILEPERMISSIONMODE)
}

/*
isLocalRemoved notes whether a deletion may be being reintroduced even though it
was completely accepted.
*/
func (m *Model) isLocalRemoved(identification string) bool {
	path := m.RootPath + "/" + shared.TINZENITEDIR + "/" + shared.LOCALDIR + "/" + shared.REMOVESTOREDIR + "/" + identification
	exists, _ := shared.FileExists(path)
	return exists
}
