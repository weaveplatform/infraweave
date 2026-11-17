package spacelift

deny[msg] {
	input.spacelift.run.type == "PROPOSED"
	msg := "proposed"
}
