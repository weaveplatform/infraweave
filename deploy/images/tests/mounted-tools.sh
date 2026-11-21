#!/bin/sh
# Standalone compatibility proof; launcher acceptance separately proves its
# signed download and read-only mount path with the current application.
set -eu
arch=$(uname -m)
case "$arch" in x86_64) arch=amd64 ;; aarch64) arch=arm64 ;; *) exit 1 ;; esac
case "$RUNNER_KIND" in terraform) tool=terraform ;; opentofu) tool=tofu ;; esac
inputs=/opt/image-tests/versions.json
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
mkdir "$work/bin"
export PATH="$work/bin:$PATH" TF_IN_AUTOMATION=1 CHECKPOINT_DISABLE=1
for cohort in current previous; do
 selector=".tools.$RUNNER_KIND"
 if [ "$cohort" = current ] && [ -n "${RUNNER_TOOL_VERSION:-}" ]; then
  selector="([.tools.$RUNNER_KIND] + (.cohorts.$RUNNER_KIND // []) | map(select(.version == \"$RUNNER_TOOL_VERSION\")) | first)"
 fi
 [ "$cohort" = current ] || selector="$selector.previous"
 version=$(jq -r "$selector.version" "$inputs")
 file="${tool}_${version}_linux_${arch}.zip"
 sum=$(jq -r "$selector.sha256[\"$file\"]" "$inputs")
 case "$tool" in
  terraform) url="https://releases.hashicorp.com/terraform/$version/$file" ;;
  tofu) url="https://github.com/opentofu/opentofu/releases/download/v$version/$file" ;;
 esac
 curl -fsSL --retry 3 "$url" -o "$work/tool.zip"
 echo "$sum  $work/tool.zip" | sha256sum -c -
 unzip -oq "$work/tool.zip" "$tool" -d "$work/bin"
 chmod 755 "$work/bin/$tool"
 "$tool" version
 mkdir "$work/$cohort"
 cd "$work/$cohort"
 cat > main.tf <<'TF'
resource "terraform_data" "example" { input = "runner-image-contract" }
output "proof" { value = terraform_data.example.output }
TF
 "$tool" init -input=false
 "$tool" plan -input=false -out=plan
 "$tool" apply -input=false plan
 [ "$("$tool" output -raw proof)" = runner-image-contract ]
 "$tool" plan -input=false -detailed-exitcode
done
for version in $(jq -r '[.tools.terragrunt.version] + [(.cohorts.terragrunt // [])[].version] | unique[]' "$inputs"); do
file="terragrunt_linux_$arch"
sum=$(jq -r "([.tools.terragrunt] + (.cohorts.terragrunt // []) | map(select(.version == \"$version\")) | first).sha256[\"$file\"]" "$inputs")
curl -fsSL --retry 3 "https://github.com/gruntwork-io/terragrunt/releases/download/v$version/$file" -o "$work/bin/terragrunt"
echo "$sum  $work/bin/terragrunt" | sha256sum -c -
chmod 755 "$work/bin/terragrunt"
: > terragrunt.hcl
export TG_TF_PATH="$work/bin/$tool"
terragrunt --version
terragrunt plan -input=false -detailed-exitcode
done
echo "mounted-tool compatibility: current, previous, and Terragrunt passed"
