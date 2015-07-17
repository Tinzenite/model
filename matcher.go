package model

import (
	"io/ioutil"
	"os"
	"strings"
)

/*
Matcher is a helper object that checks paths against a .tinignore file.
*/
type matcher struct {
	root      string
	dirRules  []string
	fileRules []string
	used      bool
}

/*
CreateMatcher creates a new matching object for fast checks against a .tinignore
file. The root path is the directory where the .tinignore file is expected to lie
in.
*/
func createMatcher(rootPath string) (*matcher, error) {
	var match matcher
	match.root = rootPath
	allRules, err := readTinIgnore(rootPath)
	if err == ErrNoTinIgnore {
		// if empty we're done
		return &match, nil
	} else if err != nil {
		// return other errors however
		return nil, err
	}
	for _, line := range allRules {
		// is the line a rule for a directory?
		if strings.HasPrefix(line, "/") {
			match.dirRules = append(match.dirRules, line)
		} else {
			match.fileRules = append(match.fileRules, line)
		}
	}
	// possibly empty .tinignore so catch
	if len(match.dirRules) != 0 || len(match.fileRules) != 0 {
		// if we have values set it
		match.used = true
	}
	return &match, nil
}

/*
Ignore checks whether the given path is to be ignored given the rules within the
root .tinignore file.
*/
func (m *matcher) Ignore(path string) bool {
	// no need to check anything in this case
	if m.IsEmpty() {
		return false
	}
	// start with directories as we always need to check these
	for _, dirLine := range m.dirRules {
		// contains because may be subdir already
		if strings.Contains(path, dirLine) {
			return true
		}
	}
	// make sure the path IS a file (no need to check anything otherwise)
	info, err := os.Lstat(path)
	if err != nil {
		return false
	}
	// no need to check file stuff if path points to directory
	if !info.IsDir() {
		// check files
		for _, fileLine := range m.fileRules {
			// suffix because rest of path doesn't matter for file matches
			if strings.HasSuffix(path, fileLine) {
				return true
			}
		}
	}
	return false
}

/*
IsEmpty can be used to see if the matcher contains any rules at all.
*/
func (m *matcher) IsEmpty() bool {
	return !m.used
}

/*
Same returns true if the path is the path for this matcher.
*/
func (m *matcher) Same(path string) bool {
	return path == m.root
}

/*
Resolve the matcher for the given path from the bottom up. If no matcher is found
on any subpath, the original matcher is returned.
*/
func (m *matcher) Resolve(path *relativePath) *matcher {
	for hasTinIgnore(path.FullPath()) != true {
		path = path.Up()
	}
	matcher, err := createMatcher(path.FullPath())
	if err != nil {
		return m
	}
	if matcher.Same(m.root) {
		return m
	}
	return matcher
}

func (m *matcher) String() string {
	return "Matcher of <" + m.root + ">"
}

/*
ReadTinIgnore reads the .tinignore file in the given path if it exists. If not
or some other error happens it returns ErrNoTinIgnore.
*/
func readTinIgnore(path string) ([]string, error) {
	data, err := ioutil.ReadFile(path + "/" + TINIGNORE)
	if err != nil {
		// TODO is this correct? Can I be sure that I don't want to know what
		//	    other errors may happen here?
		return nil, ErrNoTinIgnore
	}
	// sanitize (remove empty lines)
	list := strings.Split(string(data), "\n")
	var sanitized []string
	for _, value := range list {
		// filter out comments
		if strings.HasPrefix(value, "#") {
			continue
		}
		// ignore empty lines
		if value == "" {
			continue
		}
		sanitized = append(sanitized, value)
	}
	return sanitized, nil
}

/*
hasTinIgnore checks whether the path has a .tinignore file.
*/
func hasTinIgnore(path string) bool {
	_, err := ioutil.ReadFile(path + "/" + TINIGNORE)
	return err == nil
}
