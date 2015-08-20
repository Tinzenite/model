package model

import (
	"encoding/json"
	"os"
	"time"

	"github.com/tinzenite/shared"
)

/*
staticinfo stores all information that Tinzenite must keep between calls to
m.Update(). This includes the object ID and version for reapplication, plus
the content hash if required for file content changes detection.
*/
type staticinfo struct {
	Identification string
	Directory      bool
	Content        string
	Modtime        time.Time
	Version        shared.Version
}

/*
createStaticInfo for the given file at the path with all values filled
accordingly.
*/
func createStaticInfo(path, selfpeerid string) (*staticinfo, error) {
	// fetch all values we'll need to store
	id, err := shared.NewIdentifier()
	if err != nil {
		return nil, err
	}
	stat, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	hash := ""
	if !stat.IsDir() {
		hash, err = shared.ContentHash(path)
		if err != nil {
			return nil, err
		}
	}
	return &staticinfo{
		Identification: id,
		Version:        shared.CreateVersion(),
		Directory:      stat.IsDir(),
		Content:        hash,
		Modtime:        stat.ModTime()}, nil
}

/*
UpdateFromDisk updates the hash and modtime to match the file on disk.
*/
func (s *staticinfo) updateFromDisk(path string) error {
	if !s.Directory {
		hash, err := shared.ContentHash(path)
		if err != nil {
			return err
		}
		s.Content = hash
	}
	stat, err := os.Lstat(path)
	if err != nil {
		return err
	}
	s.Modtime = stat.ModTime()
	return nil
}

/*
ApplyObjectInfo to staticinfo object.
*/
func (s *staticinfo) applyObjectInfo(obj *shared.ObjectInfo) {
	s.Identification = obj.Identification
	s.Version = obj.Version
	s.Directory = obj.Directory
	s.Content = obj.Content
}

func (s *staticinfo) String() string {
	data, _ := json.Marshal(s)
	return string(data)
}
