#!/bin/sh
# Fixture production only. Never publishes an image or invokes apply/actions.
set -eu
here=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
output=${1:?usage: reproduce.sh NEW_OUTPUT_DIRECTORY}
mkdir "$output"
output=$(CDPATH= cd -- "$output" && pwd)
git clone --depth 1 --branch v1.17.2 https://github.com/hashicorp/terraform.git "$output/source"
[ "$(git -C "$output/source" rev-parse HEAD)" = ae094fc0ee6d4f3d2a990b40ac48a6d2dbaeb53e ]
git -C "$output/source" apply "$here/producer.patch"
(
 cd "$output/source"
 GOWORK=off GOTOOLCHAIN=go1.27.4 CGO_ENABLED=0 go build -ldflags '-X main.experimentsAllowed=yes' -o "$output/terraform" .
)
for project in resource-prerequisite count-unknown; do
 directory="$output/$project"
 mkdir "$directory"
 fixture=$here
 [ "$project" != count-unknown ] || fixture="$here/count-unknown"
 cp "$fixture/main.tf" "$fixture/.terraform.lock.hcl" "$directory/"
 (
  cd "$directory"
  "$output/terraform" init -input=false > init.stdout 2> init.stderr
  if [ "$project" = resource-prerequisite ]; then
   "$output/terraform" plan -input=false -allow-deferral=true -var=secret=dummy-action-secret-675 -out=proof.tfplan > plan.stdout 2> plan.stderr
  else
   "$output/terraform" plan -input=false -allow-deferral=true -out=proof.tfplan > plan.stdout 2> plan.stderr
  fi
  "$output/terraform" show -json proof.tfplan > plan.json 2> show.stderr
 )
done
