package model

import (
	"errors"
	"time"
)

/*
private errors
*/
var (
	errMismatch             = errors.New("mismatch in structs")
	errModelInconsitent     = errors.New("model tracked and staticinfo are inconsistent")
	errMissingUpdateFile    = errors.New("file for update missing from temp")
	errIncompatibleModel    = errors.New("model is incompatible")
	errParentObjectsMissing = errors.New("missing parent objects")
	errObjectUntracked      = errors.New("object untracked")
	errFilter               = errors.New("filter found illegal values")
)

/*
public errors
*/
var (
	ErrUpdateKnown   = errors.New("update is already incorporated")
	ErrObjectRemoved = errors.New("object removed")
)

var tag = "Model:"

/*
removalTimeout is the timeout after which removals are considered orphaned and
Model will warn of them.

TODO: change to sensible value
*/
const removalTimeout = 2 * time.Hour

/*
removalLocal is the timeout after which a peer will forget about a removal
locally.

TODO: change to sensible value
*/
const removalLocal = 1 * time.Hour
