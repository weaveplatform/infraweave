package private

import "errors"

// ErrShared is a file others than its owner may reach.
var ErrShared = errors.New("the file is not its owner's alone")
