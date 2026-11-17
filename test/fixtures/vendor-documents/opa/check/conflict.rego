package spacelift

sample := true if input.spacelift.run.type == "TRACKED"

sample := false if input.spacelift.run.state == "PLANNING"
