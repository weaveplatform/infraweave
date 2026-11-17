package spacelift

resources := [change |
	some change in input.spacelift.run.changes
	change.entity.entity_type == "resource"
]

warn contains sprintf("warn: %d resource(s) affected", [count(resources)]) if count(resources) > 0

deny contains "deny: commit message asks for a deny" if {
	input.spacelift.run.type == "PROPOSED"
	contains(input.spacelift.run.commit.message, "[deny]")
}

flag contains "plan-flag"

sample := true
