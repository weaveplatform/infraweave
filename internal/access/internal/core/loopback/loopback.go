package loopback

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha512"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
)

// MinKeyBits is the smallest RSA key Seal encrypts to: FIPS 140-3's
// floor. spacectl's are 4096 bits.
const MinKeyBits = 2048

// keyLen is the AES key's length: AES-256.
const keyLen = 32

// ErrKey reports a public key Seal cannot encrypt to; the error says why.
var ErrKey = errors.New("invalid public key")

// b64 is unpadded base64url, as the CLI reads both halves.
var b64 = base64.RawURLEncoding

// Seal encrypts token to pubKey, the CLI's RSA public key in PEM, or that
// PEM in base64url as the CLI puts it in the login URL. It answers
// <key>.<token>: key is a fresh AES-256 key encrypted with RSA-OAEP over
// SHA-512, and token is the token encrypted with AES-CBC under that key,
// its first block the IV and its plaintext PKCS#7-padded, both in
// unpadded base64url. The browser splits it at the dot into the key and
// token query parameters of the CLI's loopback callback, and only the CLI,
// which holds the private key, can read the token.
func Seal(pubKey, token string) (string, error) {
	pub, err := parse(pubKey)
	if err != nil {
		return "", err
	}
	key := make([]byte, keyLen)
	_, _ = rand.Read(key) // crypto/rand never fails
	wrapped, err := rsa.EncryptOAEP(sha512.New(), rand.Reader, pub, key, nil)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrKey, err)
	}
	block, _ := aes.NewCipher(key) // a 32-byte key is always valid
	pad := aes.BlockSize - len(token)%aes.BlockSize
	plain := append([]byte(token), bytes.Repeat([]byte{byte(pad)}, pad)...)
	out := make([]byte, aes.BlockSize+len(plain))
	_, _ = rand.Read(out[:aes.BlockSize])
	//nolint:gosec // G407 takes the IV for make's zeroes; rand.Read fills it above
	cipher.NewCBCEncrypter(block, out[:aes.BlockSize]).CryptBlocks(out[aes.BlockSize:], plain)
	return b64.EncodeToString(wrapped) + "." + b64.EncodeToString(out), nil
}

// parse reads an RSA public key of at least MinKeyBits from PEM, or from
// PEM in base64url with or without padding, in PKIX or PKCS #1 form.
func parse(s string) (*rsa.PublicKey, error) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "-----BEGIN") {
		raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "="))
		if err != nil {
			return nil, fmt.Errorf("%w: neither PEM nor PEM in base64url", ErrKey)
		}
		s = string(raw)
	}
	blk, _ := pem.Decode([]byte(s))
	if blk == nil {
		return nil, fmt.Errorf("%w: no PEM block", ErrKey)
	}
	var pub *rsa.PublicKey
	if k, err := x509.ParsePKIXPublicKey(blk.Bytes); err == nil {
		var ok bool
		if pub, ok = k.(*rsa.PublicKey); !ok {
			return nil, fmt.Errorf("%w: not an RSA key", ErrKey)
		}
	} else if pub, err = x509.ParsePKCS1PublicKey(blk.Bytes); err != nil {
		return nil, fmt.Errorf("%w: not an RSA public key", ErrKey)
	}
	if pub.N.BitLen() < MinKeyBits {
		return nil, fmt.Errorf("%w: %d bits, fewer than %d", ErrKey, pub.N.BitLen(), MinKeyBits)
	}
	return pub, nil
}
