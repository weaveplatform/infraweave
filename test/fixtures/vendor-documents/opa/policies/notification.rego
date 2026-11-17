package spacelift

inbox contains {
	"title": sprintf("Run %s", [input.run_updated.state]),
	"body": sprintf("The run on %s is %s.", [input.run_updated.stack.id, input.run_updated.state]),
	"severity": "INFO",
} if input.run_updated.state in {"FINISHED", "FAILED", "UNCONFIRMED", "CANCELED", "DISCARDED"}

sample := true
