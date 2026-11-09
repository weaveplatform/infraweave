// Package mask hides secret values in text before it leaves the process
// that holds them. The server masks evaluated expressions and anything else
// that renders a secret with it, and the agent masks run logs with the same
// code, so the two cannot disagree on what is hidden.
//
// New builds an Aho-Corasick automaton over the secrets' literal values,
// split into lines; lines shorter than MinLen are left visible. Every byte
// that lies inside an occurrence of a secret is masked, and each maximal
// run of such bytes, however many overlapping or adjacent occurrences make
// it, becomes Replacement once. A Masker never changes after New.
//
// Masking is best effort: an encoded, split or transformed secret is not
// recognised, and a secret that spans lines is masked only line by line.
//
// ref: decision 0013
//
// # Input and output
//
// String and Bytes take text and return it masked. Writer wraps an
// io.Writer: it buffers each line until its newline, keeps at most MaxLine
// bytes of it, masks it and writes it on, so a secret split across writes
// is masked; Close writes the last, unterminated line. A wrapped writer's
// error is returned and kept. Nothing is logged.
//
// # Dependencies
//
// It imports only the standard library: bytes, errors, io and strings.
// The server's domains and internal/agent import it.
package mask
