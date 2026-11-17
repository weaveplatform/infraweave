package spacelift

deny contains msg if {
	input.spacelift.run.type == "PROPOSED"
	msg := "proposed"
}
