package model

import (
	"errors"
	"time"
)

var (
	errMismatch             = errors.New("mismatch in structs")
	errModelInconsitent     = errors.New("model tracked and staticinfo are inconsistent")
	errMissingUpdateFile    = errors.New("file for update missing from temp")
	errIncompatibleModel    = errors.New("model is incompatible")
	errParentObjectsMissing = errors.New("missing parent objects")
	errObjectUntracked      = errors.New("object untracked")
)

var tag = "Model:"

/*
removalTimeout is the timeout after which removals are considered orphaned and
Model will warn of them.
*/
const removalTimeout = 24 * time.Hour

/*
removalLocal is the timeout after which a peer will forget about a removal
locally.
*/
const removalLocal = 24 * time.Hour
