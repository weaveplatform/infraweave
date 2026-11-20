#!/bin/sh
set -eu
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
export PULUMI_CONFIG_PASSPHRASE=runner-image-test PULUMI_SKIP_UPDATE_CHECK=true
pulumi version
expected=${RUNNER_TOOL_VERSION:-$(jq -r '.tools.pulumi.version' /opt/image-tests/versions.json)}
[ "$(pulumi version)" = "v$expected" ]
mkdir "$work/state"
pulumi login "file://$work/state"
for language in nodejs python; do
 mkdir "$work/$language"
 cd "$work/$language"
 printf 'name: image-%s\nruntime: %s\n' "$language" "$language" > Pulumi.yaml
 if [ "$language" = nodejs ]; then
  cp /opt/image-tests/tests/pulumi-package.json package.json
  cp /opt/image-tests/tests/pulumi-package-lock.json package-lock.json
  cp /opt/image-tests/tests/pulumi.js index.js
  npm ci --ignore-scripts --no-audit --no-fund
 else
  python3 -m venv venv
  venv/bin/pip install --disable-pip-version-check --no-cache-dir 'pulumi==3.277.0' 'pulumi-random==4.21.6'
  printf 'name: image-python\nruntime:\n  name: python\n  options:\n    virtualenv: venv\n' > Pulumi.yaml
  cp /opt/image-tests/tests/pulumi.py __main__.py
 fi
 pulumi stack init test --non-interactive
 pulumi preview --non-interactive --diff
 pulumi up --yes --non-interactive --skip-preview
 pulumi stack output --json > outputs.json
 jq -e '.proof == "runner-image-contract" and (.value | length) == 12' outputs.json
 pulumi stack export > state.json
 jq -e '[.deployment.resources[] | select(.type == "random:index/randomString:RandomString")] | length == 1' state.json
 pulumi destroy --yes --non-interactive --skip-preview
 pulumi stack rm --yes --non-interactive
done
echo "Pulumi musl compatibility: real Node and Python provider programs passed"
