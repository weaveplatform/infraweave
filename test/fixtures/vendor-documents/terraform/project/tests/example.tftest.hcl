variables {
  api_key       = "dummy-api-key-test"
  session_token = "dummy-session-token-test"
}

run "plan_pet" {
  command = plan

  assert {
    condition     = random_pet.name.length == 3
    error_message = "The pet has three words."
  }
}

run "apply_file" {
  assert {
    condition     = startswith(local_file.rendered.content, "name=")
    error_message = "The rendered file starts with the name."
  }
}

run "failing" {
  assert {
    condition     = random_pet.name.length == 5
    error_message = "The pet has five words."
  }
}
