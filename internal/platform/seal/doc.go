// Package seal is infraweave's sealed formats, and holds no keys: the
// server's keyring is platform/crypt's, and the runner, which may import
// this package but never crypt, brings keys of its own. Every format starts
// with the version byte 0x01 and seals with AES-256-GCM through
// cipher.NewGCMWithRandomNonce, which FIPS 140-only mode allows.
//
//   - The column format, SealColumn and OpenColumn: 0x01 | data key ID
//     (uint32, big-endian) | nonce | ciphertext and tag, with the column's
//     <table>.<column>:<primary key> as associated data. ColumnKeyID reads
//     the key ID, so the keyring can pick the key.
//   - The object stream, Writer and Reader: the header IWSB 0x01, then
//     segments of SegmentSize plaintext, each authenticating IWSB | 0x01 |
//     index | last flag, so a reordered, truncated or extended object fails.
//     Each side holds one segment at a time; the Writer refuses a 2^32nd
//     segment. ObjectSize gives the sealed size of a plaintext size, so a
//     store can declare it before it writes.
//   - The pool envelope, SealEnvelope and OpenEnvelope: 0x01 | ephemeral
//     P-256 public key | nonce | ciphertext and tag, under HKDF-SHA256 of
//     an ECDH shared secret with the pool's key. The associated data is
//     the version byte, the run ID behind its uint16 length and the state
//     version as a uint64, so a spec opens only for the run and execution
//     it was issued for.
//
// A value that fails to open reports ErrAuth whatever the cause, ErrFormat
// when it is too short or malformed to try, and ErrVersion for another
// version byte.
//
// ref: decisions 0004 and 0013
//
// # Input and output
//
// The column and envelope functions take and return byte slices. Writer
// writes to and Reader reads from the io.Writer and io.Reader they wrap,
// whose errors they return wrapped. Nothing is logged.
//
// # Dependencies
//
// It imports only the standard library: crypto/aes, crypto/cipher,
// crypto/ecdh, crypto/hkdf, crypto/sha256 and crypto/rand. platform/crypt
// builds the keyring on it, and internal/agent may import it.
package seal
