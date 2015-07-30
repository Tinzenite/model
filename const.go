package model

import "errors"

var (
	errMismatch          = errors.New("mismatch in structs")
	errModelInconsitent  = errors.New("model tracked and staticinfo are inconsistent")
	errMissingUpdateFile = errors.New("file for update missing from temp")
	errIncompatibleModel = errors.New("model is incompatible")
)

var tag = "Model:"
