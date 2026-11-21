#!/bin/sh
# Runs with --network none and no tool mounts: the image must be usable alone.
set -eu
case "$RUNNER_KIND" in terraform) tool=terraform ;; opentofu) tool=tofu ;; *) exit 1 ;; esac
[ "$(command -v "$tool")" = "/bin/$tool" ]
[ -s "/usr/share/licenses/$RUNNER_KIND/LICENSE" ]
[ "$("$tool" version -json | jq -r .terraform_version)" = "$RUNNER_TOOL_VERSION" ]
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
cd "$work"
export TF_IN_AUTOMATION=1 CHECKPOINT_DISABLE=1
cat > main.tf <<'TF'
resource "terraform_data" "example" { input = "baked-runner-image-contract" }
output "proof" { value = terraform_data.example.output }
TF
"$tool" init -input=false
"$tool" plan -input=false -out=plan
"$tool" show -json plan > plan.json
jq -e '.resource_changes | any(.address == "terraform_data.example")' plan.json
"$tool" apply -input=false plan
[ "$("$tool" output -raw proof)" = baked-runner-image-contract ]
"$tool" plan -input=false -detailed-exitcode
"$tool" destroy -input=false -auto-approve
[ "$("$tool" state list | wc -l | tr -d ' ')" = 0 ]
echo "baked-tool offline contract: $tool $RUNNER_TOOL_VERSION passed"
