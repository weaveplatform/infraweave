package spacelift_test

import data.spacelift

test_warns_on_a_change if {
	spacelift.warn["warn: 1 resource(s) affected"] with input as {"spacelift": {"run": {"type": "TRACKED", "commit": {"message": "x"}, "changes": [{"entity": {"entity_type": "resource"}}]}}}
}

test_denies_a_proposed_run if {
	count(spacelift.deny) == 1 with input as {"spacelift": {"run": {"type": "PROPOSED", "commit": {"message": "[deny]"}, "changes": []}}}
}

test_fails_on_purpose if {
	count(spacelift.deny) == 1 with input as {"spacelift": {"run": {"type": "TRACKED", "commit": {"message": "[deny]"}, "changes": []}}}
}
