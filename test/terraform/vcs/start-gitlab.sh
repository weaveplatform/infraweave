#!/usr/bin/env bash
# Starts the GitLab lab's container: GitLab Community Edition serving
# https://localhost:8443 under a certificate made here, with an
# administrator's personal access token made for the run. GitLab.com
# offers group and project access tokens only on paid tiers, and the lab
# needs both; a self-managed instance offers them on every tier.
#
#   start-gitlab.sh <image> <dir>
#
# It writes to dir the certificate (cert.pem), a bundle of it and the
# system's roots (bundle.pem), and env, the variables the GitLab root,
# seed.sh and the tests read, as KEY=VALUE lines; env holds the token, so
# a caller masks it before printing any. A first start takes several
# minutes.
set -euo pipefail

image=${1:?usage: start-gitlab.sh <image> <dir>}
dir=${2:?usage: start-gitlab.sh <image> <dir>}
host=https://localhost:8443

mkdir -p "$dir/ssl"
openssl req -x509 -newkey rsa:3072 -nodes -days 1 -subj '/CN=localhost' \
  -addext 'subjectAltName=DNS:localhost,IP:127.0.0.1' \
  -keyout "$dir/ssl/key.pem" -out "$dir/cert.pem" 2>/dev/null
cp "$dir/cert.pem" "$dir/ssl/cert.pem"
chmod 0644 "$dir/ssl/key.pem"
roots=/etc/ssl/certs/ca-certificates.crt
[ -f "$roots" ] || roots=/etc/ssl/cert.pem
cat "$roots" "$dir/cert.pem" > "$dir/bundle.pem"

# The services the lab never calls stay off, which shortens the start.
docker run -d --name gitlab -p 8443:8443 --shm-size 256m \
  -v "$dir/ssl:/lab-ssl:ro" \
  -e GITLAB_OMNIBUS_CONFIG="external_url '$host'
letsencrypt['enable'] = false
nginx['ssl_certificate'] = '/lab-ssl/cert.pem'
nginx['ssl_certificate_key'] = '/lab-ssl/key.pem'
prometheus_monitoring['enable'] = false
registry['enable'] = false
gitlab_kas['enable'] = false
gitlab_rails['usage_ping_enabled'] = false" \
  "$image" > /dev/null

status=""
for _ in $(seq 180); do
  status=$(curl -s -o /dev/null -w '%{http_code}' --cacert "$dir/cert.pem" "$host/users/sign_in" || true)
  [ "$status" = 200 ] && break
  sleep 5
done
if [ "$status" != 200 ]; then
  echo "GitLab did not start: its sign-in page answers ${status:-nothing}" >&2
  docker logs --tail 50 gitlab >&2
  exit 1
fi

# GitLab takes a programmatic token of exactly 20 characters. It goes to
# the container in the environment, never as an argument.
LAB_TOKEN=$(openssl rand -hex 10)
export LAB_TOKEN
docker exec -e LAB_TOKEN gitlab gitlab-rails runner '
  t = User.find_by_username("root").personal_access_tokens.create!(
    name: "infraweave-lab", scopes: ["api"], expires_at: 2.days.from_now)
  t.set_token(ENV.fetch("LAB_TOKEN"))
  t.save!
'

cat > "$dir/env" <<EOF
GITLAB_TOKEN=$LAB_TOKEN
TF_VAR_host=$host
TF_VAR_ca_file=$dir/cert.pem
SEED_USER=oauth2
SEED_TOKEN=$LAB_TOKEN
GIT_SSL_CAINFO=$dir/bundle.pem
INFRAWEAVE_TEST_VCS_CA_FILE=$dir/cert.pem
EOF
chmod 0600 "$dir/env"
