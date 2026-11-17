package spacelift

deny contains msg if {
	msg := sprintf("too many: %d", [count(input.spacelift.run.changes)]
}

warn contains undefined_helper(input)
