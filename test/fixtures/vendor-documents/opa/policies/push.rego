package spacelift

track if {
	input.push.branch == input.stack.branch
	not ignore
}

propose if input.push.branch != input.stack.branch

ignore if contains(input.push.message, "[ignore]")

cancel contains run.id if {
	contains(input.push.message, "[cancel]")
	some run in input.in_progress
}

sample := true
