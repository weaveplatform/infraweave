// Package signertest is the contract every KMS signing backend passes:
// Contract signs a fixed JWS through the backend, verifies it with
// go-jose under the backend's public key, checks that a changed payload
// does not verify, and that a digest of the wrong length is refused before
// the KMS is asked. Fake models a KMS's key with a local P-256 key for the
// backends' fakes.
//
// # Input and output
//
// Contract calls the signer it is given and reports through testing.T. It
// reads and writes nothing else.
//
// # Dependencies
//
// It imports platform/crypt/signer and github.com/go-jose/go-jose/v4. Only
// the backends' tests import it.
package signertest
