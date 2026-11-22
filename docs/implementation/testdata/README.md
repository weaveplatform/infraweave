# Documentation tool examples

`dataview/` holds the original register examples, Markdown golden output and
ownership-label ordering cases used by the documentation generator tests.
`docscheck/` holds the original plan/register examples, deliberately invalid
ownership assignments and expected diagnostics used by the documentation checks.

These examples describe documentation syntax. They are not product configuration
or runtime metadata. Golden files use `.golden` because their relative links and
headings belong to the synthetic repository assembled by the tests. Test readers
normalize CRLF to LF so the examples compare identically on every supported OS.
