package seal

import (
	"crypto/hkdf"
	"crypto/sha256"
	"fmt"
)

// artifactInfo opens the HKDF info of an artifact's content key, which
// goes on to name the object's key.
const artifactInfo = "infraweave/artifact/"

// ArtifactKey is the content key of the run artifact stored under key:
// HKDF-SHA256 of the run's artifact key, with an empty salt and the info
// infraweave/artifact/<key>. The runner seals the object under it, and
// the server derives it again to read the object, so no content key
// travels and each object has its own.
//
// ref: decision 0013 § "Sealed objects"
func ArtifactKey(artifact []byte, key string) ([]byte, error) {
	if len(artifact) != KeySize {
		return nil, ErrKeySize
	}
	k, err := hkdf.Key(sha256.New, artifact, nil, artifactInfo+key, KeySize)
	if err != nil {
		return nil, fmt.Errorf("seal: %w", err)
	}
	return k, nil
}
