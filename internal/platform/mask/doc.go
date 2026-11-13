// Package mask hides secret values in text before it leaves the process
// that holds them. The server masks evaluated expressions and anything else
// that renders a secret with it, and the agent masks run logs with the same
// code, so the two cannot disagree on what is hidden.
//
// New builds an Aho-Corasick automaton over the secrets' literal values,
// split into lines; lines shorter than MinLen are left visible. With
// answers a new Masker that masks more words, as a run's ::add-mask does,
// and WellKnown one that also masks the secrets of well-known patterns:
// cloud access keys, client secrets, connection string keys,
// GitHub, Slack and JSON web tokens, as regular expressions, and private
// key blocks in PEM or PGP armour. Every byte that lies inside an
// occurrence of a secret is masked, and each maximal run of such bytes,
// however many overlapping or adjacent occurrences make it, becomes
// Replacement once. A Masker never changes after it is made.
//
// A private key block spans lines: its BEGIN line opens it, and every
// line up to its END line is masked, but for the spaces around it. A line
// that is not a key's base64 or header closes a block whose END line
// never came. Bytes and String mask a block within the text they are
// given; Lines carries one open block from one line to the next.
//
// Masking is best effort: an encoded, split or transformed secret is not
// recognised, and a secret that spans lines is masked only line by line.
//
// ref: decision 0013
//
// # Input and output
//
// String and Bytes take text and return it masked. Lines.Mask takes one
// line at a time. Writer wraps an io.Writer: it buffers each line until
// its newline, keeps at most MaxLine bytes of it, masks it and writes it
// on, so a secret split across writes is masked; Close writes the last,
// unterminated line. A wrapped writer's error is returned and kept.
// Nothing is logged.
//
// # Dependencies
//
// It imports only the standard library: bytes, errors, io, regexp, slices
// and strings.
// The server's domains and internal/agent import it.
package mask
