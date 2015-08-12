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
)

var tag = "Model:"

/*
removalTimeout is the timeout after which removals are considered orphaned and
Model will warn of them.
*/
const removalTimeout = 24 * time.Hour
