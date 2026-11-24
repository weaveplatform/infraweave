// Package signertest is the contract every KMS signing backend passes:
// Contract signs a fixed JWS through the backend under an algorithm,
// verifies it with go-jose under the backend's public key, checks that a
// changed payload does not verify, and that a digest of the wrong length
// is refused before the KMS is asked. Fake models a KMS's key with a
// local P-256 key (NewFake), RSA 3072 key (NewRSAFake, generated once per
// test binary) or RSA 2048 key (NewRSA2048Fake) for the backends' fakes,
// answering each KMS's signature form.
//
// # Input and output
//
// Contract calls the signer it is given and reports through testing.T. It
// reads and writes nothing else.
//
// # Dependencies
//
// It imports platform/crypt/signer and github.com/go-jose/go-jose/v4. Only
// tests import it: the backends' and internal/app's.
package signertest
