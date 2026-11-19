// Package sanitize hashes Terraform-family plan value strings before their
// JSON reaches server-readable artifacts. DEFAULT hashes all applicable
// values; SMART follows sensitivity markers and configuration constants;
// SMART also hashes identical copies of strings known to be sensitive in the
// same document, including across units, without hashing structural metadata.
// NONE validates the document and preserves its original bytes. Structural
// metadata and non-string JSON values remain unchanged. Terragrunt units
// are transformed independently. The binary plan is never read or changed.
//
// # Input and output
//
// Plan accepts format 1.x JSON or a Terragrunt units envelope and a Mode.
// ErrMalformed, ErrTooLarge and ErrMode identify refused inputs; reader and
// writer errors are wrapped. Value implements the shared sanitized() hash.
//
// # Effects
//
// Plan reads at most MaxSize bytes plus one from its reader and writes only
// after the entire document passes validation. It touches no file, network,
// environment, clock or log. Writer failure may leave a partial sanitized
// document; callers must not publish it or fall back to the original.
//
// # Dependencies
//
// Standard library only. The agent and server may import it. MaxSize matches
// the plan parser's input bound; no policy engine or domain is imported.
package sanitize
