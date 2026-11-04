# Reduces golangci-lint's findings on this module to "file check import",
# so expected.txt names each violation's file and the rule that caught it
# without depending on line numbers or messages. A line no expression
# matches passes through whole and fails the diff.
s|^test/depguard/||
s|^([^:]+):[0-9]+:[0-9]+: import '([^']+)' is not allowed from list '([^']+)'.*\(depguard\)$|\1 \3 \2|
s|^([^:]+):[0-9]+:[0-9]+: use of `([^`]+)` forbidden .*\(forbidigo\)$|\1 forbidigo \2|
s|^([^:]+):[0-9]+:[0-9]+: use of internal package ([^ ]+) not allowed \(typecheck\)$|\1 compiler \2|
