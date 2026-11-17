package spacelift

warn contains msg if {
	msg := undefined_helper(input)
}

flag contains input.missing[_] if unknown_var > 1
