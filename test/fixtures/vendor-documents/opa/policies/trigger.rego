package spacelift

trigger contains stack.id if {
	input.run.state == "FINISHED"
	input.run.type == "TRACKED"
	some stack in input.stacks
	stack.id == "stack-b"
}

sample := true
